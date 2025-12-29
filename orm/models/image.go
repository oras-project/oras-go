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

// Image represents an OCI or Docker image manifest.
// Images have a config and layers, and may support platform specification.
type Image struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher
	client     ManifestClient

	// Lazy-loaded manifest
	manifest     *ocispec.Manifest
	manifestOnce sync.Once
	manifestErr  error

	// Lazy-loaded relationships
	config     *Blob
	configOnce sync.Once
	configErr  error

	layers     []*Blob
	layersOnce sync.Once
	layersErr  error

	subject     Manifest
	subjectOnce sync.Once
	subjectErr  error
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

// Annotations returns the annotations associated with this image.
func (i *Image) Annotations() map[string]string {
	return i.descriptor.Annotations
}

// loadManifest loads the image manifest from storage.
func (i *Image) loadManifest(ctx context.Context) (*ocispec.Manifest, error) {
	i.manifestOnce.Do(func() {
		if i.manifest != nil {
			return // Already loaded
		}

		if i.fetcher == nil {
			i.manifestErr = ErrNoFetcher
			return
		}

		// Fetch manifest content
		manifestBytes, err := content.FetchAll(ctx, i.fetcher, i.descriptor)
		if err != nil {
			i.manifestErr = err
			return
		}

		// Parse image manifest
		var manifest ocispec.Manifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			i.manifestErr = err
			return
		}

		i.manifest = &manifest
	})

	return i.manifest, i.manifestErr
}

// Config returns the config blob for this image.
// The config is lazily loaded and cached.
func (i *Image) Config(ctx context.Context) (*Blob, error) {
	i.configOnce.Do(func() {
		manifest, err := i.loadManifest(ctx)
		if err != nil {
			i.configErr = err
			return
		}

		i.config = NewBlob(manifest.Config, i.fetcher, i.pusher)
	})

	return i.config, i.configErr
}

// Layers returns all layer blobs for this image.
// The layers are lazily loaded and cached.
func (i *Image) Layers(ctx context.Context) ([]*Blob, error) {
	i.layersOnce.Do(func() {
		manifest, err := i.loadManifest(ctx)
		if err != nil {
			i.layersErr = err
			return
		}

		// Convert descriptors to Blob objects
		i.layers = make([]*Blob, len(manifest.Layers))
		for idx, desc := range manifest.Layers {
			i.layers[idx] = NewBlob(desc, i.fetcher, i.pusher)
		}
	})

	return i.layers, i.layersErr
}

// Platform returns the platform specification for this image.
// Returns nil if no platform is specified.
func (i *Image) Platform(ctx context.Context) (*ocispec.Platform, error) {
	// Platform is stored in the descriptor, not the manifest
	return i.descriptor.Platform, nil
}

// Subject returns the subject manifest this image refers to.
// Returns nil if no subject is set.
func (i *Image) Subject() (Manifest, error) {
	i.subjectOnce.Do(func() {
		manifest, err := i.loadManifest(context.Background())
		if err != nil {
			i.subjectErr = err
			return
		}

		if manifest.Subject == nil {
			return // No subject
		}

		if i.client == nil {
			i.subjectErr = ErrNoClient
			return
		}

		// Fetch subject manifest
		i.subject, i.subjectErr = i.client.FetchManifest(context.Background(), *manifest.Subject)
	})

	return i.subject, i.subjectErr
}

// SetSubject sets the subject manifest for this image.
func (i *Image) SetSubject(subject Manifest) {
	i.subject = subject
	i.subjectOnce = sync.Once{} // Reset once to allow re-evaluation
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

	// Fallback to direct push if no client
	if i.pusher == nil {
		return ErrNoPusher
	}

	manifestBytes, err := i.MarshalJSON()
	if err != nil {
		return err
	}

	return i.pusher.Push(ctx, i.descriptor, bytes.NewReader(manifestBytes))
}

// MarshalJSON marshals the image manifest to JSON.
func (i *Image) MarshalJSON() ([]byte, error) {
	manifest, err := i.loadManifest(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(manifest)
}
