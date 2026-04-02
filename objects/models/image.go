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

// Compile-time interface check.
var _ Manifest = (*Image)(nil)

// Image represents an OCI or Docker image manifest.
// Images have a config and layers, and may support platform specification.
type Image struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher
	client     ManifestClient

	// Lazy-loaded manifest and relationships.
	// Uses lazy[T] for thread-safe loading with retry on transient errors.
	manifest lazy[*ocispec.Manifest]
	config   lazy[*Blob]
	layers   lazy[[]*Blob]
	subject  lazy[Manifest]
}

// NewImage creates a new Image from a descriptor.
func NewImage(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher, client ManifestClient) *Image {
	return &Image{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
		client:     client,
	}
}

// NewImageFromManifestBytes creates a new Image with a pre-loaded manifest.
// This avoids a redundant network fetch when the manifest bytes are already
// available (e.g., from type detection).
func NewImageFromManifestBytes(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher, client ManifestClient, manifestBytes []byte) (*Image, error) {
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, &ObjectsError{Op: "load", Digest: desc.Digest, Err: err}
	}
	img := &Image{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
		client:     client,
	}
	img.manifest.set(&manifest)
	return img, nil
}

// Descriptor returns the OCI descriptor for this image.
func (i *Image) Descriptor() ocispec.Descriptor {
	return i.descriptor
}

// Digest returns the digest of the image manifest.
func (i *Image) Digest() digest.Digest {
	return i.descriptor.Digest
}

// MediaType returns the media type of the image.
func (i *Image) MediaType() string {
	return i.descriptor.MediaType
}

// Size returns the size of the image manifest in bytes.
func (i *Image) Size() int64 {
	return i.descriptor.Size
}

// Annotations returns a copy of the annotations associated with this image.
// If the manifest is already loaded, annotations are read from the manifest
// body (where they are authoritative). Otherwise the descriptor annotations
// are used as a fallback. The returned map is safe to modify.
func (i *Image) Annotations() map[string]string {
	if m, ok := i.manifest.peek(); ok {
		return maps.Clone(m.Annotations)
	}
	return maps.Clone(i.descriptor.Annotations)
}

// loadManifest loads the image manifest from storage.
func (i *Image) loadManifest(ctx context.Context) (*ocispec.Manifest, error) {
	return i.manifest.get(func() (*ocispec.Manifest, error) {
		if i.fetcher == nil {
			return nil, &ObjectsError{Op: "load", Digest: i.descriptor.Digest, Err: ErrNoFetcher}
		}

		manifestBytes, err := content.FetchAll(ctx, i.fetcher, i.descriptor)
		if err != nil {
			return nil, &ObjectsError{Op: "load", Digest: i.descriptor.Digest, Err: err}
		}

		var manifest ocispec.Manifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return nil, &ObjectsError{Op: "load", Digest: i.descriptor.Digest, Err: err}
		}

		return &manifest, nil
	})
}

// Load eagerly loads the image manifest from storage.
func (i *Image) Load(ctx context.Context) error {
	_, err := i.loadManifest(ctx)
	return err
}

// Config returns the config blob for this image.
// The config is lazily loaded and cached.
func (i *Image) Config(ctx context.Context) (*Blob, error) {
	return i.config.get(func() (*Blob, error) {
		manifest, err := i.loadManifest(ctx)
		if err != nil {
			return nil, err
		}

		return NewBlob(manifest.Config, i.fetcher, i.pusher), nil
	})
}

// Layers returns all layer blobs for this image.
// The layers are lazily loaded and cached.
func (i *Image) Layers(ctx context.Context) ([]*Blob, error) {
	return i.layers.get(func() ([]*Blob, error) {
		manifest, err := i.loadManifest(ctx)
		if err != nil {
			return nil, err
		}

		layers := make([]*Blob, len(manifest.Layers))
		for idx, desc := range manifest.Layers {
			layers[idx] = NewBlob(desc, i.fetcher, i.pusher)
		}
		return layers, nil
	})
}

// Platform returns the platform specification for this image.
// Returns nil if no platform is specified.
func (i *Image) Platform(ctx context.Context) (*ocispec.Platform, error) {
	return i.descriptor.Platform, nil
}

// Subject returns the subject manifest this image refers to.
// Returns nil if no subject is set.
func (i *Image) Subject(ctx context.Context) (Manifest, error) {
	return i.subject.get(func() (Manifest, error) {
		manifest, err := i.loadManifest(ctx)
		if err != nil {
			return nil, err // already wrapped by loadManifest
		}

		if manifest.Subject == nil {
			return nil, nil
		}

		if i.client == nil {
			return nil, &ObjectsError{Op: "fetch_subject", Digest: i.descriptor.Digest, Err: ErrNoClient}
		}

		subj, err := i.client.FetchManifest(ctx, *manifest.Subject)
		if err != nil {
			return nil, &ObjectsError{Op: "fetch_subject", Digest: i.descriptor.Digest, Err: err}
		}
		return subj, nil
	})
}

// SetSubject sets the subject manifest for this image.
func (i *Image) SetSubject(subject Manifest) {
	i.subject.set(subject)
}

// Predecessors returns all manifests that reference this image.
func (i *Image) Predecessors(ctx context.Context) ([]Manifest, error) {
	if i.client == nil {
		return nil, ErrNoClient
	}
	return i.client.FindPredecessors(ctx, i)
}

// Push pushes this image manifest to the target with the given reference.
func (i *Image) Push(ctx context.Context, reference string) error {
	if i.client != nil {
		return i.client.PushManifest(ctx, i, reference)
	}

	if i.pusher == nil {
		return ErrNoPusher
	}

	manifest, err := i.loadManifest(ctx)
	if err != nil {
		return err
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	return i.pusher.Push(ctx, i.descriptor, bytes.NewReader(manifestBytes))
}

// MarshalJSON marshals the image manifest to JSON.
// The manifest must have been loaded first via Load(ctx) or any method
// that accepts a context. Returns ErrNotLoaded if not yet loaded.
func (i *Image) MarshalJSON() ([]byte, error) {
	manifest, ok := i.manifest.peek()
	if !ok {
		return nil, ErrNotLoaded
	}
	return json.Marshal(manifest)
}
