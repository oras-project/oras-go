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
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
)

// layoutBuilder assembles an OCI layout in memory.
type layoutBuilder struct {
	t     *testing.T
	files fstest.MapFS
	index ocispec.Index
}

func newLayoutBuilder(t *testing.T) *layoutBuilder {
	t.Helper()
	files := validLayoutFS()
	// the placeholder that keeps blobs/sha256 in an otherwise empty layout is
	// itself unverifiable, and every layout built here has blobs
	delete(files, ocispec.ImageBlobsDir+"/sha256/.keep")
	return &layoutBuilder{t: t, files: files, index: ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}}}
}

// blob adds a blob with the given content and returns its descriptor.
func (b *layoutBuilder) blob(mediaType string, data []byte) ocispec.Descriptor {
	b.t.Helper()
	desc := ocispec.Descriptor{MediaType: mediaType, Digest: digestOf(data), Size: int64(len(data))}
	b.files[blobFilePath(data)] = &fstest.MapFile{Data: data}
	return desc
}

// manifest adds an image manifest and returns its descriptor.
func (b *layoutBuilder) manifest(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) ocispec.Descriptor {
	b.t.Helper()
	data, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    config,
		Layers:    layers,
		Subject:   subject,
	})
	if err != nil {
		b.t.Fatal(err)
	}
	return b.blob(ocispec.MediaTypeImageManifest, data)
}

// image adds a manifest with a fresh config and layer, named by seed, and
// returns the manifest, config and layer descriptors.
func (b *layoutBuilder) image(seed string, subject *ocispec.Descriptor) (manifest, config, layer ocispec.Descriptor) {
	b.t.Helper()
	config = b.blob(ocispec.MediaTypeImageConfig, []byte("config-"+seed))
	layer = b.blob(ocispec.MediaTypeImageLayer, []byte("layer-"+seed))
	return b.manifest(config, subject, layer), config, layer
}

// tag lists desc in index.json under the reference name.
func (b *layoutBuilder) tag(desc ocispec.Descriptor, ref string) {
	desc.Annotations = map[string]string{ocispec.AnnotationRefName: ref}
	b.index.Manifests = append(b.index.Manifests, desc)
}

// untagged lists desc in index.json without a reference name.
func (b *layoutBuilder) untagged(desc ocispec.Descriptor) {
	b.index.Manifests = append(b.index.Manifests, desc)
}

func (b *layoutBuilder) remove(desc ocispec.Descriptor) {
	delete(b.files, filePathOf(desc))
}

func (b *layoutBuilder) fsys() fstest.MapFS {
	b.t.Helper()
	data, err := json.Marshal(b.index)
	if err != nil {
		b.t.Fatal(err)
	}
	b.files[ocispec.ImageIndexFile] = &fstest.MapFile{Data: data}
	return b.files
}

func (b *layoutBuilder) verify() *VerifyReport {
	b.t.Helper()
	report, err := Verify(context.Background(), b.fsys(), VerifyOptions{})
	if err != nil {
		b.t.Fatal("Verify() error =", err)
	}
	return report
}

func filePathOf(desc ocispec.Descriptor) string {
	p, _, _ := blobPathOf(desc.Digest)
	return p
}

func sortedPaths(entries []VerifyEntry) []string {
	out := entryPaths(entries)
	slices.Sort(out)
	return out
}

func wantPaths(t *testing.T, name string, entries []VerifyEntry, descs ...ocispec.Descriptor) {
	t.Helper()
	want := make([]string, len(descs))
	for i, d := range descs {
		want[i] = filePathOf(d)
	}
	slices.Sort(want)
	if got := sortedPaths(entries); !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestVerify_Reachability_Clean(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, _ := b.image("a", nil)
	b.tag(m, "v1")

	report := b.verify()
	wantPaths(t, "Missing", report.Missing)
	wantPaths(t, "Unreferenced", report.Unreferenced)
	wantPaths(t, "Unverifiable", report.Unverifiable)
	if len(report.Verified) != 3 {
		t.Errorf("Verified = %v, want manifest, config and layer", entryPaths(report.Verified))
	}
}

func TestVerify_Reachability_OrphanBlob(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, _ := b.image("a", nil)
	b.tag(m, "v1")
	orphan := b.blob(ocispec.MediaTypeImageLayer, []byte("orphan"))

	report := b.verify()
	wantPaths(t, "Unreferenced", report.Unreferenced, orphan)
	wantPaths(t, "Missing", report.Missing)
	mustContainPath(t, report.Verified, filePathOf(orphan))
	if got := mustContainPath(t, report.Unreferenced, filePathOf(orphan)); got.Reason != nil {
		t.Errorf("Unreferenced reason = %v, want nil", got.Reason)
	}
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %v, want none", entryPaths(report.Failed))
	}
}

func TestVerify_Reachability_MissingLayer(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, layer := b.image("a", nil)
	b.tag(m, "v1")
	b.remove(layer)

	report := b.verify()
	wantPaths(t, "Missing", report.Missing, layer)
	got := mustContainPath(t, report.Missing, filePathOf(layer))
	if !errors.Is(got.Reason, errdef.ErrNotFound) {
		t.Errorf("Missing reason = %v, want %v", got.Reason, errdef.ErrNotFound)
	}
	// a missing blob is reported once, and nothing else is dragged along
	wantPaths(t, "Failed", report.Failed)
	wantPaths(t, "Unreferenced", report.Unreferenced)
	wantPaths(t, "Unverifiable", report.Unverifiable)
}

func TestVerify_Reachability_MissingNestedManifest(t *testing.T) {
	// index.json -> image index -> manifest (absent) -> its blobs
	b := newLayoutBuilder(t)
	child, _, _ := b.image("child", nil)
	data, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{child},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := b.blob(ocispec.MediaTypeImageIndex, data)
	b.tag(root, "v1")
	b.remove(child)

	report := b.verify()
	wantPaths(t, "Missing", report.Missing, child)
	wantPaths(t, "Failed", report.Failed)
	wantPaths(t, "Unverifiable", report.Unverifiable)
}

func TestVerify_Reachability_MissingManifest(t *testing.T) {
	b := newLayoutBuilder(t)
	kept, _, _ := b.image("kept", nil)
	b.tag(kept, "kept")
	lost, lostConfig, lostLayer := b.image("lost", nil)
	b.tag(lost, "lost")
	b.remove(lost)

	report := b.verify()
	wantPaths(t, "Missing", report.Missing, lost)
	wantPaths(t, "Failed", report.Failed)
	wantPaths(t, "Unverifiable", report.Unverifiable)
	// Without the manifest nothing refers to its blobs, and GC would remove
	// them: reachability is "exactly what GC keeps".
	wantPaths(t, "Unreferenced", report.Unreferenced, lostConfig, lostLayer)

}

func TestVerify_Reachability_MissingUntaggedEntry(t *testing.T) {
	b := newLayoutBuilder(t)
	m, config, layer := b.image("a", nil)
	b.untagged(m)
	b.remove(m)

	report := b.verify()
	wantPaths(t, "Missing", report.Missing, m)
	wantPaths(t, "Failed", report.Failed)

	// The missing untagged manifest is not a GC root. Its config and layer
	// are intact but unreachable under the GC reachability rule.
	wantPaths(t, "Unreferenced", report.Unreferenced, config, layer)

	// The missing manifest itself does not make the graph unverifiable:
	// its absence is known, and no present manifest needs to be parsed
	// to establish that its own config and layer are not GC-reachable.
	wantPaths(t, "Unverifiable", report.Unverifiable)
}

func TestVerify_Reachability_AbsentSubjectIsNotMissing(t *testing.T) {
	b := newLayoutBuilder(t)
	subject := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digestOf([]byte("elsewhere")), Size: 9}
	r, _, _ := b.image("referrer", &subject)
	b.tag(r, "referrer")

	report := b.verify()
	wantPaths(t, "Missing", report.Missing)
	wantPaths(t, "Unreferenced", report.Unreferenced)
}

func TestVerify_Reachability_CorruptReferencedBlob(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, layer := b.image("a", nil)
	b.tag(m, "v1")
	b.files[filePathOf(layer)] = &fstest.MapFile{Data: []byte("tampered")}

	report := b.verify()
	wantPaths(t, "Failed", report.Failed, layer)
	if got := mustContainPath(t, report.Failed, filePathOf(layer)); !errors.Is(got.Reason, errdefMismatch()) {
		t.Errorf("Failed reason = %v", got.Reason)
	}
	wantPaths(t, "Unreferenced", report.Unreferenced)
	wantPaths(t, "Missing", report.Missing)
	wantPaths(t, "Unverifiable", report.Unverifiable)
}

func TestVerify_Reachability_UnparseableManifest(t *testing.T) {
	b := newLayoutBuilder(t)
	kept, _, _ := b.image("kept", nil)
	b.tag(kept, "kept")
	broken := b.blob(ocispec.MediaTypeImageManifest, []byte("this is not a manifest"))
	b.tag(broken, "broken")
	orphan := b.blob(ocispec.MediaTypeImageLayer, []byte("orphan"))

	report := b.verify()
	// The orphan is not reported: the broken manifest might be what refers to it.
	wantPaths(t, "Unreferenced", report.Unreferenced)
	wantPaths(t, "Unverifiable", report.Unverifiable, broken)
	if got := mustContainPath(t, report.Unverifiable, filePathOf(broken)); got.Reason == nil {
		t.Error("Unverifiable reason = nil, want why the manifest cannot be followed")
	}
	// the broken manifest itself is intact content
	mustContainPath(t, report.Verified, filePathOf(broken))
	mustContainPath(t, report.Verified, filePathOf(orphan))
	wantPaths(t, "Failed", report.Failed)
}

func TestVerify_Reachability_CorruptManifest(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, _ := b.image("a", nil)
	b.tag(m, "v1")
	orphan := b.blob(ocispec.MediaTypeImageLayer, []byte("orphan"))
	b.files[filePathOf(m)] = &fstest.MapFile{Data: []byte(`{"layers":[]}`)}

	report := b.verify()
	wantPaths(t, "Failed", report.Failed, m)
	wantPaths(t, "Unverifiable", report.Unverifiable, m)
	wantPaths(t, "Unreferenced", report.Unreferenced)
	wantPaths(t, "Missing", report.Missing)
	mustContainPath(t, report.Verified, filePathOf(orphan))
}

func TestVerify_Reachability_UnlistableAlgorithmDir(t *testing.T) {
	b := newLayoutBuilder(t)
	m, _, _ := b.image("a", nil)
	b.tag(m, "v1")
	algPath := "blobs/sha256"

	report, err := Verify(context.Background(), failReadDirFS{FS: b.fsys(), fail: algPath}, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	mustContainPath(t, report.Failed, algPath)
	mustContainPath(t, report.Unverifiable, algPath)
	// what the directory holds is unknown: nothing in it is reported as absent
	// or as unreferenced
	wantPaths(t, "Missing", report.Missing)
	wantPaths(t, "Unreferenced", report.Unreferenced)
}

// TestVerify_Reachability_UntaggedAndReferrers pins the reachability rule to
// GC's: tagged roots, plus untagged referrers that reach one through their
// subject chain. An untagged entry that is not a referrer is not reachable,
// which is where it differs from everything in index.json.
func TestVerify_Reachability_UntaggedAndReferrers(t *testing.T) {
	b := newLayoutBuilder(t)
	root, _, _ := b.image("root", nil)
	b.tag(root, "v1")

	// untagged referrer of the tagged manifest: reachable
	ref, refConfig, refLayer := b.image("referrer", &root)
	b.untagged(ref)
	// untagged referrer of that referrer: reachable through the chain
	nested, nestedConfig, nestedLayer := b.image("nested", &ref)
	b.untagged(nested)

	// untagged manifest without a subject: not reachable
	loose, looseConfig, looseLayer := b.image("loose", nil)
	b.untagged(loose)
	// untagged referrer whose subject chain ends at an untagged manifest
	stray, strayConfig, strayLayer := b.image("stray", &loose)
	b.untagged(stray)

	report := b.verify()
	wantPaths(t, "Unreferenced", report.Unreferenced,
		loose, looseConfig, looseLayer,
		stray, strayConfig, strayLayer)
	for _, d := range []ocispec.Descriptor{ref, refConfig, refLayer, nested, nestedConfig, nestedLayer} {
		for _, e := range report.Unreferenced {
			if e.Path == filePathOf(d) {
				t.Errorf("%s is a referrer of a tagged manifest, but was reported unreferenced", e.Path)
			}
		}
	}
	wantPaths(t, "Missing", report.Missing)
	wantPaths(t, "Failed", report.Failed)
}

// TestVerify_Reachability_MatchesGC checks the contract of the feature, that
// Unreferenced is what GC removes (excluding recent blobs within the GC grace
// period), by running both on the same layout.
func TestVerify_Reachability_MatchesGC(t *testing.T) {
	b := newLayoutBuilder(t)
	root, _, _ := b.image("root", nil)
	b.tag(root, "v1")
	ref, _, _ := b.image("referrer", &root)
	b.untagged(ref)
	loose, _, _ := b.image("loose", nil)
	b.untagged(loose)
	b.blob(ocispec.MediaTypeImageLayer, []byte("orphan"))

	dir := t.TempDir()
	for name, file := range b.fsys() {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, file.Data, 0o666); err != nil {
			t.Fatal(err)
		}
	}

	report, err := Verify(context.Background(), os.DirFS(dir), VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Unreferenced) == 0 {
		t.Fatal("Unreferenced is empty, the test would prove nothing")
	}

	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.AutoGC = false

	before := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ocispec.ImageBlobsDir+"/") {
			before[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal("WalkDir() before GC:", err)
	}

	backdateBlobs(t, dir)
	if err := store.GC(context.Background()); err != nil {
		t.Fatal("GC() error =", err)
	}

	removed := map[string]bool{}
	for p := range before {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
		switch {
		case err == nil:
			// Still present.
		case errors.Is(err, fs.ErrNotExist):
			removed[p] = true
		case err != nil:
			t.Fatalf("Stat(%s): %v", p, err)
		}
	}

	got := map[string]bool{}
	for _, e := range report.Unreferenced {
		got[e.Path] = true
	}

	// Every reported unreferenced blob must be removed by GC.
	for p := range got {
		if !removed[p] {
			t.Errorf("Verify reported %s as unreferenced, but GC kept it", p)
		}
	}

	// Every blob removed by GC in this all-valid fixture must have been
	// reported as unreferenced.
	for p := range removed {
		if !got[p] {
			t.Errorf("GC removed %s, but Verify did not report it as unreferenced", p)
		}
	}
}

func digestOf(data []byte) digest.Digest { return digest.FromBytes(data) }

func errdefMismatch() error { return content.ErrMismatchedDigest }

// TestVerify_Reachability_UntaggedReferrerOfAbsentSubject: GC fails on an
// untagged referrer whose subject chain leaves the layout, and so removes
// nothing. Verify reports why, and no Unreferenced.
func TestVerify_Reachability_UntaggedReferrerOfAbsentSubject(t *testing.T) {
	b := newLayoutBuilder(t)
	root, _, _ := b.image("root", nil)
	b.tag(root, "v1")
	gone := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digestOf([]byte("elsewhere")), Size: 9}
	r, config, layer := b.image("referrer", &gone)
	b.untagged(r)
	orphan := b.blob(ocispec.MediaTypeImageLayer, []byte("orphan"))

	report := b.verify()

	for _, desc := range []ocispec.Descriptor{r, config, layer, orphan} {
		mustContainPath(t, report.Unreferenced, filePathOf(desc))
	}

	if len(report.Unverifiable) != 0 {
		t.Errorf("Unverifiable = %v, want no entries", report.Unverifiable)
	}
}

func TestBlobPathOfRejectsMalformedDigests(t *testing.T) {
	tests := []digest.Digest{
		"",
		"sha256",
		"sha256:",
		"sha256:.",
		"sha256:..",
		"sha256:../escape",
		"sha256:aa/bb",
		"sha256:GG",
		"SHA256:abcd",
	}

	for _, dgst := range tests {
		t.Run(string(dgst), func(t *testing.T) {
			p, _, ok := blobPathOf(dgst)
			if ok {
				t.Errorf("blobPathOf(%q) = %q, want invalid digest",
					dgst, p)
			}
		})
	}

	// Unsupported algorithms remain valid references when their syntax
	// is valid. Support is a separate verification concern.
	p, alg, ok := blobPathOf("unknown:abcd")
	if !ok || alg != "unknown" ||
		p != "blobs/unknown/abcd" {
		t.Errorf("blobPathOf(unknown:abcd) = (%q, %q, %v)",
			p, alg, ok)
	}
}

func TestVerify_Reachability_UnreadableNestedIndex(t *testing.T) {
	b := newLayoutBuilder(t)

	child, _, _ := b.image("child", nil)
	nestedData, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{child},
	})
	if err != nil {
		t.Fatal(err)
	}
	nested := b.blob(ocispec.MediaTypeImageIndex, nestedData)

	rootData, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{nested},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := b.blob(ocispec.MediaTypeImageIndex, rootData)
	b.tag(root, "v1")

	orphan := b.blob(
		ocispec.MediaTypeImageLayer,
		[]byte("orphan"),
	)

	report, err := Verify(
		context.Background(),
		failOpenFS{
			FS:   b.fsys(),
			fail: filePathOf(nested),
		},
		VerifyOptions{},
	)
	if err != nil {
		t.Fatal("Verify() error =", err)
	}

	mustContainPath(t, report.Unverifiable, filePathOf(nested))
	wantPaths(t, "Unreferenced", report.Unreferenced)
	mustContainPath(t, report.Verified, filePathOf(orphan))
}
