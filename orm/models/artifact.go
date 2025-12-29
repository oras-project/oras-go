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
	"github.com/oras-project/oras-go/v3/internal/spec"
)

// Artifact represents an OCI artifact manifest.
// Artifacts contain typed blobs and can reference a subject manifest.
type Artifact struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher
	client     ManifestClient

	// Lazy-loaded manifest
	manifest     *spec.Artifact
	manifestOnce sync.Once
	manifestErr  error

	// Lazy-loaded relationships
	blobs     []*Blob
	blobsOnce sync.Once
	blobsErr  error

	subject     Manifest
	subjectOnce sync.Once
	subjectErr  error
}

// ManifestClient provides operations for manifest relationships.
type ManifestClient interface {
	// FetchManifest fetches a manifest by descriptor.
	FetchManifest(ctx context.Context, desc ocispec.Descriptor) (Manifest, error)

	// FindPredecessors finds all manifests that reference the given content.
	FindPredecessors(ctx context.Context, content Content) ([]Manifest, error)

	// PushManifest pushes a manifest with a reference.
	PushManifest(ctx context.Context, manifest Manifest, reference string) error
}

// NewArtifact creates a new Artifact from a descriptor.
func NewArtifact(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher, client ManifestClient) *Artifact {
	return &Artifact{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
		client:     client,
	}
}

// Descriptor returns the OCI descriptor for this artifact.
func (a *Artifact) Descriptor() ocispec.Descriptor {
	return a.descriptor
}

// Digest returns the digest of the artifact manifest.
func (a *Artifact) Digest() digest.Digest {
	return a.descriptor.Digest
}

// MediaType returns the media type of the artifact.
func (a *Artifact) MediaType() string {
	return a.descriptor.MediaType
}

// Size returns the size of the artifact manifest in bytes.
func (a *Artifact) Size() int64 {
	return a.descriptor.Size
}

// Annotations returns the annotations associated with this artifact.
func (a *Artifact) Annotations() map[string]string {
	return a.descriptor.Annotations
}

// loadManifest loads the artifact manifest from storage.
func (a *Artifact) loadManifest(ctx context.Context) (*spec.Artifact, error) {
	a.manifestOnce.Do(func() {
		if a.manifest != nil {
			return // Already loaded
		}

		if a.fetcher == nil {
			a.manifestErr = ErrNoFetcher
			return
		}

		// Fetch manifest content
		manifestBytes, err := content.FetchAll(ctx, a.fetcher, a.descriptor)
		if err != nil {
			a.manifestErr = err
			return
		}

		// Parse artifact manifest
		var manifest spec.Artifact
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			a.manifestErr = err
			return
		}

		a.manifest = &manifest
	})

	return a.manifest, a.manifestErr
}

// ArtifactType returns the artifact type (IANA media type).
func (a *Artifact) ArtifactType(ctx context.Context) (string, error) {
	manifest, err := a.loadManifest(ctx)
	if err != nil {
		return "", err
	}
	return manifest.ArtifactType, nil
}

// Blobs returns all blobs referenced by this artifact.
// The blobs are lazily loaded and cached.
func (a *Artifact) Blobs(ctx context.Context) ([]*Blob, error) {
	a.blobsOnce.Do(func() {
		manifest, err := a.loadManifest(ctx)
		if err != nil {
			a.blobsErr = err
			return
		}

		// Convert descriptors to Blob objects
		a.blobs = make([]*Blob, len(manifest.Blobs))
		for i, desc := range manifest.Blobs {
			a.blobs[i] = NewBlob(desc, a.fetcher, a.pusher)
		}
	})

	return a.blobs, a.blobsErr
}

// Subject returns the subject manifest this artifact refers to.
// Returns nil if no subject is set.
func (a *Artifact) Subject() (Manifest, error) {
	a.subjectOnce.Do(func() {
		manifest, err := a.loadManifest(context.Background())
		if err != nil {
			a.subjectErr = err
			return
		}

		if manifest.Subject == nil {
			return // No subject
		}

		if a.client == nil {
			a.subjectErr = ErrNoClient
			return
		}

		// Fetch subject manifest
		a.subject, a.subjectErr = a.client.FetchManifest(context.Background(), *manifest.Subject)
	})

	return a.subject, a.subjectErr
}

// SetSubject sets the subject manifest for this artifact.
func (a *Artifact) SetSubject(subject Manifest) {
	a.subject = subject
	a.subjectOnce = sync.Once{} // Reset once to allow re-evaluation
}

// Predecessors returns all manifests that reference this artifact.
func (a *Artifact) Predecessors(ctx context.Context) ([]Manifest, error) {
	if a.client == nil {
		return nil, ErrNoClient
	}
	return a.client.FindPredecessors(ctx, a)
}

// Push pushes this artifact manifest to the target with the given reference.
func (a *Artifact) Push(ctx context.Context, reference string) error {
	if a.client != nil {
		return a.client.PushManifest(ctx, a, reference)
	}

	// Fallback to direct push if no client
	if a.pusher == nil {
		return ErrNoPusher
	}

	manifestBytes, err := a.MarshalJSON()
	if err != nil {
		return err
	}

	return a.pusher.Push(ctx, a.descriptor, bytes.NewReader(manifestBytes))
}

// MarshalJSON marshals the artifact manifest to JSON.
func (a *Artifact) MarshalJSON() ([]byte, error) {
	manifest, err := a.loadManifest(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(manifest)
}
