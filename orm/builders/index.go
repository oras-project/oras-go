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

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/orm/models"
)

// IndexBuilder provides a fluent API for building image indexes (manifest lists).
type IndexBuilder struct {
	fetcher     content.Fetcher
	pusher      content.Pusher
	client      models.ManifestClient
	manifests   []models.Manifest
	subject     models.Manifest
	annotations map[string]string
}

// NewIndexBuilder creates a new IndexBuilder.
func NewIndexBuilder(fetcher content.Fetcher, pusher content.Pusher, client models.ManifestClient) *IndexBuilder {
	return &IndexBuilder{
		fetcher:     fetcher,
		pusher:      pusher,
		client:      client,
		annotations: make(map[string]string),
	}
}

// AddManifest adds a manifest to the index.
func (ib *IndexBuilder) AddManifest(manifest models.Manifest) *IndexBuilder {
	ib.manifests = append(ib.manifests, manifest)
	return ib
}

// WithManifests sets all manifests at once.
func (ib *IndexBuilder) WithManifests(manifests []models.Manifest) *IndexBuilder {
	ib.manifests = manifests
	return ib
}

// WithSubject sets the subject manifest.
func (ib *IndexBuilder) WithSubject(subject models.Manifest) *IndexBuilder {
	ib.subject = subject
	return ib
}

// WithAnnotation adds an annotation.
func (ib *IndexBuilder) WithAnnotation(key, value string) *IndexBuilder {
	ib.annotations[key] = value
	return ib
}

// WithAnnotations sets multiple annotations.
func (ib *IndexBuilder) WithAnnotations(annotations map[string]string) *IndexBuilder {
	for k, v := range annotations {
		ib.annotations[k] = v
	}
	return ib
}

// Build creates the image index.
func (ib *IndexBuilder) Build(ctx context.Context) (*models.Index, error) {
	if len(ib.manifests) == 0 {
		return nil, errors.New("at least one manifest is required for index")
	}

	// Build manifest descriptors
	manifestDescs := make([]ocispec.Descriptor, len(ib.manifests))
	for i, manifest := range ib.manifests {
		manifestDescs[i] = manifest.Descriptor()
	}

	// Build index
	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType:   ocispec.MediaTypeImageIndex,
		Manifests:   manifestDescs,
		Annotations: ib.annotations,
	}

	// Add subject if present
	if ib.subject != nil {
		subjectDesc := ib.subject.Descriptor()
		index.Subject = &subjectDesc
	}

	// Marshal to JSON
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}

	// Create descriptor
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(indexBytes),
		Size:      int64(len(indexBytes)),
	}

	// Push index to storage
	if ib.pusher != nil {
		if err := ib.pusher.Push(ctx, desc, bytes.NewReader(indexBytes)); err != nil {
			return nil, err
		}
	}

	// Create and return index model
	idx := models.NewIndex(desc, ib.fetcher, ib.pusher, ib.client)
	return idx, nil
}

// BuildAndPush creates the index and pushes it with a reference.
func (ib *IndexBuilder) BuildAndPush(ctx context.Context, ref string) (*models.Index, error) {
	idx, err := ib.Build(ctx)
	if err != nil {
		return nil, err
	}

	if err := idx.Push(ctx, ref); err != nil {
		return nil, err
	}

	return idx, nil
}
