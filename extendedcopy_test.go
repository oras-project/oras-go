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
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/spec"
	"oras.land/oras-go/v2/registry/remote"
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
	generateManifest := func(subject *ocispec.Descriptor, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config:  config,
			Layers:  layers,
			Subject: subject,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(nil, descs[0], descs[1:3]...)             // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 4
	generateArtifactManifest(descs[3], descs[4])               // Blob 5
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 6
	generateArtifactManifest(descs[5], descs[6])               // Blob 7
	appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))     // Blob 8
	generateManifest(&descs[3], descs[0], descs[8])            // Blob 9

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
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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

func TestExtendedCopyGraph_artifactIndex(t *testing.T) {
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
	generateManifest := func(subject *ocispec.Descriptor, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Subject: subject,
			Config:  config,
			Layers:  layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(subject *ocispec.Descriptor, manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Subject:   subject,
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_1"))            // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("layer_1"))              // Blob 1
	generateManifest(nil, descs[0], descs[1])                               // Blob 2
	appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2"))            // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("layer_2"))              // Blob 4
	generateManifest(nil, descs[3], descs[4])                               // Blob 5
	appendBlob(ocispec.MediaTypeImageLayer, []byte("{}"))                   // Blob 6
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sbom_1"))               // Blob 7
	generateManifest(&descs[2], descs[6], descs[7])                         // Blob 8
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sbom_2"))               // Blob 9
	generateManifest(&descs[5], descs[6], descs[9])                         // Blob 10
	generateIndex(nil, []ocispec.Descriptor{descs[2], descs[5]}...)         // Blob 11 (root)
	generateIndex(&descs[11], []ocispec.Descriptor{descs[8], descs[10]}...) // Blob 12 (root)

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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
	// all blobs should be copied
	copiedIndice := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	uncopiedIndice := []int{}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[2]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[2], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// all blobs should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	uncopiedIndice = []int{}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[8]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[8], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// all blobs should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	uncopiedIndice = []int{}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[11]
	dst = memory.New()
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[11], oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	// all blobs should be copied
	copiedIndice = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	uncopiedIndice = []int{}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
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
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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
		FindPredecessors: func(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			predecessors, err := src.Predecessors(ctx, desc)
			if err != nil {
				return nil, err
			}
			var filtered []ocispec.Descriptor
			for _, p := range predecessors {
				// filter media type
				switch p.MediaType {
				case spec.MediaTypeArtifactManifest:
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

func TestExtendedCopyGraph_FilterAnnotationWithRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, key string, value string) {
		manifest := spec.Artifact{
			MediaType:   spec.MediaTypeArtifactManifest,
			Subject:     &subject,
			Annotations: map[string]string{key: value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))   // descs[0]
	generateArtifactManifest(descs[0], "bar", "bluebrown")   // descs[1]
	generateArtifactManifest(descs[0], "bar", "blackred")    // descs[2]
	generateArtifactManifest(descs[0], "bar", "blackviolet") // descs[3]
	generateArtifactManifest(descs[0], "bar", "greengrey")   // descs[4]
	generateArtifactManifest(descs[0], "bar", "brownblack")  // descs[5]
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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

	// test FilterAnnotation with key unavailable in predecessors' annotation
	// should return nothing
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	exp = "black."
	regex = regexp.MustCompile(exp)
	opts.FilterAnnotation("bar1", regex)

	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0}
	uncopiedIndice = []int{1, 2, 3, 4, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	//test FilterAnnotation with key available in predecessors' annotation, regex equal to nil
	//should return all predecessors with the provided key
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	opts.FilterAnnotation("bar", nil)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0, 1, 2, 3, 4, 5}
	uncopiedIndice = []int{}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterAnnotationWithMultipleRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, key string, value string) {
		manifest := spec.Artifact{
			MediaType:   spec.MediaTypeArtifactManifest,
			Subject:     &subject,
			Annotations: map[string]string{key: value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))   // descs[0]
	generateArtifactManifest(descs[0], "bar", "bluebrown")   // descs[1]
	generateArtifactManifest(descs[0], "bar", "blackred")    // descs[2]
	generateArtifactManifest(descs[0], "bar", "blackviolet") // descs[3]
	generateArtifactManifest(descs[0], "bar", "greengrey")   // descs[4]
	generateArtifactManifest(descs[0], "bar", "brownblack")  // descs[5]
	generateArtifactManifest(descs[0], "bar", "blackblack")  // descs[6]
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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
	// test extended copy by descs[0] with two annotation filters
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	exp1 := "black."
	exp2 := ".pink|red"
	regex1 := regexp.MustCompile(exp1)
	regex2 := regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("bar", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 2}
	uncopiedIndice := []int{1, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with three annotation filters, nil included
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	exp1 = "black."
	exp2 = ".pink|red"
	regex1 = regexp.MustCompile(exp1)
	regex2 = regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("bar", nil)
	opts.FilterAnnotation("bar", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0, 2}
	uncopiedIndice = []int{1, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with two annotation filters, the second filter has an unavailable key
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	exp1 = "black."
	exp2 = ".pink|red"
	regex1 = regexp.MustCompile(exp1)
	regex2 = regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("test", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0}
	uncopiedIndice = []int{1, 2, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterAnnotationWithRegex_AnnotationInDescriptor(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType, key, value string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:   mediaType,
			Digest:      digest.FromBytes(blob),
			Size:        int64(len(blob)),
			Annotations: map[string]string{key: value},
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, key string, value string) {
		manifest := spec.Artifact{
			MediaType:   spec.MediaTypeArtifactManifest,
			Subject:     &subject,
			Annotations: map[string]string{key: value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, key, value, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageLayer, "", "", []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "bar", "bluebrown")         // descs[1]
	generateArtifactManifest(descs[0], "bar", "blackred")          // descs[2]
	generateArtifactManifest(descs[0], "bar", "blackviolet")       // descs[3]
	generateArtifactManifest(descs[0], "bar", "greengrey")         // descs[4]
	generateArtifactManifest(descs[0], "bar", "brownblack")        // descs[5]
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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
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

func TestExtendedCopyGraph_FilterAnnotationWithMultipleRegex_Referrers(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType, key, value string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:   mediaType,
			Digest:      digest.FromBytes(blob),
			Size:        int64(len(blob)),
			Annotations: map[string]string{key: value},
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, key string, value string) {
		manifest := spec.Artifact{
			MediaType:   spec.MediaTypeArtifactManifest,
			Subject:     &subject,
			Annotations: map[string]string{key: value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, key, value, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageLayer, "", "", []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "bar", "bluebrown")         // descs[1]
	generateArtifactManifest(descs[0], "bar", "blackred")          // descs[2]
	generateArtifactManifest(descs[0], "bar", "blackviolet")       // descs[3]
	generateArtifactManifest(descs[0], "bar", "greengrey")         // descs[4]
	generateArtifactManifest(descs[0], "bar", "brownblack")        // descs[5]
	generateArtifactManifest(descs[0], "bar", "blackblack")        // descs[6]

	// set up test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		var manifests []ocispec.Descriptor
		switch {
		case p == "/v2/test/referrers/"+descs[0].Digest.String():
			manifests = descs[1:]
			fallthrough
		case strings.HasPrefix(p, "/v2/test/referrers/"):
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: manifests,
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		case strings.Contains(p, descs[0].Digest.String()):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Content-Digest", descs[0].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[0])))
			w.Write(blobs[0])
		case strings.Contains(p, descs[1].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[1].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[1])))
			w.Write(blobs[1])
		case strings.Contains(p, descs[2].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[2].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[2])))
			w.Write(blobs[2])
		case strings.Contains(p, descs[3].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[3].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[3])))
			w.Write(blobs[3])
		case strings.Contains(p, descs[4].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[4].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[4])))
			w.Write(blobs[4])
		case strings.Contains(p, descs[5].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[5].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[5])))
			w.Write(blobs[5])
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Errorf("invalid test http server: %v", err)
	}

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Errorf("NewRepository() error = %v", err)
	}

	// test extended copy by descs[0] with two annotation filters
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	exp1 := "black."
	exp2 := ".pink|red"
	regex1 := regexp.MustCompile(exp1)
	regex2 := regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("bar", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 2}
	uncopiedIndice := []int{1, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with three annotation filters, nil included
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	exp1 = "black."
	exp2 = ".pink|red"
	regex1 = regexp.MustCompile(exp1)
	regex2 = regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("bar", nil)
	opts.FilterAnnotation("bar", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0, 2}
	uncopiedIndice = []int{1, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with two annotation filters, the second filter has an unavailable key
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	exp1 = "black."
	exp2 = ".pink|red"
	regex1 = regexp.MustCompile(exp1)
	regex2 = regexp.MustCompile(exp2)
	opts.FilterAnnotation("bar", regex1)
	opts.FilterAnnotation("test", regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0}
	uncopiedIndice = []int{1, 2, 3, 4, 5, 6}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
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
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")  // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")      // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")     // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")      // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")    // descs[5]

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src := memory.New()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp.
	exp := ".bar."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	regex := regexp.MustCompile(exp)
	opts.FilterArtifactType(regex)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 1, 3, 4}
	uncopiedIndice := []int{2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with no regex
	// type matches exp.
	opts = oras.ExtendedCopyGraphOptions{}
	opts.FilterArtifactType(nil)
	if opts.FindPredecessors != nil {
		t.Fatal("FindPredecessors not nil!")
	}
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
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")  // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")      // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")     // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")      // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")    // descs[5]

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
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp1 and exp2.
	exp1 := ".foo|bar."
	exp2 := "bad."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	regex1 := regexp.MustCompile(exp1)
	regex2 := regexp.MustCompile(exp2)
	opts.FilterArtifactType(regex1)
	opts.FilterArtifactType(regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 3, 4}
	uncopiedIndice := []int{1, 2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp1 and exp2 and nil
	exp1 = ".foo|bar."
	exp2 = "bad."
	dst = memory.New()
	opts = oras.ExtendedCopyGraphOptions{}
	regex1 = regexp.MustCompile(exp1)
	regex2 = regexp.MustCompile(exp2)
	opts.FilterArtifactType(regex1)
	opts.FilterArtifactType(regex2)
	opts.FilterArtifactType(nil)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice = []int{0, 3, 4}
	uncopiedIndice = []int{1, 2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterArtifactTypeWithRegex_ArtifactTypeInDescriptor(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, artifactType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:    mediaType,
			ArtifactType: artifactType,
			Digest:       digest.FromBytes(blob),
			Size:         int64(len(blob)),
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string) {
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, artifactType, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageLayer, "", []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")      // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")          // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")         // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")          // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")        // descs[5]

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src := memory.New()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp.
	exp := ".bar."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	regex := regexp.MustCompile(exp)
	opts.FilterArtifactType(regex)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 1, 3, 4}
	uncopiedIndice := []int{2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)

	// test extended copy by descs[0] with no regex
	// type matches exp.
	opts = oras.ExtendedCopyGraphOptions{}
	opts.FilterArtifactType(nil)
	if opts.FindPredecessors != nil {
		t.Fatal("FindPredecessors not nil!")
	}
}

func TestExtendedCopyGraph_FilterArtifactTypeWithMultipleRegex_Referrers(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, artifactType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:    mediaType,
			ArtifactType: artifactType,
			Digest:       digest.FromBytes(blob),
			Size:         int64(len(blob)),
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string) {
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, artifactType, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageLayer, "", []byte("foo")) // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow")      // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red")          // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue")         // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red")          // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink")        // descs[5]

	// set up test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		var manifests []ocispec.Descriptor
		switch {
		case p == "/v2/test/referrers/"+descs[0].Digest.String():
			manifests = descs[1:]
			fallthrough
		case strings.HasPrefix(p, "/v2/test/referrers/"):
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: manifests,
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		case strings.Contains(p, descs[0].Digest.String()):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Content-Digest", descs[0].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[0])))
			w.Write(blobs[0])
		case strings.Contains(p, descs[1].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[1].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[1])))
			w.Write(blobs[1])
		case strings.Contains(p, descs[2].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[2].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[2])))
			w.Write(blobs[2])
		case strings.Contains(p, descs[3].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[3].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[3])))
			w.Write(blobs[3])
		case strings.Contains(p, descs[4].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[4].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[4])))
			w.Write(blobs[4])
		case strings.Contains(p, descs[5].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[5].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[5])))
			w.Write(blobs[5])
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Errorf("invalid test http server: %v", err)
	}

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Errorf("NewRepository() error = %v", err)
	}

	// test extended copy by descs[0], include the predecessors whose artifact
	// type matches exp1 and exp2.
	exp1 := ".foo|bar."
	exp2 := "bad."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	regex1 := regexp.MustCompile(exp1)
	regex2 := regexp.MustCompile(exp2)
	opts.FilterArtifactType(regex1)
	opts.FilterArtifactType(regex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 3, 4}
	uncopiedIndice := []int{1, 2, 5}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterArtifactTypeAndAnnotationWithMultipleRegex(t *testing.T) {
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
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string, value string) {
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
			Annotations:  map[string]string{"rank": value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}
	generateImageManifest := func(subject, config ocispec.Descriptor, value string) {
		manifest := ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType:   ocispec.MediaTypeImageManifest,
			Config:      config,
			Subject:     &subject,
			Annotations: map[string]string{"rank": value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))       // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow", "1st") // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red", "1st")     // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue", "2nd")    // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red", "3rd")     // descs[4]
	appendBlob("good-woo-pink", []byte("bar"))                   // descs[5]
	generateImageManifest(descs[0], descs[5], "3rd")             // descs[6]
	appendBlob("bad-bar-pink", []byte("baz"))                    // descs[7]
	generateImageManifest(descs[0], descs[7], "4th")             // descs[8]
	appendBlob("bad-bar-orange", []byte("config!"))              // descs[9]
	generateImageManifest(descs[0], descs[9], "5th")             // descs[10]
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
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test extended copy by descs[0], include the predecessors whose artifact
	// type and annotation match the regular expressions.
	typeExp1 := ".foo|bar."
	typeExp2 := "bad."
	annotationExp1 := "[1-4]."
	annotationExp2 := "2|4."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	typeRegex1 := regexp.MustCompile(typeExp1)
	typeRegex2 := regexp.MustCompile(typeExp2)
	annotationRegex1 := regexp.MustCompile(annotationExp1)
	annotationRegex2 := regexp.MustCompile(annotationExp2)
	opts.FilterAnnotation("rank", annotationRegex1)
	opts.FilterArtifactType(typeRegex1)
	opts.FilterAnnotation("rank", annotationRegex2)
	opts.FilterArtifactType(typeRegex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 3, 7, 8}
	uncopiedIndice := []int{1, 2, 4, 5, 6, 9, 10}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopyGraph_FilterArtifactTypeAndAnnotationWithMultipleRegex_Referrers(t *testing.T) {
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, artifactType string, blob []byte, value string) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType:    mediaType,
			ArtifactType: artifactType,
			Digest:       digest.FromBytes(blob),
			Size:         int64(len(blob)),
			Annotations:  map[string]string{"rank": value},
		})
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, artifactType string, value string) {
		manifest := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: artifactType,
			Subject:      &subject,
			Annotations:  map[string]string{"rank": value},
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, artifactType, manifestJSON, value)
	}
	appendBlob(ocispec.MediaTypeImageLayer, "", []byte("foo"), "na") // descs[0]
	generateArtifactManifest(descs[0], "good-bar-yellow", "1st")     // descs[1]
	generateArtifactManifest(descs[0], "bad-woo-red", "1st")         // descs[2]
	generateArtifactManifest(descs[0], "bad-bar-blue", "2nd")        // descs[3]
	generateArtifactManifest(descs[0], "bad-bar-red", "3rd")         // descs[4]
	generateArtifactManifest(descs[0], "good-woo-pink", "2nd")       // descs[5]
	generateArtifactManifest(descs[0], "good-foo-blue", "3rd")       // descs[6]
	generateArtifactManifest(descs[0], "bad-bar-orange", "4th")      // descs[7]
	generateArtifactManifest(descs[0], "bad-woo-white", "4th")       // descs[8]
	generateArtifactManifest(descs[0], "good-woo-orange", "na")      // descs[9]

	// set up test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		var manifests []ocispec.Descriptor
		switch {
		case p == "/v2/test/referrers/"+descs[0].Digest.String():
			manifests = descs[1:]
			fallthrough
		case strings.HasPrefix(p, "/v2/test/referrers/"):
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: manifests,
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		case strings.Contains(p, descs[0].Digest.String()):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Content-Digest", descs[0].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[0])))
			w.Write(blobs[0])
		case strings.Contains(p, descs[1].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[1].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[1])))
			w.Write(blobs[1])
		case strings.Contains(p, descs[2].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[2].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[2])))
			w.Write(blobs[2])
		case strings.Contains(p, descs[3].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[3].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[3])))
			w.Write(blobs[3])
		case strings.Contains(p, descs[4].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[4].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[4])))
			w.Write(blobs[4])
		case strings.Contains(p, descs[5].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[5].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[5])))
			w.Write(blobs[5])
		case strings.Contains(p, descs[6].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[6].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[6])))
			w.Write(blobs[6])
		case strings.Contains(p, descs[7].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[7].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[7])))
			w.Write(blobs[7])
		case strings.Contains(p, descs[8].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[8].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[8])))
			w.Write(blobs[8])
		case strings.Contains(p, descs[9].Digest.String()):
			w.Header().Set("Content-Type", spec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", descs[9].Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(blobs[9])))
			w.Write(blobs[9])
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Errorf("invalid test http server: %v", err)
	}

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
			if _, err := content.FetchAll(ctx, dst, descs[i]); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("content[%d] error = %v, wantErr %v", i, err, errdef.ErrNotFound)
			}
		}
	}

	src, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Errorf("NewRepository() error = %v", err)
	}
	// test extended copy by descs[0], include the predecessors whose artifact
	// type and annotation match the regular expressions.
	typeExp1 := ".foo|bar."
	typeExp2 := "bad."
	annotationExp1 := "[1-4]."
	annotationExp2 := "2|4."
	dst := memory.New()
	opts := oras.ExtendedCopyGraphOptions{}
	typeRegex1 := regexp.MustCompile(typeExp1)
	typeRegex2 := regexp.MustCompile(typeExp2)
	annotationRegex1 := regexp.MustCompile(annotationExp1)
	annotationRegex2 := regexp.MustCompile(annotationExp2)
	opts.FilterAnnotation("rank", annotationRegex1)
	opts.FilterArtifactType(typeRegex1)
	opts.FilterAnnotation("rank", annotationRegex2)
	opts.FilterArtifactType(typeRegex2)
	if err := oras.ExtendedCopyGraph(ctx, src, dst, descs[0], opts); err != nil {
		t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, false)
	}
	copiedIndice := []int{0, 3, 7}
	uncopiedIndice := []int{1, 2, 4, 5, 6, 8, 9}
	verifyCopy(dst, copiedIndice, uncopiedIndice)
}

func TestExtendedCopy_CopyError(t *testing.T) {
	t.Run("src target is nil", func(t *testing.T) {
		ctx := context.Background()
		dst := memory.New()
		_, err := oras.ExtendedCopy(ctx, nil, "", dst, "", oras.DefaultExtendedCopyOptions)
		if err == nil {
			t.Errorf("ExtendedCopy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("dst target is nil", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		_, err := oras.ExtendedCopy(ctx, src, "", nil, "", oras.DefaultExtendedCopyOptions)
		if err == nil {
			t.Fatalf("ExtendedCopy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("find predecessors error", func(t *testing.T) {
		var errFindPredecessors = errors.New("find predecessors error")
		ctx := context.Background()
		src := memory.New()
		dst := memory.New()
		srcRef := "test"

		// prepare test content
		manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/test", oras.PackManifestOptions{})
		if err != nil {
			t.Fatalf("failed to pack test content: %v", err)
		}
		if err := src.Tag(ctx, manifestDesc, srcRef); err != nil {
			t.Fatalf("failed to tag test content on src: %v", err)
		}

		// test extended copy
		opts := oras.DefaultExtendedCopyOptions
		opts.FindPredecessors = func(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			return nil, errFindPredecessors
		}
		_, err = oras.ExtendedCopy(ctx, src, srcRef, dst, "", opts)
		if err == nil {
			t.Errorf("ExtendedCopy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errFindPredecessors; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyError error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})

	t.Run("tag error", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		dst := &badGraphTagger{
			GraphTarget: memory.New(),
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

		// test extended copy
		_, err = oras.ExtendedCopy(ctx, src, srcRef, dst, "", oras.DefaultExtendedCopyOptions)
		if err == nil {
			t.Errorf("ExtendedCopy() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopy() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if wantErr := errGraphTag; !errors.Is(copyErr.Err, wantErr) {
			t.Errorf("CopyError error = %v, wantErr %v", copyErr.Err, wantErr)
		}
	})
}

func TestExtendedCopyGraph_CopyError(t *testing.T) {

	t.Run("src target is nil", func(t *testing.T) {
		ctx := context.Background()
		dst := memory.New()
		err := oras.ExtendedCopyGraph(ctx, nil, dst, ocispec.Descriptor{}, oras.DefaultExtendedCopyGraphOptions)
		if err == nil {
			t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("dst target is nil", func(t *testing.T) {
		ctx := context.Background()
		src := memory.New()
		err := oras.ExtendedCopyGraph(ctx, src, nil, ocispec.Descriptor{}, oras.DefaultExtendedCopyGraphOptions)
		if err == nil {
			t.Errorf("ExtendedCopyGraph() error = %v, wantErr %v", err, true)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedCopyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginDestination; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

}

type badGraphTagger struct {
	oras.GraphTarget
}

var errGraphTag = errors.New("graph tag error")

func (bgt *badGraphTagger) Tag(_ context.Context, _ ocispec.Descriptor, _ string) error {
	return errGraphTag
}
