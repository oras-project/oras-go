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

package manifestutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/container/set"
	"oras.land/oras-go/v2/internal/docker"
)

var ErrBadFetch = errors.New("bad fetch error")

// testStorage implements Fetcher
type testStorage struct {
	store    *memory.Store
	badFetch set.Set[digest.Digest]
}

func (s *testStorage) Push(ctx context.Context, expected ocispec.Descriptor, reader io.Reader) error {
	return s.store.Push(ctx, expected, reader)
}

func (s *testStorage) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if s.badFetch.Contains(target.Digest) {
		return nil, ErrBadFetch
	}
	return s.store.Fetch(ctx, target)
}

func TestConfig(t *testing.T) {
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))           // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))               // Blob 1
	generateManifest(ocispec.MediaTypeImageManifest, descs[0], descs[1]) // Blob 2
	generateManifest(docker.MediaTypeManifest, descs[0], descs[1])       // Blob 3
	generateManifest("whatever", descs[0], descs[1])                     // Blob 4

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	tests := []struct {
		name    string
		desc    ocispec.Descriptor
		want    *ocispec.Descriptor
		wantErr bool
	}{
		{
			name: "OCI Image Manifest",
			desc: descs[2],
			want: &descs[0],
		},
		{
			name:    "Docker Manifest",
			desc:    descs[3],
			want:    &descs[0],
			wantErr: false,
		},
		{
			name: "Other media type",
			desc: descs[4],
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Config(ctx, storage, tt.desc)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_ErrorPath(t *testing.T) {
	data := []byte("data")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}

	t.Run("Fetch error", func(t *testing.T) {
		storage := cas.NewMemory()
		ctx := context.Background()
		if _, err := Config(ctx, storage, desc); err == nil {
			t.Error("Config() error = nil, wantErr = true")
		}
	})

	t.Run("Unmarshal error", func(t *testing.T) {
		storage := cas.NewMemory()
		ctx := context.Background()
		if err := storage.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("failed to push test content to src: %v", err)
		}
		_, err := Config(ctx, storage, desc)
		if err == nil {
			t.Error("Config() error = nil, wantErr = true")
		}
	})
}

func TestManifests(t *testing.T) {
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
	generateIndex := func(mediaType string, subject *ocispec.Descriptor, manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Subject:   subject,
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(mediaType, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))           // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))               // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))               // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))             // Blob 3
	generateManifest(nil, descs[0], descs[1:3]...)                       // Blob 4
	generateManifest(nil, descs[0], descs[3])                            // Blob 5
	appendBlob(ocispec.MediaTypeImageConfig, []byte("{}"))               // Blob 6
	appendBlob("test/sig", []byte("sig"))                                // Blob 7
	generateManifest(&descs[4], descs[5], descs[6])                      // Blob 8
	generateIndex(ocispec.MediaTypeImageIndex, &descs[8], descs[4:6]...) // Blob 9
	generateIndex(docker.MediaTypeManifestList, nil, descs[4:6]...)      // Blob 10
	generateIndex("whatever", &descs[8], descs[4:6]...)                  // Blob 11

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	tests := []struct {
		name    string
		desc    ocispec.Descriptor
		want    []ocispec.Descriptor
		wantErr bool
	}{
		{
			name: "OCI Image Index",
			desc: descs[9],
			want: descs[4:6],
		},
		{
			name:    "Docker Manifest List",
			desc:    descs[10],
			want:    descs[4:6],
			wantErr: false,
		},
		{
			name: "Other media type",
			desc: descs[11],
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Manifests(ctx, storage, tt.desc)
			if (err != nil) != tt.wantErr {
				t.Errorf("Manifests() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Manifests() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManifests_ErrorPath(t *testing.T) {
	data := []byte("data")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}

	t.Run("Fetch error", func(t *testing.T) {
		storage := cas.NewMemory()
		ctx := context.Background()
		if _, err := Manifests(ctx, storage, desc); err == nil {
			t.Error("Manifests() error = nil, wantErr = true")
		}
	})

	t.Run("Unmarshal error", func(t *testing.T) {
		storage := cas.NewMemory()
		ctx := context.Background()
		if err := storage.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("failed to push test content to src: %v", err)
		}
		_, err := Manifests(ctx, storage, desc)
		if err == nil {
			t.Error("Manifests() error = nil, wantErr = true")
		}
	})
}

func TestSubject(t *testing.T) {
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
	generateManifest := func(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config:  config,
			Subject: subject,
			Layers:  layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob"))    // Blob 1
	generateManifest(descs[0], nil, descs[1])                  // Blob 2, manifest
	generateManifest(descs[0], &descs[2], descs[1])            // Blob 3, referrer of blob 2

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}
	got, err := Subject(ctx, storage, descs[3])
	if err != nil {
		t.Fatalf("error when getting subject: %v", err)
	}
	if !reflect.DeepEqual(*got, descs[2]) {
		t.Errorf("Subject() = %v, want %v", got, descs[2])
	}
	got, err = Subject(ctx, storage, descs[0])
	if err != nil {
		t.Fatalf("error when getting subject: %v", err)
	}
	if got != nil {
		t.Errorf("Subject() = %v, want %v", got, nil)
	}
}

func TestSubject_ErrorPath(t *testing.T) {

	t.Run("Fetch error", func(t *testing.T) {
		s := testStorage{
			store:    memory.New(),
			badFetch: set.New[digest.Digest](),
		}
		ctx := context.Background()

		// generate test content
		var blobs [][]byte
		var descs []ocispec.Descriptor
		appendBlob := func(mediaType string, artifactType string, blob []byte) {
			blobs = append(blobs, blob)
			descs = append(descs, ocispec.Descriptor{
				MediaType:    mediaType,
				ArtifactType: artifactType,
				Annotations:  map[string]string{"test": "content"},
				Digest:       digest.FromBytes(blob),
				Size:         int64(len(blob)),
			})
		}
		generateImageManifest := func(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) {
			manifest := ocispec.Manifest{
				MediaType:   ocispec.MediaTypeImageManifest,
				Config:      config,
				Subject:     subject,
				Layers:      layers,
				Annotations: map[string]string{"test": "content"},
			}
			manifestJSON, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			appendBlob(ocispec.MediaTypeImageManifest, manifest.Config.MediaType, manifestJSON)
		}
		appendBlob("image manifest", "image config", []byte("config"))    // Blob 0
		appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("foo"))   // Blob 1
		appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("bar"))   // Blob 2
		appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("hello")) // Blob 3
		generateImageManifest(descs[0], nil, descs[1])                    // Blob 4
		generateImageManifest(descs[0], &descs[4], descs[2])              // Blob 5
		s.badFetch.Add(descs[5].Digest)

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

		// test fetch error
		if _, err := Subject(ctx, &s, descs[5]); !errors.Is(err, ErrBadFetch) {
			t.Errorf("Subject() error = %v, want %v", err, ErrBadFetch)
		}
	})

	t.Run("Unmarshal error", func(t *testing.T) {
		data := []byte("data")
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageIndex,
			Digest:    digest.FromBytes(data),
			Size:      int64(len(data)),
		}

		storage := cas.NewMemory()
		ctx := context.Background()
		if err := storage.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("failed to push test content to src: %v", err)
		}
		_, err := Subject(ctx, storage, desc)
		if err == nil {
			t.Error("Subject() error = nil, wantErr = true")
		}
	})
}
