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

package builders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/orm/models"
)

// ArtifactBuilder provides a fluent API for building artifact manifests.
type ArtifactBuilder struct {
	fetcher      content.Fetcher
	pusher       content.Pusher
	client       models.ManifestClient
	artifactType string
	blobs        []*models.Blob
	subject      models.Manifest
	annotations  map[string]string
}

// NewArtifactBuilder creates a new ArtifactBuilder.
func NewArtifactBuilder(artifactType string, fetcher content.Fetcher, pusher content.Pusher, client models.ManifestClient) *ArtifactBuilder {
	return &ArtifactBuilder{
		fetcher:      fetcher,
		pusher:       pusher,
		client:       client,
		artifactType: artifactType,
		annotations:  make(map[string]string),
	}
}

// AddBlob adds a blob to the artifact.
func (ab *ArtifactBuilder) AddBlob(blob *models.Blob) *ArtifactBuilder {
	ab.blobs = append(ab.blobs, blob)
	return ab
}

// WithBlobs sets all blobs at once.
// The input slice is copied to prevent caller mutation from affecting the builder.
func (ab *ArtifactBuilder) WithBlobs(blobs []*models.Blob) *ArtifactBuilder {
	ab.blobs = slices.Clone(blobs)
	return ab
}

// WithSubject sets the subject manifest.
func (ab *ArtifactBuilder) WithSubject(subject models.Manifest) *ArtifactBuilder {
	ab.subject = subject
	return ab
}

// WithAnnotation adds an annotation.
func (ab *ArtifactBuilder) WithAnnotation(key, value string) *ArtifactBuilder {
	ab.annotations[key] = value
	return ab
}

// WithAnnotations sets multiple annotations.
func (ab *ArtifactBuilder) WithAnnotations(annotations map[string]string) *ArtifactBuilder {
	for k, v := range annotations {
		ab.annotations[k] = v
	}
	return ab
}

// Build creates the artifact manifest.
func (ab *ArtifactBuilder) Build(ctx context.Context) (*models.Artifact, error) {
	if ab.artifactType == "" {
		return nil, errors.New("artifactType is required for artifact manifest")
	}

	// Validate no nil blobs.
	for i, blob := range ab.blobs {
		if blob == nil {
			return nil, fmt.Errorf("blob at index %d is nil", i)
		}
	}

	// Build blob descriptors
	blobDescs := make([]ocispec.Descriptor, len(ab.blobs))
	for i, blob := range ab.blobs {
		blobDescs[i] = blob.Descriptor()
	}

	// Build artifact manifest
	artifactManifest := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		ArtifactType: ab.artifactType,
		Blobs:        blobDescs,
		Annotations:  ab.annotations,
	}

	// Add subject if present
	if ab.subject != nil {
		subjectDesc := ab.subject.Descriptor()
		artifactManifest.Subject = &subjectDesc
	}

	// Marshal to JSON
	manifestBytes, err := json.Marshal(artifactManifest)
	if err != nil {
		return nil, err
	}

	// Create descriptor
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// Push manifest to storage
	if ab.pusher != nil {
		if err := ab.pusher.Push(ctx, desc, bytes.NewReader(manifestBytes)); err != nil {
			return nil, err
		}
	}

	// Create and return artifact model
	artifact := models.NewArtifact(desc, ab.fetcher, ab.pusher, ab.client)
	return artifact, nil
}

// BuildAndPush creates the artifact and pushes it with a reference.
func (ab *ArtifactBuilder) BuildAndPush(ctx context.Context, ref string) (*models.Artifact, error) {
	artifact, err := ab.Build(ctx)
	if err != nil {
		return nil, err
	}

	if err := artifact.Push(ctx, ref); err != nil {
		return nil, err
	}

	return artifact, nil
}
