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

	// test copy
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
	opts.MapRoot = func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
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
		MapRoot: func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
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
		MapRoot: func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
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
		MapRoot: func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error) {
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
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
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
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
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
	expected := fmt.Sprintf("mismatch MediaType %s: expect %s", docker.MediaTypeConfig, ocispec.MediaTypeImageConfig)
	if err.Error() != expected {
		t.Fatalf("Copy() error = %v, wantErr %v", err, expected)
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
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
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
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("Copy() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestCopy_RestoreDuplicates(t *testing.T) {
	src := memory.New()
	temp := t.TempDir()
	dst := file.New(temp)
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

func TestCopy_DiscardDuplicates(t *testing.T) {
	src := memory.New()
	temp := t.TempDir()
	dst := file.New(temp)
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
}
