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

package oras_test

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/spec"
)

// storageTracker tracks storage API counts.
type storageTracker struct {
	content.Storage
	fetch  int64
	push   int64
	exists int64
}

func (t *storageTracker) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	atomic.AddInt64(&t.fetch, 1)
	return t.Storage.Fetch(ctx, target)
}

func (t *storageTracker) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	atomic.AddInt64(&t.push, 1)
	return t.Storage.Push(ctx, expected, content)
}

func (t *storageTracker) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	atomic.AddInt64(&t.exists, 1)
	return t.Storage.Exists(ctx, target)
}

type mockReferencePusher struct {
	oras.Target
	pushReference int64
}

func (p *mockReferencePusher) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	atomic.AddInt64(&p.pushReference, 1)
	if err := p.Target.Push(ctx, expected, content); err != nil {
		return err
	}
	return p.Target.Tag(ctx, expected, reference)
}

func TestCopy_FullCopy(t *testing.T) {
	src := memory.New()
	dst := memory.New()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "foobar"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", oras.CopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
}

func TestCopy_ExistedRoot(t *testing.T) {
	src := memory.New()
	dst := memory.New()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "foobar"
	newTag := "newtag"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	var skippedCount int64
	copyOpts := oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			OnCopySkipped: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&skippedCount, 1)
				return nil
			},
		},
	}

	// copy with src tag
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", copyOpts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}
	// copy with new tag
	gotDesc, err = oras.Copy(ctx, src, ref, dst, newTag, copyOpts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify src tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
	// verify new tag
	gotDesc, err = dst.Resolve(ctx, newTag)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
	// verify invocation of onCopySkipped()
	if got, want := skippedCount, int64(1); got != want {
		t.Errorf("count(OnCopySkipped()) = %v, want %v", got, want)
	}
}

func TestCopyGraph_FullCopy(t *testing.T) {
	src := cas.NewMemory()
	dst := cas.NewMemory()

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

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy graph
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root := descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	contents := dst.Map()
	if got, want := len(contents), len(blobs); got != want {
		t.Errorf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(len(blobs)); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopyGraph_PartialCopy(t *testing.T) {
	src := cas.NewMemory()
	dst := cas.NewMemory()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 4
	generateManifest(descs[0], descs[4])                       // Blob 5
	generateIndex(descs[3], descs[5])                          // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// initial copy
	root := descs[3]
	if err := oras.CopyGraph(ctx, src, dst, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// verify contents
	contents := dst.Map()
	if got, want := len(contents), len(blobs[:4]); got != want {
		t.Fatalf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs[:4] {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Fatalf("content[%d] error = %v, wantErr %v", i, err, false)
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Fatalf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root = descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	contents = dst.Map()
	if got, want := len(contents), len(blobs); got != want {
		t.Errorf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(3); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(3); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(5); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopy_WithOptions(t *testing.T) {
	src := memory.New()

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
	appendManifest := func(arc, os string, mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Platform: &ocispec.Platform{
				Architecture: arc,
				OS:           os,
			},
		})
	}
	generateManifest := func(arc, os string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendManifest(arc, os, ocispec.MediaTypeImageManifest, manifestJSON)
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))           // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))               // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))               // Blob 2
	generateManifest("test-arc-1", "test-os-1", descs[0], descs[1:3]...) // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))             // Blob 4
	generateManifest("test-arc-2", "test-os-2", descs[0], descs[4])      // Blob 5
	generateIndex(descs[3], descs[5])                                    // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[6]
	ref := "foobar"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy with media type filter
	dst := memory.New()
	opts := oras.DefaultCopyOptions
	opts.MapRoot = func(ctx context.Context, src content.ReadOnlyStorage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
		if root.MediaType == ocispec.MediaTypeImageIndex {
			return root, nil
		} else {
			return ocispec.Descriptor{}, errdef.ErrNotFound
		}
	}
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}

	// test copy with platform filter and hooks
	dst = memory.New()
	preCopyCount := int64(0)
	postCopyCount := int64(0)
	opts = oras.CopyOptions{
		MapRoot: func(ctx context.Context, src content.ReadOnlyStorage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
			manifests, err := content.Successors(ctx, src, root)
			if err != nil {
				return ocispec.Descriptor{}, errdef.ErrNotFound
			}

			// platform filter
			for _, m := range manifests {
				if m.Platform.Architecture == "test-arc-2" && m.Platform.OS == "test-os-2" {
					return m, nil
				}
			}
			return ocispec.Descriptor{}, errdef.ErrNotFound
		},
		CopyGraphOptions: oras.CopyGraphOptions{
			PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&preCopyCount, 1)
				return nil
			},
			PostCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&postCopyCount, 1)
				return nil
			},
		},
	}
	wantDesc := descs[5]
	gotDesc, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("Copy() = %v, want %v", gotDesc, wantDesc)
	}

	// verify contents
	for i, desc := range append([]ocispec.Descriptor{descs[0]}, descs[4:6]...) {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// verify API counts
	if got, want := preCopyCount, int64(3); got != want {
		t.Errorf("count(PreCopy()) = %v, want %v", got, want)
	}
	if got, want := postCopyCount, int64(3); got != want {
		t.Errorf("count(PostCopy()) = %v, want %v", got, want)
	}

	// test copy with root filter, but no matching node can be found
	dst = memory.New()
	opts = oras.CopyOptions{
		MapRoot: func(ctx context.Context, src content.ReadOnlyStorage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
			if root.MediaType == "test" {
				return root, nil
			} else {
				return ocispec.Descriptor{}, errdef.ErrNotFound
			}
		},
		CopyGraphOptions: oras.DefaultCopyGraphOptions,
	}

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test copy with MaxMetadataBytes = 1
	dst = memory.New()
	opts = oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			MaxMetadataBytes: 1,
		},
	}
	if _, err := oras.Copy(ctx, src, ref, dst, "", opts); !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}
}

func TestCopy_WithTargetPlatformOptions(t *testing.T) {
	src := memory.New()
	arc_1 := "test-arc-1"
	os_1 := "test-os-1"
	variant_1 := "v1"
	arc_2 := "test-arc-2"
	os_2 := "test-os-2"
	variant_2 := "v2"

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
	appendManifest := func(arc, os, variant string, mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Platform: &ocispec.Platform{
				Architecture: arc,
				OS:           os,
				Variant:      variant,
			},
		})
	}
	generateManifest := func(arc, os, variant string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendManifest(arc, os, variant, ocispec.MediaTypeImageManifest, manifestJSON)
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))            // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))            // Blob 2
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1:3]...) // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello1"))         // Blob 4
	generateManifest(arc_2, os_2, variant_1, descs[0], descs[4])      // Blob 5
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello2"))         // Blob 6
	generateManifest(arc_1, os_1, variant_2, descs[0], descs[6])      // Blob 7
	generateIndex(descs[3], descs[5], descs[7])                       // Blob 8

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[8]
	ref := "foobar"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy with platform filter for the image index
	dst := memory.New()
	opts := oras.CopyOptions{}
	targetPlatform := ocispec.Platform{
		Architecture: arc_2,
		OS:           os_2,
	}
	opts.WithTargetPlatform(&targetPlatform)
	wantDesc := descs[5]
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("Copy() = %v, want %v", gotDesc, wantDesc)
	}

	// verify contents
	for i, desc := range append([]ocispec.Descriptor{descs[0]}, descs[4:6]...) {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// test copy with platform filter for the image index, and multiple
	// manifests match the required platform. Should return the first matching
	// entry.
	dst = memory.New()
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts = oras.CopyOptions{}
	opts.WithTargetPlatform(&targetPlatform)
	wantDesc = descs[3]
	gotDesc, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("Copy() = %v, want %v", gotDesc, wantDesc)
	}

	// verify contents
	for i, desc := range append([]ocispec.Descriptor{descs[0]}, descs[1:3]...) {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// test copy with platform filter and existing MapRoot func for the image
	// index, but there is no matching node. Should return not found error.
	dst = memory.New()
	opts = oras.CopyOptions{
		MapRoot: func(ctx context.Context, src content.ReadOnlyStorage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
			if root.MediaType == ocispec.MediaTypeImageIndex {
				return root, nil
			} else {
				return ocispec.Descriptor{}, errdef.ErrNotFound
			}
		},
	}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_2,
	}
	opts.WithTargetPlatform(&targetPlatform)

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if wantErr := errdef.ErrNotFound; !errors.Is(err, wantErr) {
		t.Errorf("Copy() error = %v, wantErr %v", err, wantErr)
	}

	// test copy with platform filter for the manifest
	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts.WithTargetPlatform(&targetPlatform)

	root = descs[7]
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	wantDesc = descs[7]
	gotDesc, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("Copy() = %v, want %v", gotDesc, wantDesc)
	}

	// verify contents
	for i, desc := range append([]ocispec.Descriptor{descs[0]}, descs[6]) {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// test copy with platform filter for the manifest, but there is no matching
	// node. Should return not found error.
	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
		Variant:      variant_2,
	}
	opts.WithTargetPlatform(&targetPlatform)

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if wantErr := errdef.ErrNotFound; !errors.Is(err, wantErr) {
		t.Errorf("Copy() error = %v, wantErr %v", err, wantErr)
	}

	// test copy with platform filter, but the node's media type is not
	// supported. Should return unsupported error
	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts.WithTargetPlatform(&targetPlatform)

	root = descs[1]
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}

	// generate incorrect test content
	blobs = nil
	descs = nil
	appendBlob(docker.MediaTypeConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author 1",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo1"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts.WithTargetPlatform(&targetPlatform)

	// test copy with platform filter for the manifest, but the manifest is
	// invalid by having docker mediaType config in the manifest and oci
	// mediaType in the image config. Should return error.
	root = descs[2]
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if wantErr := errdef.ErrUnsupported; !errors.Is(err, wantErr) {
		t.Errorf("Copy() error = %v, wantErr %v", err, wantErr)
	}

	// generate test content with null config blob
	blobs = nil
	descs = nil
	appendBlob(ocispec.MediaTypeImageConfig, []byte("null"))     // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo2"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts.WithTargetPlatform(&targetPlatform)

	// test copy with platform filter for the manifest with null config blob
	// should return not found error
	root = descs[2]
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if wantErr := errdef.ErrNotFound; !errors.Is(err, wantErr) {
		t.Errorf("Copy() error = %v, wantErr %v", err, wantErr)
	}

	// generate test content with empty config blob
	blobs = nil
	descs = nil
	appendBlob(ocispec.MediaTypeImageConfig, []byte(""))         // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo3"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	dst = memory.New()
	opts = oras.CopyOptions{}
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	opts.WithTargetPlatform(&targetPlatform)

	// test copy with platform filter for the manifest with empty config blob
	// should return not found error
	root = descs[2]
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if wantErr := errdef.ErrNotFound; !errors.Is(err, wantErr) {
		t.Errorf("Copy() error = %v, wantErr %v", err, wantErr)
	}

	// test copy with no platform filter and nil opts.MapRoot
	// opts.MapRoot should be nil
	opts = oras.CopyOptions{}
	opts.WithTargetPlatform(nil)
	if opts.MapRoot != nil {
		t.Fatal("opts.MapRoot not equal to nil when platform is not provided")
	}

	// test copy with no platform filter and custom opts.MapRoot
	// should return ErrNotFound
	opts = oras.CopyOptions{
		MapRoot: func(ctx context.Context, src content.ReadOnlyStorage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
			if root.MediaType == "test" {
				return root, nil
			} else {
				return ocispec.Descriptor{}, errdef.ErrNotFound
			}
		},
		CopyGraphOptions: oras.DefaultCopyGraphOptions,
	}
	opts.WithTargetPlatform(nil)

	_, err = oras.Copy(ctx, src, ref, dst, "", opts)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestCopy_RestoreDuplicates(t *testing.T) {
	src := memory.New()
	temp := t.TempDir()
	dst, err := file.New(temp)
	if err != nil {
		t.Fatal("file.New() error =", err)
	}
	defer dst.Close()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte, title string) {
		blobs = append(blobs, blob)
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		if title != "" {
			desc.Annotations = map[string]string{
				ocispec.AnnotationTitle: title,
			}
		}
		descs = append(descs, desc)
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
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON, "")
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("{}"), "") // Blob 0
	// 2 blobs with same content
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"), "foo.txt") // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"), "bar.txt") // Blob 2
	generateManifest(descs[0], descs[1:3]...)                           // Blob 3

	ctx := context.Background()
	for i := range blobs {
		exists, err := src.Exists(ctx, descs[i])
		if err != nil {
			t.Fatalf("failed to check existence in src: %d: %v", i, err)
		}
		if exists {
			continue
		}
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "latest"
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", oras.CopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
}

func TestCopy_DiscardDuplicates(t *testing.T) {
	src := memory.New()
	temp := t.TempDir()
	dst, err := file.New(temp)
	if err != nil {
		t.Fatal("file.New() error =", err)
	}
	dst.ForceCAS = true
	defer dst.Close()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte, title string) {
		blobs = append(blobs, blob)
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		if title != "" {
			desc.Annotations = map[string]string{
				ocispec.AnnotationTitle: title,
			}
		}
		descs = append(descs, desc)
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
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON, "")
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("{}"), "") // Blob 0
	// 2 blobs with same content
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"), "foo.txt") // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"), "bar.txt") // Blob 2
	generateManifest(descs[0], descs[1:3]...)                           // Blob 3

	ctx := context.Background()
	for i := range blobs {
		exists, err := src.Exists(ctx, descs[i])
		if err != nil {
			t.Fatalf("failed to check existence in src: %d: %v", i, err)
		}
		if exists {
			continue
		}
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "latest"
	err = src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", oras.CopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify only one of foo.txt and bar.txt exists
	fooExists, err := dst.Exists(ctx, descs[1])
	if err != nil {
		t.Fatalf("dst.Exists(foo) error = %v", err)
	}
	barExists, err := dst.Exists(ctx, descs[2])
	if err != nil {
		t.Fatalf("dst.Exists(bar) error = %v", err)
	}
	if fooExists == barExists {
		t.Error("Only one of foo.txt and bar.txt should exist")
	}
}

func TestCopyGraph_WithOptions(t *testing.T) {
	src := cas.NewMemory()
	dst := cas.NewMemory()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 4
	generateManifest(descs[0], descs[4])                       // Blob 5
	generateIndex(descs[3], descs[5])                          // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// initial copy
	root := descs[3]
	opts := oras.DefaultCopyGraphOptions
	opts.FindSuccessors = func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		successors, err := content.Successors(ctx, fetcher, desc)
		if err != nil {
			return nil, err
		}
		// filter media type
		var filtered []ocispec.Descriptor
		for _, s := range successors {
			if s.MediaType != ocispec.MediaTypeImageConfig {
				filtered = append(filtered, s)
			}
		}
		return filtered, nil
	}
	if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// verify contents
	contents := dst.Map()
	if got, want := len(contents), len(blobs[1:4]); got != want {
		t.Fatalf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := 1; i < 4; i++ {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Fatalf("content[%d] error = %v, wantErr %v", i, err, false)
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Fatalf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// test successor descriptors not obtained from src
	root = descs[3]
	opts = oras.DefaultCopyGraphOptions
	opts.FindSuccessors = func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if content.Equal(desc, root) {
			return descs[1:3], nil
		}
		return content.Successors(ctx, fetcher, desc)
	}
	if err := oras.CopyGraph(ctx, src, cas.NewMemory(), root, opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// test partial copy
	var preCopyCount int64
	var postCopyCount int64
	var skippedCount int64
	opts = oras.CopyGraphOptions{
		PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
			atomic.AddInt64(&preCopyCount, 1)
			return nil
		},
		PostCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
			atomic.AddInt64(&postCopyCount, 1)
			return nil
		},
		OnCopySkipped: func(ctx context.Context, desc ocispec.Descriptor) error {
			atomic.AddInt64(&skippedCount, 1)
			return nil
		},
	}
	root = descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	contents = dst.Map()
	if got, want := len(contents), len(blobs); got != want {
		t.Errorf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := preCopyCount, int64(4); got != want {
		t.Errorf("count(PreCopy()) = %v, want %v", got, want)
	}
	if got, want := postCopyCount, int64(4); got != want {
		t.Errorf("count(PostCopy()) = %v, want %v", got, want)
	}
	if got, want := skippedCount, int64(1); got != want {
		t.Errorf("count(OnCopySkipped()) = %v, want %v", got, want)
	}

	// test CopyGraph with MaxMetadataBytes = 1
	root = descs[6]
	dst = cas.NewMemory()
	opts = oras.CopyGraphOptions{
		MaxMetadataBytes: 1,
	}
	if err := oras.CopyGraph(ctx, src, dst, root, opts); !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}

	t.Run("SkipNode", func(t *testing.T) {
		// test CopyGraph with PreCopy = 1
		root = descs[6]
		dst := &countingStorage{storage: cas.NewMemory()}
		opts = oras.CopyGraphOptions{
			PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				if descs[1].Digest == desc.Digest {
					// blob 1 is handled by us (really this would be a Mount but )
					rc, err := src.Fetch(ctx, desc)
					if err != nil {
						t.Fatalf("Failed to fetch: %v", err)
					}
					defer rc.Close()
					err = dst.storage.Push(ctx, desc, rc) // bypass the counters
					if err != nil {
						t.Fatalf("Failed to fetch: %v", err)
					}
					return oras.SkipNode
				}
				return nil
			},
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := dst.numExists.Load(), int64(7); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		// 7 (exists) - 1 (skipped) = 6 pushes expected
		if got, expected := dst.numPush.Load(), int64(6); got != expected {
			// If we get >=7 then SkipNode did not short circuit the push like it is supposed to do.
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom mounted", func(t *testing.T) {
		root = descs[6]
		dst := &countingStorage{storage: cas.NewMemory()}
		var numMount atomic.Int64
		dst.mount = func(ctx context.Context,
			desc ocispec.Descriptor,
			fromRepo string,
			getContent func() (io.ReadCloser, error),
		) error {
			numMount.Add(1)
			if expected := "source"; fromRepo != expected {
				t.Fatalf("fromRepo = %v, want %v", fromRepo, expected)
			}
			rc, err := src.Fetch(ctx, desc)
			if err != nil {
				t.Fatalf("Failed to fetch content: %v", err)
			}
			defer rc.Close()
			err = dst.storage.Push(ctx, desc, rc) // bypass the counters
			if err != nil {
				t.Fatalf("Failed to push content: %v", err)
			}
			return nil
		}
		opts = oras.CopyGraphOptions{}
		var numPreCopy, numPostCopy, numOnMounted, numMountFrom atomic.Int64
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPreCopy.Add(1)
			return nil
		}
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPostCopy.Add(1)
			return nil
		}
		opts.OnMounted = func(ctx context.Context, d ocispec.Descriptor) error {
			numOnMounted.Add(1)
			return nil
		}
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return []string{"source"}, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := dst.numExists.Load(), int64(7); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		// 7 (exists) - 1 (skipped) = 6 pushes expected
		if got, expected := dst.numPush.Load(), int64(3); got != expected {
			// If we get >=7 then ErrSkipDesc did not short circuit the push like it is supposed to do.
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMount.Load(), int64(4); got != expected {
			t.Errorf("count(Mount()) = %d, want %d", got, expected)
		}
		if got, expected := numOnMounted.Load(), int64(4); got != expected {
			t.Errorf("count(OnMounted()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(4); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
		if got, expected := numPreCopy.Load(), int64(3); got != expected {
			t.Errorf("count(PreCopy()) = %d, want %d", got, expected)
		}
		if got, expected := numPostCopy.Load(), int64(3); got != expected {
			t.Errorf("count(PostCopy()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom copied", func(t *testing.T) {
		root = descs[6]
		dst := &countingStorage{storage: cas.NewMemory()}
		var numMount atomic.Int64
		dst.mount = func(ctx context.Context,
			desc ocispec.Descriptor,
			fromRepo string,
			getContent func() (io.ReadCloser, error),
		) error {
			numMount.Add(1)
			if expected := "source"; fromRepo != expected {
				t.Fatalf("fromRepo = %v, want %v", fromRepo, expected)
			}

			rc, err := getContent()
			if err != nil {
				t.Fatalf("Failed to fetch content: %v", err)
			}
			defer rc.Close()
			err = dst.storage.Push(ctx, desc, rc) // bypass the counters
			if err != nil {
				t.Fatalf("Failed to push content: %v", err)
			}
			return nil
		}
		opts = oras.CopyGraphOptions{}
		var numPreCopy, numPostCopy, numOnMounted, numMountFrom atomic.Int64
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPreCopy.Add(1)
			return nil
		}
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPostCopy.Add(1)
			return nil
		}
		opts.OnMounted = func(ctx context.Context, d ocispec.Descriptor) error {
			numOnMounted.Add(1)
			return nil
		}
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return []string{"source"}, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := dst.numExists.Load(), int64(7); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		// 7 (exists) - 1 (skipped) = 6 pushes expected
		if got, expected := dst.numPush.Load(), int64(3); got != expected {
			// If we get >=7 then ErrSkipDesc did not short circuit the push like it is supposed to do.
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMount.Load(), int64(4); got != expected {
			t.Errorf("count(Mount()) = %d, want %d", got, expected)
		}
		if got, expected := numOnMounted.Load(), int64(0); got != expected {
			t.Errorf("count(OnMounted()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(4); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
		if got, expected := numPreCopy.Load(), int64(7); got != expected {
			t.Errorf("count(PreCopy()) = %d, want %d", got, expected)
		}
		if got, expected := numPostCopy.Load(), int64(7); got != expected {
			t.Errorf("count(PostCopy()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom mounted second try", func(t *testing.T) {
		root = descs[6]
		dst := &countingStorage{storage: cas.NewMemory()}
		var numMount atomic.Int64
		dst.mount = func(ctx context.Context,
			desc ocispec.Descriptor,
			fromRepo string,
			getContent func() (io.ReadCloser, error),
		) error {
			numMount.Add(1)
			switch fromRepo {
			case "source":
				rc, err := src.Fetch(ctx, desc)
				if err != nil {
					t.Fatalf("Failed to fetch content: %v", err)
				}
				defer rc.Close()
				err = dst.storage.Push(ctx, desc, rc) // bypass the counters
				if err != nil {
					t.Fatalf("Failed to push content: %v", err)
				}
				return nil
			case "missing/the/data":
				// simulate a registry mount will fail, so it will request the content to start the copy.
				rc, err := getContent()
				if err != nil {
					return fmt.Errorf("getContent failed: %w", err)
				}
				defer rc.Close()
				err = dst.storage.Push(ctx, desc, rc) // bypass the counters
				if err != nil {
					t.Fatalf("Failed to push content: %v", err)
				}
				return nil
			default:
				t.Fatalf("fromRepo = %v, want either %v or %v", fromRepo, "missing/the/data", "source")
				return nil
			}
		}
		opts = oras.CopyGraphOptions{}
		var numPreCopy, numPostCopy, numOnMounted, numMountFrom atomic.Int64
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPreCopy.Add(1)
			return nil
		}
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPostCopy.Add(1)
			return nil
		}
		opts.OnMounted = func(ctx context.Context, d ocispec.Descriptor) error {
			numOnMounted.Add(1)
			return nil
		}
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return []string{"missing/the/data", "source"}, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := dst.numExists.Load(), int64(7); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		// 7 (exists) - 1 (skipped) = 6 pushes expected
		if got, expected := dst.numPush.Load(), int64(3); got != expected {
			// If we get >=7 then ErrSkipDesc did not short circuit the push like it is supposed to do.
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMount.Load(), int64(4*2); got != expected {
			t.Errorf("count(Mount()) = %d, want %d", got, expected)
		}
		if got, expected := numOnMounted.Load(), int64(4); got != expected {
			t.Errorf("count(OnMounted()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(4); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
		if got, expected := numPreCopy.Load(), int64(3); got != expected {
			t.Errorf("count(PreCopy()) = %d, want %d", got, expected)
		}
		if got, expected := numPostCopy.Load(), int64(3); got != expected {
			t.Errorf("count(PostCopy()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom copied dst not a Mounter", func(t *testing.T) {
		root = descs[6]
		dst := cas.NewMemory()
		opts = oras.CopyGraphOptions{}
		var numPreCopy, numPostCopy, numOnMounted, numMountFrom atomic.Int64
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPreCopy.Add(1)
			return nil
		}
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPostCopy.Add(1)
			return nil
		}
		opts.OnMounted = func(ctx context.Context, d ocispec.Descriptor) error {
			numOnMounted.Add(1)
			return nil
		}
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return []string{"source"}, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := numOnMounted.Load(), int64(0); got != expected {
			t.Errorf("count(OnMounted()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(0); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
		if got, expected := numPreCopy.Load(), int64(7); got != expected {
			t.Errorf("count(PreCopy()) = %d, want %d", got, expected)
		}
		if got, expected := numPostCopy.Load(), int64(7); got != expected {
			t.Errorf("count(PostCopy()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom empty sourceRepositories", func(t *testing.T) {
		root = descs[6]
		dst := &countingStorage{storage: cas.NewMemory()}
		opts = oras.CopyGraphOptions{}
		var numMountFrom atomic.Int64
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return nil, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph() error = %v", err)
		}

		if got, expected := dst.numExists.Load(), int64(7); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numPush.Load(), int64(7); got != expected {
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(4); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom error", func(t *testing.T) {
		root = descs[3]
		dst := &countingStorage{storage: cas.NewMemory()}
		opts = oras.CopyGraphOptions{
			// to make the run result deterministic, we limit concurrency to 1
			Concurrency: 1,
		}
		var numMountFrom atomic.Int64
		e := errors.New("mountFrom error")
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return nil, e
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); !errors.Is(err, e) {
			t.Fatalf("CopyGraph() error = %v, wantErr %v", err, e)
		}

		if got, expected := dst.numExists.Load(), int64(2); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numPush.Load(), int64(0); got != expected {
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(1); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
	})

	t.Run("MountFrom OnMounted error", func(t *testing.T) {
		root = descs[3]
		dst := &countingStorage{storage: cas.NewMemory()}
		var numMount atomic.Int64
		dst.mount = func(ctx context.Context,
			desc ocispec.Descriptor,
			fromRepo string,
			getContent func() (io.ReadCloser, error),
		) error {
			numMount.Add(1)
			if expected := "source"; fromRepo != expected {
				t.Fatalf("fromRepo = %v, want %v", fromRepo, expected)
			}
			rc, err := src.Fetch(ctx, desc)
			if err != nil {
				t.Fatalf("Failed to fetch content: %v", err)
			}
			defer rc.Close()
			err = dst.storage.Push(ctx, desc, rc) // bypass the counters
			if err != nil {
				t.Fatalf("Failed to push content: %v", err)
			}
			return nil
		}
		opts = oras.CopyGraphOptions{
			// to make the run result deterministic, we limit concurrency to 1
			Concurrency: 1,
		}
		var numPreCopy, numPostCopy, numOnMounted, numMountFrom atomic.Int64
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPreCopy.Add(1)
			return nil
		}
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			numPostCopy.Add(1)
			return nil
		}
		e := errors.New("onMounted error")
		opts.OnMounted = func(ctx context.Context, d ocispec.Descriptor) error {
			numOnMounted.Add(1)
			return e
		}
		opts.MountFrom = func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
			numMountFrom.Add(1)
			return []string{"source"}, nil
		}
		if err := oras.CopyGraph(ctx, src, dst, root, opts); !errors.Is(err, e) {
			t.Fatalf("CopyGraph() error = %v, wantErr %v", err, e)
		}

		if got, expected := dst.numExists.Load(), int64(2); got != expected {
			t.Errorf("count(Exists()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numFetch.Load(), int64(0); got != expected {
			t.Errorf("count(Fetch()) = %d, want %d", got, expected)
		}
		if got, expected := dst.numPush.Load(), int64(0); got != expected {
			t.Errorf("count(Push()) = %d, want %d", got, expected)
		}
		if got, expected := numMount.Load(), int64(1); got != expected {
			t.Errorf("count(Mount()) = %d, want %d", got, expected)
		}
		if got, expected := numOnMounted.Load(), int64(1); got != expected {
			t.Errorf("count(OnMounted()) = %d, want %d", got, expected)
		}
		if got, expected := numMountFrom.Load(), int64(1); got != expected {
			t.Errorf("count(MountFrom()) = %d, want %d", got, expected)
		}
		if got, expected := numPreCopy.Load(), int64(0); got != expected {
			t.Errorf("count(PreCopy()) = %d, want %d", got, expected)
		}
		if got, expected := numPostCopy.Load(), int64(0); got != expected {
			t.Errorf("count(PostCopy()) = %d, want %d", got, expected)
		}
	})
}

// countingStorage counts the calls to its content.Storage methods
type countingStorage struct {
	storage content.Storage
	mount   mountFunc

	numExists, numFetch, numPush atomic.Int64
}

func (cs *countingStorage) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	cs.numExists.Add(1)
	return cs.storage.Exists(ctx, target)
}

func (cs *countingStorage) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	cs.numFetch.Add(1)
	return cs.storage.Fetch(ctx, target)
}

func (cs *countingStorage) Push(ctx context.Context, target ocispec.Descriptor, r io.Reader) error {
	cs.numPush.Add(1)
	return cs.storage.Push(ctx, target, r)
}

type mountFunc func(context.Context, ocispec.Descriptor, string, func() (io.ReadCloser, error)) error

func (cs *countingStorage) Mount(ctx context.Context,
	desc ocispec.Descriptor,
	fromRepo string,
	getContent func() (io.ReadCloser, error),
) error {
	return cs.mount(ctx, desc, fromRepo, getContent)
}

func TestCopyGraph_WithConcurrencyLimit(t *testing.T) {
	src := cas.NewMemory()
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
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    config,
			Layers:    layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}
	generateArtifact := func(subject *ocispec.Descriptor, artifactType string, blobs ...ocispec.Descriptor) {
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			Subject:      subject,
			Blobs:        blobs,
			ArtifactType: artifactType,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(index.MediaType, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	generateArtifact(&descs[3], "artifact.1")                  // Blob 4
	generateArtifact(&descs[3], "artifact.2")                  // Blob 5
	generateArtifact(&descs[3], "artifact.3")                  // Blob 6
	generateArtifact(&descs[3], "artifact.4")                  // Blob 7
	generateIndex(descs[3:8]...)                               // Blob 8

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test different concurrency limit
	root := descs[len(descs)-1]
	directSuccessorsNum := 5
	opts := oras.DefaultCopyGraphOptions
	for i := 1; i <= directSuccessorsNum; i++ {
		dst := cas.NewMemory()
		opts.Concurrency = i
		if err := oras.CopyGraph(ctx, src, dst, root, opts); err != nil {
			t.Fatalf("CopyGraph(concurrency: %d) error = %v, wantErr %v", i, err, false)
		}

		// verify contents
		contents := dst.Map()
		if got, want := len(contents), len(blobs); got != want {
			t.Errorf("len(dst) = %v, wantErr %v", got, want)
		}
		for i := range blobs {
			got, err := content.FetchAll(ctx, dst, descs[i])
			if err != nil {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
				continue
			}
			if want := blobs[i]; !bytes.Equal(got, want) {
				t.Errorf("content[%d] = %v, want %v", i, got, want)
			}
		}
	}
}

func TestCopyGraph_ForeignLayers(t *testing.T) {
	src := cas.NewMemory()
	dst := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		if mediaType == ocispec.MediaTypeImageLayerNonDistributable {
			desc.URLs = append(desc.URLs, "http://127.0.0.1/dummy")
			blob = nil
		}
		descs = append(descs, desc)
		blobs = append(blobs, blob)
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))               // Blob 0
	appendBlob(ocispec.MediaTypeImageLayerNonDistributable, []byte("hello")) // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))                   // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))                   // Blob 3
	generateManifest(descs[0], descs[1:4]...)                                // Blob 4

	ctx := context.Background()
	for i := range blobs {
		if blobs[i] == nil {
			continue
		}
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root := descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	contents := dst.Map()
	if got, want := len(contents), len(blobs)-1; got != want {
		t.Errorf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs {
		if blobs[i] == nil {
			continue
		}
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(len(blobs)-1); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(len(blobs)-1); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(len(blobs)-1); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopyGraph_ForeignLayers_Mixed(t *testing.T) {
	src := cas.NewMemory()
	dst := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		if mediaType == ocispec.MediaTypeImageLayerNonDistributable {
			desc.URLs = append(desc.URLs, "http://127.0.0.1/dummy")
			blob = nil
		}
		descs = append(descs, desc)
		blobs = append(blobs, blob)
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))               // Blob 0
	appendBlob(ocispec.MediaTypeImageLayerNonDistributable, []byte("hello")) // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))                 // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))                   // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))                   // Blob 4
	generateManifest(descs[0], descs[1:5]...)                                // Blob 5

	ctx := context.Background()
	for i := range blobs {
		if blobs[i] == nil {
			continue
		}
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root := descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{
		Concurrency: 1,
	}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	contents := dst.Map()
	if got, want := len(contents), len(blobs)-1; got != want {
		t.Errorf("len(dst) = %v, wantErr %v", got, want)
	}
	for i := range blobs {
		if blobs[i] == nil {
			continue
		}
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(len(blobs)-1); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(len(blobs)-1); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(len(blobs)-1); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopy_ReferencePusher(t *testing.T) {
	ctx := context.Background()
	src := memory.New()
	dst := &mockReferencePusher{Target: memory.New()}

	// generate test content
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[len(descs)-1]
	tag := "latest"
	if err := src.Tag(ctx, root, tag); err != nil {
		t.Fatalf("failed to tag manifest: %v", err)
	}

	// test copying to a reference pusher
	var preCopyCount int64
	var postCopyCount int64
	opts := oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&preCopyCount, 1)
				return nil
			},
			PostCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&postCopyCount, 1)
				return nil
			},
		},
	}
	gotManifestDesc, err := oras.Copy(ctx, src, tag, dst, tag, opts)
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotManifestDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotManifestDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err := dst.Resolve(ctx, tag)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}

	// verify API counts
	if got, want := preCopyCount, int64(4); got != want {
		t.Errorf("count(PreCopy()) = %v, want %v", got, want)
	}
	if got, want := postCopyCount, int64(4); got != want {
		t.Errorf("count(PostCopy()) = %v, want %v", got, want)
	}
}

func TestCopy_CopyError(t *testing.T) {
	t.Run("src target is nil", func(t *testing.T) {
		ctx := context.Background()
		dst := memory.New()
		_, err := oras.Copy(ctx, nil, "", dst, "", oras.DefaultCopyOptions)
		if err == nil {
			t.Fatalf("Copy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Copy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Fatalf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("dst target is nil", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		_, err := oras.Copy(ctx, src, "", nil, "", oras.DefaultCopyOptions)
		if err == nil {
			t.Errorf("Copy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Copy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Fatalf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("failed to resolve reference", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := memory.New()
		_, err := oras.Copy(ctx, src, "whatever", dst, "", oras.DefaultCopyOptions)
		if err == nil {
			t.Errorf("Copy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Copy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Fatalf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("tag error", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := &badTagger{
			Target: memory.New(),
		}
		srcRef := "test"

		// prepare test content
		manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/test", oras.PackManifestOptions{})
		if err != nil {
			t.Fatalf("failed to pack test content: %v", err)
		}
		if err := src.Tag(ctx, manifestDesc, srcRef); err != nil {
			t.Fatalf("failed to tag test content on src: %v", err)
		}

		// test copy
		_, err = oras.Copy(ctx, src, srcRef, dst, "", oras.DefaultCopyOptions)
		if err == nil {
			t.Errorf("Copy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Copy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errTag; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyError error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

}

func TestCopyGraph_CopyError(t *testing.T) {
	t.Run("src target is nil", func(t *testing.T) {
		ctx := context.Background()
		dst := memory.New()
		err := oras.CopyGraph(ctx, nil, dst, ocispec.Descriptor{}, oras.DefaultCopyGraphOptions)
		if err == nil {
			t.Fatalf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Fatalf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("dst target is nil", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		err := oras.CopyGraph(ctx, src, nil, ocispec.Descriptor{}, oras.DefaultCopyGraphOptions)
		if err == nil {
			t.Fatalf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Fatalf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("exists error", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := &badExister{
			memory.New(),
		}
		err := oras.CopyGraph(ctx, src, dst, ocispec.Descriptor{}, oras.DefaultCopyGraphOptions)
		if err == nil {
			t.Errorf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errExists; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyGraph() error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

	t.Run("fetch error", func(t *testing.T) {
		ctx := context.Background()
		src := &badFetcher{
			memory.New(),
		}
		dst := memory.New()
		err := oras.CopyGraph(ctx, src, dst, ocispec.Descriptor{}, oras.DefaultCopyGraphOptions)
		if err == nil {
			t.Errorf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errFetch; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyGraph() error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

	t.Run("push error", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := &badPusher{
			memory.New(),
		}

		// prepare test content
		manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/test", oras.PackManifestOptions{})
		if err != nil {
			t.Fatalf("failed to pack test content: %v", err)
		}

		err = oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions)
		if err == nil {
			t.Errorf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errPush; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyGraph() error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

	t.Run("mount fetch error", func(t *testing.T) {
		ctx := context.Background()
		src := &badFetcher{
			memory.New(),
		}
		dst := &testMounter{
			memory.New(),
		}

		opts := oras.CopyGraphOptions{
			MountFrom: func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
				return []string{"source"}, nil
			},
		}
		err := oras.CopyGraph(ctx, src, dst, ocispec.Descriptor{}, opts)
		if err == nil {
			t.Errorf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errFetch; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyGraph() error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

	t.Run("mount error", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := &badMounter{
			memory.New(),
		}

		// prepare test content
		manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/test", oras.PackManifestOptions{})
		if err != nil {
			t.Fatalf("failed to pack test content: %v", err)
		}

		// test copy graph
		opts := oras.CopyGraphOptions{
			MountFrom: func(ctx context.Context, desc ocispec.Descriptor) ([]string, error) {
				return []string{"source"}, nil
			},
		}
		err = oras.CopyGraph(ctx, src, dst, manifestDesc, opts)
		if err == nil {
			t.Errorf("CopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("CopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errMount; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyGraph() error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})
}

var (
	errExists = errors.New("exists error")
	errPush   = errors.New("push error")
	errFetch  = errors.New("fetch error")
	errTag    = errors.New("tag error")
	errMount  = errors.New("mount error")
)

type badExister struct {
	content.Storage
}

func (be *badExister) Exists(_ context.Context, _ ocispec.Descriptor) (bool, error) {
	return false, errExists
}

type badFetcher struct {
	content.Storage
}

func (bf *badFetcher) Fetch(_ context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, errFetch
}

type badPusher struct {
	content.Storage
}

func (bp *badPusher) Push(_ context.Context, _ ocispec.Descriptor, _ io.Reader) error {
	return errPush
}

type badTagger struct {
	oras.Target
}

func (bt *badTagger) Tag(_ context.Context, _ ocispec.Descriptor, _ string) error {
	return errTag
}

type testMounter struct {
	oras.Target
}

func (tm *testMounter) Mount(_ context.Context, _ ocispec.Descriptor, _ string, getContent func() (io.ReadCloser, error)) error {
	_, err := getContent()
	if err != nil {
		return err
	}
	return nil
}

type badMounter struct {
	oras.Target
}

func (bm *badMounter) Mount(_ context.Context, _ ocispec.Descriptor, _ string, _ func() (io.ReadCloser, error)) error {
	return errMount
}
