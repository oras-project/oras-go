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

package models

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
)

// Index represents an OCI image index (manifest list).
// Indexes contain multiple manifests, typically for different platforms.
type Index struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher
	client     ManifestClient

	// Lazy-loaded index
	index     *ocispec.Index
	indexOnce sync.Once
	indexErr  error

	// Lazy-loaded relationships
	manifests     []Manifest
	manifestsOnce sync.Once
	manifestsErr  error

	subject     Manifest
	subjectOnce sync.Once
	subjectErr  error
}

// NewIndex creates a new Index from a descriptor.
func NewIndex(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher, client ManifestClient) *Index {
	return &Index{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
		client:     client,
	}
}

// Descriptor returns the OCI descriptor for this index.
func (idx *Index) Descriptor() ocispec.Descriptor {
	return idx.descriptor
}

// Digest returns the digest of the index.
func (idx *Index) Digest() digest.Digest {
	return idx.descriptor.Digest
}

// MediaType returns the media type of the index.
func (idx *Index) MediaType() string {
	return idx.descriptor.MediaType
}

// Size returns the size of the index in bytes.
func (idx *Index) Size() int64 {
	return idx.descriptor.Size
}

// Annotations returns the annotations associated with this index.
func (idx *Index) Annotations() map[string]string {
	return idx.descriptor.Annotations
}

// loadIndex loads the index from storage.
func (idx *Index) loadIndex(ctx context.Context) (*ocispec.Index, error) {
	idx.indexOnce.Do(func() {
		if idx.index != nil {
			return // Already loaded
		}

		if idx.fetcher == nil {
			idx.indexErr = ErrNoFetcher
			return
		}

		// Fetch index content
		indexBytes, err := content.FetchAll(ctx, idx.fetcher, idx.descriptor)
		if err != nil {
			idx.indexErr = err
			return
		}

		// Parse index
		var index ocispec.Index
		if err := json.Unmarshal(indexBytes, &index); err != nil {
			idx.indexErr = err
			return
		}

		idx.index = &index
	})

	return idx.index, idx.indexErr
}

// Manifests returns all manifests in this index.
// The manifests are lazily loaded and cached.
func (idx *Index) Manifests(ctx context.Context) ([]Manifest, error) {
	idx.manifestsOnce.Do(func() {
		index, err := idx.loadIndex(ctx)
		if err != nil {
			idx.manifestsErr = err
			return
		}

		if idx.client == nil {
			idx.manifestsErr = ErrNoClient
			return
		}

		// Convert descriptors to Manifest objects
		idx.manifests = make([]Manifest, len(index.Manifests))
		for i, desc := range index.Manifests {
			manifest, err := idx.client.FetchManifest(ctx, desc)
			if err != nil {
				idx.manifestsErr = err
				return
			}
			idx.manifests[i] = manifest
		}
	})

	return idx.manifests, idx.manifestsErr
}

// FilterByPlatform returns manifests matching the given platform.
func (idx *Index) FilterByPlatform(ctx context.Context, platform *ocispec.Platform) ([]Manifest, error) {
	manifests, err := idx.Manifests(ctx)
	if err != nil {
		return nil, err
	}

	if platform == nil {
		return manifests, nil
	}

	var filtered []Manifest
	for _, manifest := range manifests {
		desc := manifest.Descriptor()
		if desc.Platform != nil && platformMatches(desc.Platform, platform) {
			filtered = append(filtered, manifest)
		}
	}

	return filtered, nil
}

// platformMatches checks if two platforms match.
func platformMatches(a, b *ocispec.Platform) bool {
	if a.Architecture != b.Architecture {
		return false
	}
	if a.OS != b.OS {
		return false
	}
	// Variant is optional
	if b.Variant != "" && a.Variant != b.Variant {
		return false
	}
	return true
}

// Subject returns the subject manifest this index refers to.
// Returns nil if no subject is set.
func (idx *Index) Subject() (Manifest, error) {
	idx.subjectOnce.Do(func() {
		index, err := idx.loadIndex(context.Background())
		if err != nil {
			idx.subjectErr = err
			return
		}

		if index.Subject == nil {
			return // No subject
		}

		if idx.client == nil {
			idx.subjectErr = ErrNoClient
			return
		}

		// Fetch subject manifest
		idx.subject, idx.subjectErr = idx.client.FetchManifest(context.Background(), *index.Subject)
	})

	return idx.subject, idx.subjectErr
}

// SetSubject sets the subject manifest for this index.
func (idx *Index) SetSubject(subject Manifest) {
	idx.subject = subject
	idx.subjectOnce = sync.Once{} // Reset once to allow re-evaluation
}

// Predecessors returns all manifests that reference this index.
func (idx *Index) Predecessors(ctx context.Context) ([]Manifest, error) {
	if idx.client == nil {
		return nil, ErrNoClient
	}
	return idx.client.FindPredecessors(ctx, idx)
}

// Push pushes this index to the target with the given reference.
func (idx *Index) Push(ctx context.Context, reference string) error {
	if idx.client != nil {
		return idx.client.PushManifest(ctx, idx, reference)
	}

	// Fallback to direct push if no client
	if idx.pusher == nil {
		return ErrNoPusher
	}

	indexBytes, err := idx.MarshalJSON()
	if err != nil {
		return err
	}

	return idx.pusher.Push(ctx, idx.descriptor, bytes.NewReader(indexBytes))
}

// MarshalJSON marshals the index to JSON.
func (idx *Index) MarshalJSON() ([]byte, error) {
	index, err := idx.loadIndex(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(index)
}
