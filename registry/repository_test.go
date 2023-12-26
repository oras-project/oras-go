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
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/container/set"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/spec"
)

var ErrBadFetch = errors.New("bad fetch error")

// testStorage implements content.ReadOnlyGraphStorage
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

func (s *testStorage) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.store.Exists(ctx, target)
}

func (s *testStorage) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.store.Predecessors(ctx, node)
}

// TestReferrerLister implements content.ReadOnlyGraphStorage and registry.ReferrerLister
type TestReferrerLister struct {
	*testStorage
}

func (rl *TestReferrerLister) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	results := []ocispec.Descriptor{desc}
	return fn(results)
}

func TestReferrers(t *testing.T) {
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
	generateManifestList := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			ArtifactType: "manifest list",
			Manifests:    manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(docker.MediaTypeManifestList, index.ArtifactType, indexJSON)
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
	generateManifestList(descs[4:7]...)                               // blob 9
	generateImageManifest(descs[0], nil, descs[4])                    // Blob 10
	generateArtifactManifest(nil, descs[5])                           // Blob 11

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
		{descs[4], descs[6], descs[10]}, // Blob 0
		{descs[4]},                      // Blob 1
		{descs[5]},                      // Blob 2
		{descs[6]},                      // Blob 3
		{descs[5], descs[7], descs[8], descs[9], descs[10]}, // Blob 4
		{descs[6], descs[7], descs[8], descs[9], descs[11]}, // Blob 5
		{descs[7], descs[8], descs[9]},                      // Blob 6
		{descs[8]},                                          // Blob 7
		nil,                                                 // Blob 8
		nil,                                                 // Blob 9
		nil,                                                 // Blob 10
		nil,                                                 // Blob 11
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
		nil,                  // Blob 9
		nil,                  // Blob 10
		nil,                  // Blob 11
	}
	for i := 0; i <= 3; i++ {
		_, err := Referrers(ctx, &s, descs[i], "")
		if !errors.Is(err, errdef.ErrUnsupported) {
			t.Errorf("Store.Referrers(%d) error = %v, want %v", i, err, errdef.ErrUnsupported)
		}
	}
	for i := 4; i < len(wantedReferrers); i++ {
		want := wantedReferrers[i]
		results, err := Referrers(ctx, &s, descs[i], "")
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
		nil,        // Blob 9
		nil,        // Blob 10
		nil,        // Blob 11
	}
	for i := 4; i < len(wantedReferrers); i++ {
		want := wantedReferrers[i]
		results, err := Referrers(ctx, &s, descs[i], "image manifest")
		if err != nil {
			t.Errorf("Store.Referrers(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(results, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, results, want)
		}
	}

	// test ReferrerLister
	rl := TestReferrerLister{testStorage: &s}
	wantedReferrers = [][]ocispec.Descriptor{
		nil,         // Blob 0
		nil,         // Blob 1
		nil,         // Blob 2
		nil,         // Blob 3
		{descs[4]},  // Blob 4
		{descs[5]},  // Blob 5
		{descs[6]},  // Blob 6
		{descs[7]},  // Blob 7
		{descs[8]},  // Blob 8
		{descs[9]},  // Blob 9
		{descs[10]}, // Blob 10
		{descs[11]}, // Blob 11
	}
	for i := 4; i < len(wantedReferrers); i++ {
		want := wantedReferrers[i]
		results, err := Referrers(ctx, &rl, descs[i], "")
		if err != nil {
			t.Errorf("Store.Referrers(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(results, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, results, want)
		}
	}
}

func TestReferrers_BadFetch(t *testing.T) {
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
	generateImageManifest(descs[0], &descs[4], descs[2])              // Blob 5
	generateArtifactManifest(&descs[5], descs[3])                     // Blob 6
	generateIndex(&descs[6])                                          // Blob 7
	s.badFetch.Add(descs[5].Digest)
	s.badFetch.Add(descs[6].Digest)
	s.badFetch.Add(descs[7].Digest)

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

	for i := 4; i < 7; i++ {
		_, err := Referrers(ctx, &s, descs[i], "")
		if !errors.Is(err, ErrBadFetch) {
			t.Errorf("Store.Referrers(%d) error = %v, want %v", i, err, ErrBadFetch)
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
