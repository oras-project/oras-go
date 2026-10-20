/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/container/set"
)

// rebuildManifests is the oracle of these tests: it builds the manifests of
// `index.json` from scratch out of the references of a resolver, the way
// saveIndexFrom did before the list was kept incrementally. It is deliberately
// independent of the code under test, and returns the entries in a canonical
// order, since the order of the manifests is not specified.
func rebuildManifests(refMap map[string]ocispec.Descriptor) []string {
	var entries []ocispec.Descriptor
	var untagged []ocispec.Descriptor
	tagged := set.New[digest.Digest]()
	for ref, desc := range refMap {
		if ref == desc.Digest.String() {
			untagged = append(untagged, desc)
			continue
		}
		annotations := map[string]string{ocispec.AnnotationRefName: ref}
		maps.Copy(annotations, desc.Annotations)
		annotations[ocispec.AnnotationRefName] = ref
		desc.Annotations = annotations
		entries = append(entries, desc)
		tagged.Add(desc.Digest)
	}
	for _, desc := range untagged {
		if !tagged.Contains(desc.Digest) {
			desc.Annotations = maps.Clone(desc.Annotations)
			delete(desc.Annotations, ocispec.AnnotationRefName)
			if len(desc.Annotations) == 0 {
				desc.Annotations = nil
			}
			entries = append(entries, desc)
		}
	}
	return canonicalManifests(entries)
}

// canonicalManifests returns the JSON of the descriptors in a canonical order.
func canonicalManifests(descs []ocispec.Descriptor) []string {
	out := make([]string, 0, len(descs))
	for _, desc := range descs {
		b, err := json.Marshal(desc)
		if err != nil {
			panic(err)
		}
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

func cloneManifests(manifests []ocispec.Descriptor) []ocispec.Descriptor {
	cloned := make([]ocispec.Descriptor, len(manifests))
	for i, manifest := range manifests {
		cloned[i] = manifest
		if manifest.Annotations != nil {
			cloned[i].Annotations = make(map[string]string, len(manifest.Annotations))
			for k, v := range manifest.Annotations {
				cloned[i].Annotations[k] = v
			}
		}
	}
	return cloned
}

// refDigests returns the digest that each reference of the store resolves to.
func refDigests(s *Store) map[string]digest.Digest {
	out := make(map[string]digest.Digest)
	for ref, desc := range s.tagResolver.All() {
		out[ref] = desc.Digest
	}
	return out
}

// assertManifestsMatchResolver fails the test unless the manifests of the
// in-memory index equal what a rebuild from the resolver gives, and the
// manifest list is internally consistent. It also checks `index.json` if
// checkFile is set, which holds only once the index has been saved.
func assertManifestsMatchResolver(t *testing.T, s *Store, checkFile bool) {
	t.Helper()

	want := rebuildManifests(s.tagResolver.Map())
	if got := canonicalManifests(s.index.Manifests); !slices.Equal(got, want) {
		t.Errorf("in-memory index.Manifests =\n%s\nwant\n%s", got, want)
	}

	if l := s.manifests; l.resolver == s.tagResolver {
		if len(l.manifests) != len(l.refs) || len(l.manifests) != len(l.refIndexes) {
			t.Fatalf("manifest list sizes differ: manifests=%d refs=%d refIndexes=%d", len(l.manifests), len(l.refs), len(l.refIndexes))
		}
		for i, key := range l.refs {
			if l.refIndexes[key] != i {
				t.Fatalf("refIndexes[%q] = %d, want %d", key, l.refIndexes[key], i)
			}
		}
	}

	if !checkFile {
		return
	}
	indexJSON, err := os.ReadFile(s.indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk ocispec.Index
	if err := json.Unmarshal(indexJSON, &onDisk); err != nil {
		t.Fatal(err)
	}
	if got := canonicalManifests(onDisk.Manifests); !slices.Equal(got, want) {
		t.Errorf("index.json manifests =\n%s\nwant\n%s", got, want)
	}
}

// toggleIndexWriteFailure makes the writes of `index.json` fail while the
// returned function is called with true. It restores fileWrite when the test
// ends.
func toggleIndexWriteFailure(t *testing.T) (setFailing func(bool)) {
	t.Helper()

	var failing atomic.Bool
	original := fileWrite
	fileWrite = func(f *os.File, data []byte) (int, error) {
		if failing.Load() && isTempFileOf(filepath.Base(f.Name()), ocispec.ImageIndexFile) {
			return 0, errors.New("no space left on device")
		}
		return original(f, data)
	}
	t.Cleanup(func() { fileWrite = original })
	return failing.Store
}

// testImages pushes images to a store, and can push them again after they
// were deleted.
type testImages struct {
	t         *testing.T
	s         *Store
	manifests []ocispec.Descriptor
	descs     [][]ocispec.Descriptor
	blobs     [][][]byte
}

func newTestImages(t *testing.T, s *Store, n int) *testImages {
	t.Helper()

	ti := &testImages{t: t, s: s}
	for i := range n {
		descs, blobs := newImage(t, fmt.Sprintf("image%d", i), 2)
		ti.manifests = append(ti.manifests, descs[len(descs)-1])
		ti.descs = append(ti.descs, descs)
		ti.blobs = append(ti.blobs, blobs)
	}
	return ti
}

// tryPush makes sure that image i is in the store.
func (ti *testImages) tryPush(i int) (ocispec.Descriptor, error) {
	for j, desc := range ti.descs[i] {
		err := ti.s.Push(context.Background(), desc, bytes.NewReader(ti.blobs[i][j]))
		if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("pushing image%d: %w", i, err)
		}
	}
	return ti.manifests[i], nil
}

// push is tryPush that fails the test on error.
func (ti *testImages) push(i int) ocispec.Descriptor {
	ti.t.Helper()

	desc, err := ti.tryPush(i)
	if err != nil {
		ti.t.Fatal(err)
	}
	return desc
}

// TestStore_IndexManifests covers the edge cases of keeping the manifests of
// the index in step with the references, checking after each step that the
// index equals a rebuild from the references.
func TestStore_IndexManifests(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	images := newTestImages(t, s, 3)
	m0, m1, m2 := images.push(0), images.push(1), images.push(2)

	step := func(name string, op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatalf("%s: error = %v", name, err)
		}
		assertManifestsMatchResolver(t, s, true)
	}
	entriesOf := func(dgst digest.Digest) (tagged, plain int) {
		for _, desc := range s.index.Manifests {
			if desc.Digest != dgst {
				continue
			}
			if desc.Annotations[ocispec.AnnotationRefName] != "" {
				tagged++
			} else {
				plain++
			}
		}
		return
	}
	wantEntries := func(name string, dgst digest.Digest, wantTagged, wantPlain int) {
		t.Helper()
		if tagged, plain := entriesOf(dgst); tagged != wantTagged || plain != wantPlain {
			t.Errorf("%s: entries of %s = %d tagged and %d plain, want %d and %d", name, dgst, tagged, plain, wantTagged, wantPlain)
		}
	}

	// the manifests were pushed without a tag, so each has a plain entry
	step("save", s.SaveIndex)
	wantEntries("pushed", m0.Digest, 0, 1)

	// a tag replaces the plain entry
	step("tag a", func() error { return s.Tag(ctx, m0, "a") })
	wantEntries("tagged", m0.Digest, 1, 0)

	// a digest with several tags has one entry per tag, and still no plain one
	step("tag b", func() error { return s.Tag(ctx, m0, "b") })
	wantEntries("two tags", m0.Digest, 2, 0)
	step("untag a", func() error { return s.Untag(ctx, "a") })
	wantEntries("one tag left", m0.Digest, 1, 0)

	// untagging the last real tag brings the plain entry back
	step("untag b", func() error { return s.Untag(ctx, "b") })
	wantEntries("untagged", m0.Digest, 0, 1)

	// a reference moved to another digest leaves no entry of the old one
	step("tag c on m0", func() error { return s.Tag(ctx, m0, "c") })
	step("tag c on m1", func() error { return s.Tag(ctx, m1, "c") })
	wantEntries("moved from", m0.Digest, 0, 1)
	wantEntries("moved to", m1.Digest, 1, 0)

	// a reference moved between digests that are both otherwise tagged
	step("tag d on m0", func() error { return s.Tag(ctx, m0, "d") })
	step("tag d on m1", func() error { return s.Tag(ctx, m1, "d") })
	wantEntries("moved from, still tagged", m0.Digest, 0, 1)
	wantEntries("moved to, two tags", m1.Digest, 2, 0)

	// tagging the same reference again changes nothing
	step("retag d", func() error { return s.Tag(ctx, m1, "d") })
	wantEntries("retagged", m1.Digest, 2, 0)

	// deleting a tagged manifest removes all of its entries
	step("delete m1", func() error { return s.Delete(ctx, m1) })
	wantEntries("deleted", m1.Digest, 0, 0)

	// GC removes the entry of an unreferenced manifest, and keeps the rest
	step("tag e on m2", func() error { return s.Tag(ctx, m2, "e") })
	step("untag e", func() error { return s.Untag(ctx, "e") })
	images.push(1)
	step("tag k on m1", func() error { return s.Tag(ctx, m1, "k") })

	// Unreferenced blobs younger than the grace period are spared, together
	// with their digest entries, so the index must keep listing them.
	step("gc within the grace period", func() error { return s.GC(ctx) })
	wantEntries("spared m2 keeps its entry", m2.Digest, 0, 1)
	wantEntries("spared m0 keeps its entry", m0.Digest, 0, 1)
	wantEntries("tagged m1 kept", m1.Digest, 1, 0)

	// Once the grace period is over they are collected, entries included.
	backdateBlobs(t, s.root)
	step("gc after the grace period", func() error { return s.GC(ctx) })

	wantEntries("after gc, untagged m2 collected", m2.Digest, 0, 0)
	wantEntries("after gc, untagged m0 collected", m0.Digest, 0, 0)
	wantEntries("after gc, tagged m1 kept", m1.Digest, 1, 0)

	// changes after GC replaced the resolver are followed
	step("tag f on m1", func() error { return s.Tag(ctx, m1, "f") })
	step("untag f", func() error { return s.Untag(ctx, "f") })
	step("delete m1", func() error { return s.Delete(ctx, m1) })
	wantEntries("after delete", m1.Digest, 0, 0)
}

// TestStore_IndexManifestsFailedSave checks that a failed write never makes the
// index, or later saves, diverge from the references.
func TestStore_IndexManifestsFailedSave(t *testing.T) {
	ctx := context.Background()
	setFailing := toggleIndexWriteFailure(t)

	t.Run("tag", func(t *testing.T) {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		images := newTestImages(t, s, 2)
		m0, m1 := images.push(0), images.push(1)
		if err := s.Tag(ctx, m0, "a"); err != nil {
			t.Fatal(err)
		}

		// Snapshot the last successfully saved index.
		before := cloneManifests(s.index.Manifests)

		// the tag is in the references even though it could not be saved, and
		// the in-memory index follows the references
		setFailing(true)
		if err := s.Tag(ctx, m1, "b"); err == nil {
			t.Fatal("Tag() succeeded despite the failing write")
		}
		setFailing(false)

		// A failed write must preserve the previous in-memory index.
		if !reflect.DeepEqual(s.index.Manifests, before) {
			t.Fatalf("index.Manifests changed after failed save:\ngot  %#v\nwant %#v",
				s.index.Manifests, before)
		}

		// The resolver contains the failed tag, so the next save must catch up.
		if err := s.SaveIndex(); err != nil {
			t.Fatal(err)
		}

		assertManifestsMatchResolver(t, s, false)

		// the next save writes everything that was missed
		if err := s.SaveIndex(); err != nil {
			t.Fatal(err)
		}

		assertManifestsMatchResolver(t, s, true)
	})

	// After GC replaced the resolver without saving the index, the first save
	// builds the list from the replacement. If that save fails, the list must
	// not be lost, since the resolver does not report its references again.
	t.Run("save after GC without autosave", func(t *testing.T) {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		images := newTestImages(t, s, 2)
		m0, m1 := images.push(0), images.push(1)
		s.AutoSaveIndex = false
		for ref, m := range map[string]ocispec.Descriptor{"a": m0, "b": m1} {
			if err := s.Tag(ctx, m, ref); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SaveIndex(); err != nil {
			t.Fatal(err)
		}
		if err := s.GC(ctx); err != nil {
			t.Fatal(err)
		}

		setFailing(true)
		if err := s.SaveIndex(); err == nil {
			t.Fatal("SaveIndex() succeeded despite the failing write")
		}
		setFailing(false)
		assertManifestsMatchResolver(t, s, false)

		if err := s.SaveIndex(); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
		if got := len(s.index.Manifests); got != 2 {
			t.Errorf("len(index.Manifests) = %d, want 2", got)
		}
	})

	// A failed GC leaves the store as it was, including its index, and the
	// store keeps following its references afterwards.
	t.Run("GC", func(t *testing.T) {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		images := newTestImages(t, s, 3)
		m0, m2 := images.push(0), images.push(2)
		// image1 is pushed but unreferenced, so GC would drop its entry
		images.push(1)
		if err := s.Tag(ctx, m0, "a"); err != nil {
			t.Fatal(err)
		}
		wantResolver := s.tagResolver
		wantLen := len(s.index.Manifests)

		setFailing(true)
		if err := s.GC(ctx); err == nil {
			t.Fatal("GC() succeeded despite the failing write")
		}
		setFailing(false)
		if s.tagResolver != wantResolver {
			t.Error("GC() replaced the resolver despite failing")
		}
		if got := len(s.index.Manifests); got != wantLen {
			t.Errorf("len(index.Manifests) after failed GC = %d, want %d", got, wantLen)
		}
		assertManifestsMatchResolver(t, s, false)

		if err := s.Tag(ctx, m2, "c"); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
		if err := s.Untag(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
	})
}

// TestStore_IndexManifestsConcurrent has tags, untags and saves race, then
// checks that the saved index equals the references.
func TestStore_IndexManifestsConcurrent(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	images := newTestImages(t, s, 4)
	for i := range 4 {
		images.push(i)
	}

	const workers, rounds = 8, 40
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 1))
			for r := range rounds {
				// workers share some references, so that they move them
				ref := fmt.Sprintf("tag%d", rng.IntN(workers*2))
				switch rng.IntN(4) {
				case 0:
					_ = s.Untag(ctx, ref)
				case 1:
					if err := s.SaveIndex(); err != nil {
						t.Error(err)
					}
				default:
					if err := s.Tag(ctx, images.manifests[(w+r)%4], ref); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	wg.Wait()

	if err := s.SaveIndex(); err != nil {
		t.Fatal(err)
	}
	assertManifestsMatchResolver(t, s, true)
}

// TestStore_IndexManifestsDifferential applies random sequences of operations
// and checks after every batch that the incrementally maintained manifests,
// the in-memory ones and those of `index.json`, equal a rebuild from scratch.
// Batches run with and without autosave, so that changes also pile up between
// saves, and some saves fail.
func TestStore_IndexManifestsDifferential(t *testing.T) {
	ctx := context.Background()
	setFailing := toggleIndexWriteFailure(t)

	const (
		seeds   = 12
		batches = 40
		nImages = 5
		nRefs   = 8
	)
	for seed := range uint64(seeds) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 7))
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			images := newTestImages(t, s, nImages)
			ref := func() string { return fmt.Sprintf("ref%d", rng.IntN(nRefs)) }

			tag := func() error {
				desc, err := images.tryPush(rng.IntN(nImages))
				if err != nil {
					return err
				}
				return s.Tag(ctx, desc, ref())
			}
			operations := []struct {
				name string
				do   func() error
			}{
				{"tag", tag}, {"tag", tag},
				{"push", func() error { _, err := images.tryPush(rng.IntN(nImages)); return err }},
				{"untag", func() error {
					err := s.Untag(ctx, ref())
					if errors.Is(err, errdef.ErrNotFound) {
						return nil
					}
					return err
				}},
				{"delete", func() error {
					err := s.Delete(ctx, images.manifests[rng.IntN(nImages)])
					if errors.Is(err, errdef.ErrNotFound) {
						return nil
					}
					return err
				}},
				{"gc", func() error { return s.GC(ctx) }},
			}

			for batch := range batches {
				s.AutoSaveIndex = rng.IntN(2) == 0
				failing := rng.IntN(6) == 0
				setFailing(failing)
				for range 1 + rng.IntN(5) {
					op := operations[rng.IntN(len(operations))]
					// an operation that saves fails when the write does
					if err := op.do(); err != nil && !failing {
						t.Fatalf("batch %d: %s error = %v", batch, op.name, err)
					}
				}
				setFailing(false)

				if err := s.SaveIndex(); err != nil {
					t.Fatalf("batch %d: SaveIndex() error = %v", batch, err)
				}
				assertManifestsMatchResolver(t, s, true)

				// a store opened on the saved layout has the same references
				if batch%10 == 9 {
					reopened, err := New(root)
					if err != nil {
						t.Fatalf("batch %d: New() error = %v", batch, err)
					}
					if got, want := refDigests(reopened), refDigests(s); !maps.Equal(got, want) {
						t.Fatalf("batch %d: reopened references = %v, want %v", batch, got, want)
					}
				}
			}
		})
	}
}

// TestStore_IndexManifestsFailedSaveRollsBackIndex verifies that a failed
// index write leaves the in-memory index unchanged and that a subsequent
// successful save reconciles the index with the resolver, including changes
// made by the operation whose initial save failed.
func TestStore_IndexManifestsFailedSaveRollsBackIndex(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	images := newTestImages(t, s, 2)
	m0, m1 := images.push(0), images.push(1)

	if err := s.Tag(ctx, m0, "a"); err != nil {
		t.Fatal(err)
	}

	// Snapshot the last successfully saved in-memory index.
	before, err := json.Marshal(s.index.Manifests)
	if err != nil {
		t.Fatal(err)
	}

	setFailing := toggleIndexWriteFailure(t)
	setFailing(true)
	err = s.Tag(ctx, m1, "b")
	setFailing(false)

	if err == nil {
		t.Fatal("Tag() succeeded despite the failing write")
	}

	// The failed write must not change the in-memory index.
	after, err := json.Marshal(s.index.Manifests)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("index.Manifests changed after failed save:\nbefore: %s\nafter:  %s", before, after)
	}

	// The resolver still contains the failed tag, so a later save must
	// persist it and bring the index back in sync.
	if err := s.SaveIndex(); err != nil {
		t.Fatal(err)
	}
	assertManifestsMatchResolver(t, s, true)
}

// TestStore_IndexManifestsGCSparedBlobs checks that the manifests GC spares
// within its grace period are listed by the index in memory and on disk right
// after GC, not only after a later save.
func TestStore_IndexManifestsGCSparedBlobs(t *testing.T) {
	ctx := context.Background()

	t.Run("autosave", func(t *testing.T) {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		images := newTestImages(t, s, 2)
		m0, m1 := images.push(0), images.push(1)
		if err := s.Tag(ctx, m0, "a"); err != nil {
			t.Fatal(err)
		}
		if err := s.GC(ctx); err != nil {
			t.Fatal(err)
		}
		// m1 is unreferenced but recent, so GC spared it
		assertManifestsMatchResolver(t, s, true)
		if got := len(s.index.Manifests); got != 2 {
			t.Errorf("len(index.Manifests) = %d, want 2 (a and the spared m1)", got)
		}

		// a store opened on the saved layout still knows the spared manifest
		reopened, err := New(s.root)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := refDigests(reopened)[m1.Digest.String()]; !ok {
			t.Error("reopened store lost the digest reference of the spared manifest")
		}

		// and changes after GC are still followed incrementally
		if err := s.Tag(ctx, m1, "b"); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
		if err := s.Untag(ctx, "b"); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
	})

	t.Run("without autosave", func(t *testing.T) {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		images := newTestImages(t, s, 2)
		m0 := images.push(0)
		images.push(1)
		if err := s.Tag(ctx, m0, "a"); err != nil {
			t.Fatal(err)
		}
		s.AutoSaveIndex = false
		if err := s.GC(ctx); err != nil {
			t.Fatal(err)
		}
		// the spared reference stays pending until the index is saved
		if err := s.SaveIndex(); err != nil {
			t.Fatal(err)
		}
		assertManifestsMatchResolver(t, s, true)
		if got := len(s.index.Manifests); got != 2 {
			t.Errorf("len(index.Manifests) = %d, want 2", got)
		}
	})
}
