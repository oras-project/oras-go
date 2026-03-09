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
	"github.com/oras-project/oras-go/v3/internal/spec"
)

// ManifestClient provides operations for manifest relationships.
type ManifestClient interface {
	// FetchManifest fetches a manifest by descriptor.
	FetchManifest(ctx context.Context, desc ocispec.Descriptor) (Manifest, error)

	// FetchByReference fetches a manifest by reference (tag or digest string).
	FetchByReference(ctx context.Context, reference string) (Manifest, error)

	// FindPredecessors finds all manifests that reference the given content.
	FindPredecessors(ctx context.Context, content Content) ([]Manifest, error)

	// PushManifest pushes a manifest with a reference.
	PushManifest(ctx context.Context, manifest Manifest, reference string) error
}

// Compile-time interface check.
var _ Manifest = (*Artifact)(nil)

// Artifact represents an OCI artifact manifest.
// Artifacts contain typed blobs and can reference a subject manifest.
type Artifact struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher
	client     ManifestClient

	// Lazy-loaded manifest and relationships.
	// Uses lazy[T] for thread-safe loading with retry on transient errors.
	manifest lazy[*spec.Artifact]
	blobs    lazy[[]*Blob]
	subject  lazy[Manifest]
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

// NewArtifactFromManifestBytes creates a new Artifact with a pre-loaded manifest.
// This avoids a redundant network fetch when the manifest bytes are already
// available (e.g., from type detection).
func NewArtifactFromManifestBytes(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher, client ManifestClient, manifestBytes []byte) (*Artifact, error) {
	var manifest spec.Artifact
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, &ObjectsError{Op: "load", Digest: desc.Digest, Err: err}
	}
	a := &Artifact{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
		client:     client,
	}
	a.manifest.set(&manifest)
	return a, nil
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

// Annotations returns a copy of the annotations associated with this artifact.
// If the manifest is already loaded, annotations are read from the manifest
// body (where they are authoritative). Otherwise the descriptor annotations
// are used as a fallback. The returned map is safe to modify.
func (a *Artifact) Annotations() map[string]string {
	if m, ok := a.manifest.peek(); ok {
		return maps.Clone(m.Annotations)
	}
	return maps.Clone(a.descriptor.Annotations)
}

// loadManifest loads the artifact manifest from storage.
func (a *Artifact) loadManifest(ctx context.Context) (*spec.Artifact, error) {
	return a.manifest.get(func() (*spec.Artifact, error) {
		if a.fetcher == nil {
			return nil, &ObjectsError{Op: "load", Digest: a.descriptor.Digest, Err: ErrNoFetcher}
		}

		manifestBytes, err := content.FetchAll(ctx, a.fetcher, a.descriptor)
		if err != nil {
			return nil, &ObjectsError{Op: "load", Digest: a.descriptor.Digest, Err: err}
		}

		var manifest spec.Artifact
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return nil, &ObjectsError{Op: "load", Digest: a.descriptor.Digest, Err: err}
		}

		return &manifest, nil
	})
}

// Load eagerly loads the artifact manifest from storage.
func (a *Artifact) Load(ctx context.Context) error {
	_, err := a.loadManifest(ctx)
	return err
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
	return a.blobs.get(func() ([]*Blob, error) {
		manifest, err := a.loadManifest(ctx)
		if err != nil {
			return nil, err // already wrapped by loadManifest
		}

		blobs := make([]*Blob, len(manifest.Blobs))
		for i, desc := range manifest.Blobs {
			blobs[i] = NewBlob(desc, a.fetcher, a.pusher)
		}
		return blobs, nil
	})
}

// Subject returns the subject manifest this artifact refers to.
// Returns nil if no subject is set.
func (a *Artifact) Subject(ctx context.Context) (Manifest, error) {
	return a.subject.get(func() (Manifest, error) {
		manifest, err := a.loadManifest(ctx)
		if err != nil {
			return nil, err // already wrapped by loadManifest
		}

		if manifest.Subject == nil {
			return nil, nil
		}

		if a.client == nil {
			return nil, &ObjectsError{Op: "fetch_subject", Digest: a.descriptor.Digest, Err: ErrNoClient}
		}

		subj, err := a.client.FetchManifest(ctx, *manifest.Subject)
		if err != nil {
			return nil, &ObjectsError{Op: "fetch_subject", Digest: a.descriptor.Digest, Err: err}
		}
		return subj, nil
	})
}

// SetSubject sets the subject manifest for this artifact.
func (a *Artifact) SetSubject(subject Manifest) {
	a.subject.set(subject)
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

	if a.pusher == nil {
		return ErrNoPusher
	}

	manifest, err := a.loadManifest(ctx)
	if err != nil {
		return err
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	return a.pusher.Push(ctx, a.descriptor, bytes.NewReader(manifestBytes))
}

// MarshalJSON marshals the artifact manifest to JSON.
// The manifest must have been loaded first via Load(ctx) or any method
// that accepts a context. Returns ErrNotLoaded if not yet loaded.
func (a *Artifact) MarshalJSON() ([]byte, error) {
	manifest, ok := a.manifest.peek()
	if !ok {
		return nil, ErrNotLoaded
	}
	return json.Marshal(manifest)
}
