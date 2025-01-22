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

package content_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/spec"
)

func TestSuccessors_dockerManifest(t *testing.T) {
	storage := cas.NewMemory()

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
		appendBlob(docker.MediaTypeManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:4]...)                  // Blob 4

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors
	manifestDesc := descs[4]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[0:4]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}
}

func TestSuccessors_imageManifest(t *testing.T) {
	storage := cas.NewMemory()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(nil, descs[0], descs[1:4]...)             // Blob 4
	appendBlob(ocispec.MediaTypeImageConfig, []byte("{}"))     // Blob 5
	appendBlob("test/sig", []byte("sig"))                      // Blob 6
	generateManifest(&descs[4], descs[5], descs[6])            // Blob 7

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors: image manifest without a subject
	manifestDesc := descs[4]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[0:4]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}

	// test Successors: image manifest with a subject
	manifestDesc = descs[7]
	got, err = content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[4:7]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}
}

func TestSuccessors_dockerManifestList(t *testing.T) {
	storage := cas.NewMemory()

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
		appendBlob(docker.MediaTypeManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(docker.MediaTypeManifestList, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateIndex(descs[4:6]...)                               // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors
	manifestDesc := descs[6]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[4:6]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}
}

func TestSuccessors_imageIndex(t *testing.T) {
	storage := cas.NewMemory()

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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(nil, descs[0], descs[1:3]...)             // Blob 4
	generateManifest(nil, descs[0], descs[3])                  // Blob 5
	appendBlob(ocispec.MediaTypeImageConfig, []byte("{}"))     // Blob 6
	appendBlob("test/sig", []byte("sig"))                      // Blob 7
	generateManifest(&descs[4], descs[5], descs[6])            // Blob 8
	generateIndex(&descs[8], descs[4:6]...)                    // Blob 9

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors
	manifestDesc := descs[9]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := append([]ocispec.Descriptor{descs[8]}, descs[4:6]...); !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}
}

func TestSuccessors_artifactManifest(t *testing.T) {
	storage := cas.NewMemory()

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
	generateArtifactManifest := func(subject *ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		manifest := spec.Artifact{
			Subject: subject,
			Blobs:   blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))   // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))   // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello")) // Blob 2
	generateArtifactManifest(nil, descs[0:3]...)             // Blob 3
	appendBlob("test/sig", []byte("sig"))                    // Blob 4
	generateArtifactManifest(&descs[3], descs[4])            // Blob 5

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors: image manifest without a subject
	manifestDesc := descs[3]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[0:3]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}

	// test Successors: image manifest with a subject
	manifestDesc = descs[5]
	got, err = content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if want := descs[3:5]; !reflect.DeepEqual(got, want) {
		t.Errorf("Successors() = %v, want %v", got, want)
	}
}

func TestSuccessors_otherMediaType(t *testing.T) {
	storage := cas.NewMemory()

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
	generateManifest := func(mediaType string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(mediaType, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest("whatever", descs[0], descs[1:4]...)      // Blob 4

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test Successors: other media type
	manifestDesc := descs[4]
	got, err := content.Successors(ctx, storage, manifestDesc)
	if err != nil {
		t.Fatal("Successors() error =", err)
	}
	if got != nil {
		t.Errorf("Successors() = %v, want nil", got)
	}
}

func TestSuccessors_ErrNotFound(t *testing.T) {
	tests := []struct {
		name string
		desc ocispec.Descriptor
	}{
		{
			name: "docker manifest",
			desc: ocispec.Descriptor{
				MediaType: docker.MediaTypeManifest,
			},
		},
		{
			name: "image manifest",
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
			},
		},
		{
			name: "docker manifest list",
			desc: ocispec.Descriptor{
				MediaType: docker.MediaTypeManifestList,
			},
		},
		{
			name: "image index",
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageIndex,
			},
		},
		{
			name: "artifact manifest",
			desc: ocispec.Descriptor{
				MediaType: spec.MediaTypeArtifactManifest,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fetcher := cas.NewMemory()
			if _, err := content.Successors(ctx, fetcher, tt.desc); !errors.Is(err, errdef.ErrNotFound) {
				t.Errorf("Successors() error = %v, wantErr = %v", err, errdef.ErrNotFound)
			}
		})
	}
}

func TestSuccessors_UnmarshalError(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
	}{
		{
			name:      "docker manifest",
			mediaType: docker.MediaTypeManifest,
		},
		{
			name:      "image manifest",
			mediaType: ocispec.MediaTypeImageManifest,
		},
		{
			name:      "docker manifest list",
			mediaType: docker.MediaTypeManifestList,
		},
		{
			name:      "image index",
			mediaType: ocispec.MediaTypeImageIndex,
		},
		{
			name:      "artifact manifest",
			mediaType: spec.MediaTypeArtifactManifest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fetcher := cas.NewMemory()

			// prepare test content
			data := "invalid json"
			desc := ocispec.Descriptor{
				MediaType: tt.mediaType,
				Digest:    digest.FromString(data),
				Size:      int64(len(data)),
			}
			if err := fetcher.Push(ctx, desc, bytes.NewReader([]byte(data))); err != nil {
				t.Fatalf("failed to push test content to fetcher: %v", err)
			}

			// test Successors
			if _, err := content.Successors(ctx, fetcher, desc); err == nil {
				t.Error("Successors() error = nil, wantErr = true")
			}
		})
	}
}
