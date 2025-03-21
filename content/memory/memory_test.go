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

package memory

import (
	"bytes"
	"context"
	"crypto/sha1"
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/resolver"
	"oras.land/oras-go/v2/internal/spec"
)

func TestStoreInterface(t *testing.T) {
	var store interface{} = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(content.PredecessorFinder); !ok {
		t.Error("&Store{} does not conform content.PredecessorFinder")
	}
}

func TestStoreSuccess(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	internalResolver := s.resolver.(*resolver.Memory)
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
	internalStorage := s.storage.(*cas.Memory)
	if got := len(internalStorage.Map()); got != 1 {
		t.Errorf("storage.Map() = %v, want %v", got, 1)
	}
}

func TestStoreContentNotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreContentAlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStoreContentBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStoreTagNotFound(t *testing.T) {
	ref := "foobar"

	s := New()
	ctx := context.Background()

	_, err := s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreTagUnknownContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Tag(ctx, desc, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreRepeatTag(t *testing.T) {
	generate := func(content []byte) ocispec.Descriptor {
		return ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	// get internal resolver
	internalResolver := s.resolver.(*resolver.Memory)

	// initial tag
	content := []byte("hello world")
	desc := generate(content)
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("foo")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("bar")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}
}

func TestStorePredecessors(t *testing.T) {
	s := New()
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		var manifest spec.Artifact
		manifest.Subject = &subject
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateManifest(descs[0], descs[1:4]...)                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	generateIndex()                                            // Blob 9
	generateIndex(descs[7:10]...)                              // Blob 10
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 11
	generateArtifactManifest(descs[6], descs[11])              // Blob 12
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 13
	generateArtifactManifest(descs[10], descs[13])             // Blob 14

	eg, egCtx := errgroup.WithContext(ctx)
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				err := s.Push(egCtx, descs[i], bytes.NewReader(blobs[i]))
				if err != nil {
					return fmt.Errorf("failed to push test content to src: %d: %v", i, err)
				}
				return nil
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	// verify predecessors
	wants := [][]ocispec.Descriptor{
		descs[4:7],            // Blob 0
		{descs[4], descs[6]},  // Blob 1
		{descs[4], descs[6]},  // Blob 2
		{descs[5], descs[6]},  // Blob 3
		{descs[7]},            // Blob 4
		{descs[7]},            // Blob 5
		{descs[8], descs[12]}, // Blob 6
		{descs[10]},           // Blob 7
		{descs[10]},           // Blob 8
		{descs[10]},           // Blob 9
		{descs[14]},           // Blob 10
		{descs[12]},           // Blob 11
		nil,                   // Blob 12
		{descs[14]},           // Blob 13
		nil,                   // Blob 14
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestStore_BadDigest(t *testing.T) {
	data := []byte("hello world")
	ref := "foobar"

	t.Run("invalid digest", func(t *testing.T) {
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    "invalid-digest",
		}

		s := New()
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, digest.ErrDigestInvalidFormat) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, digest.ErrDigestInvalidFormat)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Exists(ctx, desc); err != nil {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, nil)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})

	t.Run("unsupported digest (sha1)", func(t *testing.T) {
		h := sha1.New()
		h.Write(data)
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		}

		s := New()
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, digest.ErrDigestUnsupported) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, digest.ErrDigestUnsupported)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Exists(ctx, desc); err != nil {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, nil)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})
}

func equalDescriptorSet(actual []ocispec.Descriptor, expected []ocispec.Descriptor) bool {
	if len(actual) != len(expected) {
		return false
	}
	contains := func(node ocispec.Descriptor) bool {
		for _, candidate := range actual {
			if reflect.DeepEqual(candidate, node) {
				return true
			}
		}
		return false
	}
	for _, node := range expected {
		if !contains(node) {
			return false
		}
	}
	return true
}
