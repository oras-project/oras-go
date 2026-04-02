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
	"context"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Content represents any OCI content that can be stored and retrieved.
// This is the base interface for all ORM models (Blob, Artifact, Image, Index).
type Content interface {
	// Descriptor returns the OCI descriptor for this content.
	Descriptor() ocispec.Descriptor

	// Digest returns the digest of the content.
	Digest() digest.Digest

	// MediaType returns the media type of the content.
	MediaType() string

	// Size returns the size of the content in bytes.
	Size() int64

	// Annotations returns the annotations associated with this content.
	Annotations() map[string]string
}

// Manifest represents content that is a manifest (Artifact, Image, or Index).
// Manifests can reference other content and have subjects.
type Manifest interface {
	Content

	// Load eagerly loads the manifest data from storage.
	// This must be called before MarshalJSON if the manifest was created
	// from a descriptor (lazy loading).
	Load(ctx context.Context) error

	// Subject returns the subject (parent) manifest this manifest refers to.
	// Returns nil if no subject is set.
	Subject(ctx context.Context) (Manifest, error)

	// SetSubject sets the subject (parent) manifest for this manifest.
	SetSubject(subject Manifest)

	// Predecessors returns all manifests that reference this manifest.
	// This is useful for finding referrers (signatures, SBOMs, etc.).
	Predecessors(ctx context.Context) ([]Manifest, error)

	// Push pushes this manifest to the target with the given reference.
	Push(ctx context.Context, reference string) error
}
