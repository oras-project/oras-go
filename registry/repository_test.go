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

package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/internal/spec"
)

// testStorage implements content.ReadOnlyGraphStorage
type testStorage struct {
	store *memory.Store
}

func (s *testStorage) Push(ctx context.Context, expected ocispec.Descriptor, reader io.Reader) error {
	return s.store.Push(ctx, expected, reader)
}

func (s *testStorage) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.store.Fetch(ctx, target)
}

func (s *testStorage) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.store.Exists(ctx, target)
}

func (s *testStorage) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.store.Predecessors(ctx, node)
}

func TestReferrers(t *testing.T) {
	s := testStorage{
		store: memory.New(),
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
	generateArtifactManifest := func(subject *ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		artifact := spec.Artifact{
			MediaType:    spec.MediaTypeArtifactManifest,
			ArtifactType: "artifact",
			Subject:      subject,
			Blobs:        blobs,
			Annotations:  map[string]string{"test": "content"},
		}
		manifestJSON, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, artifact.ArtifactType, manifestJSON)
	}
	generateIndex := func(subject *ocispec.Descriptor, manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			MediaType:    ocispec.MediaTypeImageIndex,
			ArtifactType: "index",
			Subject:      subject,
			Manifests:    manifests,
			Annotations:  map[string]string{"test": "content"},
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, index.ArtifactType, indexJSON)
	}

	appendBlob("image manifest", "image config", []byte("config"))    // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("foo"))   // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("bar"))   // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, "layer", []byte("hello")) // Blob 3
	generateImageManifest(descs[0], nil, descs[1])                    // Blob 4
	generateArtifactManifest(&descs[4], descs[2])                     // Blob 5
	generateImageManifest(descs[0], &descs[5], descs[3])              // Blob 6
	generateIndex(&descs[6], descs[4:6]...)                           // Blob 7
	generateIndex(&descs[4], descs[5:8]...)                           // Blob 8

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
	wantedPredecessors := [][]ocispec.Descriptor{
		{descs[4], descs[6]},           // Blob 0
		{descs[4]},                     // Blob 1
		{descs[5]},                     // Blob 2
		{descs[6]},                     // Blob 3
		{descs[5], descs[7], descs[8]}, // Blob 4
		{descs[6], descs[7], descs[8]}, // Blob 5
		{descs[7], descs[8]},           // Blob 6
		{descs[8]},                     // Blob 7
		nil,                            // Blob 8
	}
	for i, want := range wantedPredecessors {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}

	// verify referrers
	wantedReferrers := [][]ocispec.Descriptor{
		nil,                  // Blob 0
		nil,                  // Blob 1
		nil,                  // Blob 2
		nil,                  // Blob 3
		{descs[5], descs[8]}, // Blob 4
		{descs[6]},           // Blob 5
		{descs[7]},           // Blob 6
		nil,                  // Blob 7
		nil,                  // Blob 8
	}
	for i := 4; i < len(wantedReferrers); i++ {
		want := wantedReferrers[i]
		var results []ocispec.Descriptor
		err := Referrers(ctx, &s, descs[i], "", func(referrers []ocispec.Descriptor) error {
			results = append(results, referrers...)
			return nil
		})
		if err != nil {
			t.Errorf("Store.Referrers(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(results, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, results, want)
		}
	}

	// test filtering on ArtifactType
	wantedReferrers = [][]ocispec.Descriptor{
		nil,        // Blob 0
		nil,        // Blob 1
		nil,        // Blob 2
		nil,        // Blob 3
		nil,        // Blob 4
		{descs[6]}, // Blob 5
		nil,        // Blob 6
		nil,        // Blob 7
		nil,        // Blob 8
	}
	for i := 4; i < len(wantedReferrers); i++ {
		want := wantedReferrers[i]
		var results []ocispec.Descriptor
		err := Referrers(ctx, &s, descs[i], "image manifest", func(referrers []ocispec.Descriptor) error {
			results = append(results, referrers...)
			return nil
		})
		if err != nil {
			t.Errorf("Store.Referrers(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(results, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, results, want)
		}
	}
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
