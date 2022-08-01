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
	"regexp"
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
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
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
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
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
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice := []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[4]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[4], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[5] should be copied
	copiedIndice = []int{0, 4, 5}
	uncopiedIndice = []int{1, 2, 3, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[14]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[14], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
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

func TestExtendedCopyGraph_WithDepthOption(t *testing.T) {
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
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
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
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice := []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth of 1
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: 1}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[3] and descs[5] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5}
	uncopiedIndice = []int{6, 7, 8, 9, 10, 11, 12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth of 2
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: 2}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice = []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with depth -1
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{Depth: -1}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[11] should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	uncopiedIndice = []int{12, 13, 14}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_WithFindPredecessorsOption(t *testing.T) {
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
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
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
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))     // Blob 4
	generateArtifactManifest(descs[3], descs[4])                 // Blob 5 (root)
	appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))       // Blob 6
	generateArtifactManifest(descs[3], descs[6])                 // Blob 7 (root)
	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2")) // Blob 8
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))     // Blob 9
	generateManifest(descs[8], descs[9])                         // Blob 10
	generateIndex(descs[3], descs[10])                           // Blob 11 (root)

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

	// test extended copy by descs[3] with media type filter
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{
		FindPredecessors: func(ctx context.Context, src content.GraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			predecessors, err := src.Predecessors(ctx, desc)
			if err != nil {
				return nil, err
			}
			var filtered []ocispec.Descriptor
			for _, p := range predecessors {
				// filter media type
				switch p.MediaType {
				case artifactspec.MediaTypeArtifactManifest:
					filtered = append(filtered, p)
				}
			}

			return filtered, nil
		},
	}
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[3], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// graph rooted by descs[5] and decs[7] should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7}
	uncopiedIndice := []int{8, 9, 10, 11}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterAnnotationWithRegex(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte, key string, value string) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:   mediaType,
			Digest:      digest.FromBytes(blob),
			Size:        int64(len(blob)),
			Annotations: map[string]string{key: value},
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, key string, value string) {
		var manifest artifactspec.Manifest
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
		manifest.Annotations = map[string]string{key: value}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON, key, value)
	}
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"), "bar", "blackpink") // descs[0]
	generateArtifactManifest(descs[0], "bar", "bluebrown")                     // descs[1]
	generateArtifactManifest(descs[0], "bar", "blackred")                      // descs[2]
	generateArtifactManifest(descs[0], "bar", "blackviolet")                   // descs[3]
	generateArtifactManifest(descs[0], "bar", "greengrey")                     // descs[4]
	generateArtifactManifest(descs[0], "bar", "brownblack")                    // descs[5]
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
	// test extended copy by descs[0] with annotation filter
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	exp := "black."
	regex := regexp.MustCompile(exp)
	opts.FilterAnnotation("bar", regex)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 2, 3}
	uncopiedIndice := []int{1, 4, 5}
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

func TestExtendedCopyGraph_FilterArtifactTypeWithRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string) {
		var manifest artifactspec.Manifest
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
		manifest.ArtifactType = artifactType
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")   // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")       // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")      // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")       // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")     // descs[5]

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

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp.
	exp := ".bar."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	opts.FilterArtifactType(exp)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 1, 3, 4}
	uncopiedIndice := []int{2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterArtifactTypeWithMultipleRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string) {
		var manifest artifactspec.Manifest
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
		manifest.ArtifactType = artifactType
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")   // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")       // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")      // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")       // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")     // descs[5]

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

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp1 and exp2.
	exp1 := ".foo|bar."
	exp2 := "bad."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	opts.FilterArtifactType(exp1)
	opts.FilterArtifactType(exp2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 3, 4}
	uncopiedIndice := []int{1, 2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterArtifactTypeWithRegexInvalidRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string) {
		var manifest artifactspec.Manifest
		artifactSubject := descriptor.OCIToArtifact(subject)
		manifest.Subject = &artifactSubject
		manifest.ArtifactType = artifactType
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")   // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")       // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")      // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")       // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")     // descs[5]

	ctx := context.Background()

	src := memory.New()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// exp is an invalid regex
	exp := "ab???ba"
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	opts.FilterArtifactType(exp)
	err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts)
	if err == nil {
		t.Fatalf("Invalid regex input uncaught")
	}
}
