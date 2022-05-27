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
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
)

func TestExtendedCopy_FullCopy(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		var manifest artifactspec.Manifest
		manifest.Subject = descriptor.OCIToArtifact(subject)
		for _, blob := range blobs {
			manifest.Blobs = append(manifest.Blobs, descriptor.OCIToArtifact(blob))
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 4
	generateArtifactManifest(descs[3], descs[4])               // Blob 5
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 6
	generateArtifactManifest(descs[5], descs[6])               // Blob 7

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifest := descs[3]
	ref := "foobar"
	err := src.Tag(ctx, manifest, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test extended copy
	gotDesc, err := oras.ExtendedCopy(ctx, src, ref, dst, "", oras.ExtendedCopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, manifest) {
		t.Errorf("Copy() = %v, want %v", gotDesc, manifest)
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
	if !reflect.DeepEqual(gotDesc, manifest) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, manifest)
	}
}

func TestExtendedCopyGraph_FullCopy(t *testing.T) {
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
		var manifest artifactspec.Manifest
		manifest.Subject = descriptor.OCIToArtifact(subject)
		for _, blob := range blobs {
			manifest.Blobs = append(manifest.Blobs, descriptor.OCIToArtifact(blob))
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_1")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))       // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))       // Blob 2
	generateManifest(descs[0], descs[1:3]...)                    // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))       // Blob 4
	generateManifest(descs[0], descs[4])                         // Blob 5 (root)
	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2")) // Blob 6
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))     // Blob 7
	generateManifest(descs[6], descs[7])                         // Blob 8
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))     // Blob 9
	generateArtifactManifest(descs[8], descs[9])                 // Blob 10
	generateIndex(descs[3], descs[10])                           // Blob 11 (root)
	appendBlob(ocispec.MediaTypeImageLayer, []byte("goodbye"))   // Blob 12
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))     // Blob 13
	generateArtifactManifest(descs[12], descs[13])               // Blob 14 (root)

	ctx := context.Background()
	verifyCopy := func(dst content.Fetcher, copiedIndice []int, uncopiedIndice []int) {
		for _, i := range copiedIndice {
			got, err := content.FetchAll(ctx, dst, descs[i])
			if err != nil {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
				continue
			}
			if want := blobs[i]; !bytes.Equal(got, want) {
				t.Errorf("content[%d] = %v, want %v", i, got, want)
			}
		}
		for _, i := range uncopiedIndice {
			if _, err := dst.Fetch(ctx, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src := memory.New()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0]
	dst := memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice := []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[4]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[4], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[5] should be copied
	copiedIndice = []int{0, 4, 5}
	uncopiedIndice = []int{1, 2, 3, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[14]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[14], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[14] should be copied
	copiedIndice = []int{12, 13, 14}
	uncopiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_PartialCopy(t *testing.T) {
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
	generateIndex(descs[3], descs[5])                          // Blob 6 (root)

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy a part of the graph
	root := descs[3]
	if err := oras.CopyGraph(ctx, src, dst, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// blobs [0-3] should be copied
	for i := range blobs[:4] {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Fatalf("content[%d] error = %v, wantErr %v", i, err, false)
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Fatalf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// test extended copy by descs[0]
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}

	// all blobs should be copied
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

func TestExtendedCopyGraph_WithDepth(t *testing.T) {
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
		var manifest artifactspec.Manifest
		manifest.Subject = descriptor.OCIToArtifact(subject)
		for _, blob := range blobs {
			manifest.Blobs = append(manifest.Blobs, descriptor.OCIToArtifact(blob))
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_1")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))       // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))       // Blob 2
	generateManifest(descs[0], descs[1:3]...)                    // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))       // Blob 4
	generateManifest(descs[0], descs[4])                         // Blob 5 (root)
	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2")) // Blob 6
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))     // Blob 7
	generateManifest(descs[6], descs[7])                         // Blob 8
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))     // Blob 9
	generateArtifactManifest(descs[8], descs[9])                 // Blob 10
	generateIndex(descs[3], descs[10])                           // Blob 11 (root)
	appendBlob(ocispec.MediaTypeImageLayer, []byte("goodbye"))   // Blob 12
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))     // Blob 13
	generateArtifactManifest(descs[12], descs[13])               // Blob 14 (root)

	ctx := context.Background()
	verifyCopy := func(dst content.Fetcher, copiedIndice []int, uncopiedIndice []int) {
		for _, i := range copiedIndice {
			got, err := content.FetchAll(ctx, dst, descs[i])
			if err != nil {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
				continue
			}
			if want := blobs[i]; !bytes.Equal(got, want) {
				t.Errorf("content[%d] = %v, want %v", i, got, want)
			}
		}
		for _, i := range uncopiedIndice {
			if _, err := dst.Fetch(ctx, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src := memory.New()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0] with default depth 0
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice := []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth of 1
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: 1}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[3] and descs[5] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5}
	uncopiedIndice = []int{6, 7, 8, 9, 10, 11, 12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth of 2
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: 2}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice = []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth -1
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: -1}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice = []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopy_NotFound(t *testing.T) {
	src := memory.New()
	dst := memory.New()

	ref := "foobar"
	ctx := context.Background()
	_, err := oras.ExtendedCopy(ctx, src, ref, dst, "", oras.ExtendedCopyOptions{})
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("ExtendedCopy() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}
