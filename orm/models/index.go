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
	"maps"

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

	// Lazy-loaded index and relationships.
	// Uses lazy[T] for thread-safe loading with retry on transient errors.
	index     lazy[*ocispec.Index]
	manifests lazy[[]Manifest]
	subject   lazy[Manifest]
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

// Annotations returns a copy of the annotations associated with this index.
// The returned map is safe to modify without affecting the index.
func (idx *Index) Annotations() map[string]string {
	return maps.Clone(idx.descriptor.Annotations)
}

// loadIndex loads the index from storage.
func (idx *Index) loadIndex(ctx context.Context) (*ocispec.Index, error) {
	return idx.index.get(func() (*ocispec.Index, error) {
		if idx.fetcher == nil {
			return nil, ErrNoFetcher
		}

		indexBytes, err := content.FetchAll(ctx, idx.fetcher, idx.descriptor)
		if err != nil {
			return nil, err
		}

		var index ocispec.Index
		if err := json.Unmarshal(indexBytes, &index); err != nil {
			return nil, err
		}

		return &index, nil
	})
}

// Load eagerly loads the index from storage.
func (idx *Index) Load(ctx context.Context) error {
	_, err := idx.loadIndex(ctx)
	return err
}

// Manifests returns all manifests in this index.
// The manifests are lazily loaded and cached.
func (idx *Index) Manifests(ctx context.Context) ([]Manifest, error) {
	return idx.manifests.get(func() ([]Manifest, error) {
		index, err := idx.loadIndex(ctx)
		if err != nil {
			return nil, err
		}

		if idx.client == nil {
			return nil, ErrNoClient
		}

		manifests := make([]Manifest, len(index.Manifests))
		for i, desc := range index.Manifests {
			manifest, err := idx.client.FetchManifest(ctx, desc)
			if err != nil {
				return nil, err
			}
			manifests[i] = manifest
		}
		return manifests, nil
	})
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
func (idx *Index) Subject(ctx context.Context) (Manifest, error) {
	return idx.subject.get(func() (Manifest, error) {
		index, err := idx.loadIndex(ctx)
		if err != nil {
			return nil, err
		}

		if index.Subject == nil {
			return nil, nil
		}

		if idx.client == nil {
			return nil, ErrNoClient
		}

		return idx.client.FetchManifest(ctx, *index.Subject)
	})
}

// SetSubject sets the subject manifest for this index.
func (idx *Index) SetSubject(subject Manifest) {
	idx.subject.set(subject)
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

	if idx.pusher == nil {
		return ErrNoPusher
	}

	index, err := idx.loadIndex(ctx)
	if err != nil {
		return err
	}

	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}

	return idx.pusher.Push(ctx, idx.descriptor, bytes.NewReader(indexBytes))
}

// MarshalJSON marshals the index to JSON.
// The index must have been loaded first via Load(ctx) or any method
// that accepts a context. Returns ErrNotLoaded if not yet loaded.
func (idx *Index) MarshalJSON() ([]byte, error) {
	index, ok := idx.index.peek()
	if !ok {
		return nil, ErrNotLoaded
	}
	return json.Marshal(index)
}
