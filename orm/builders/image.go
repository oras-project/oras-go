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
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/orm/models"
)

// ImageBuilder provides a fluent API for building image manifests.
type ImageBuilder struct {
	fetcher      content.Fetcher
	pusher       content.Pusher
	client       models.ManifestClient
	config       *models.Blob
	layers       []*models.Blob
	platform     *ocispec.Platform
	subject      models.Manifest
	annotations  map[string]string
	artifactType string
}

// NewImageBuilder creates a new ImageBuilder.
func NewImageBuilder(fetcher content.Fetcher, pusher content.Pusher, client models.ManifestClient) *ImageBuilder {
	return &ImageBuilder{
		fetcher:     fetcher,
		pusher:      pusher,
		client:      client,
		annotations: make(map[string]string),
	}
}

// WithConfig sets the config blob.
func (ib *ImageBuilder) WithConfig(config *models.Blob) *ImageBuilder {
	ib.config = config
	return ib
}

// AddLayer adds a layer blob.
func (ib *ImageBuilder) AddLayer(layer *models.Blob) *ImageBuilder {
	ib.layers = append(ib.layers, layer)
	return ib
}

// WithLayers sets all layers at once.
// The input slice is copied to prevent caller mutation from affecting the builder.
func (ib *ImageBuilder) WithLayers(layers []*models.Blob) *ImageBuilder {
	ib.layers = slices.Clone(layers)
	return ib
}

// WithPlatform sets the platform specification.
func (ib *ImageBuilder) WithPlatform(platform *ocispec.Platform) *ImageBuilder {
	ib.platform = platform
	return ib
}

// WithArtifactType sets the artifact type for the image manifest (OCI 1.1).
// This is useful for typed artifacts like Helm charts, WASM modules, etc.
func (ib *ImageBuilder) WithArtifactType(artifactType string) *ImageBuilder {
	ib.artifactType = artifactType
	return ib
}

// WithSubject sets the subject manifest.
func (ib *ImageBuilder) WithSubject(subject models.Manifest) *ImageBuilder {
	ib.subject = subject
	return ib
}

// WithAnnotation adds an annotation.
func (ib *ImageBuilder) WithAnnotation(key, value string) *ImageBuilder {
	ib.annotations[key] = value
	return ib
}

// WithAnnotations sets multiple annotations.
func (ib *ImageBuilder) WithAnnotations(annotations map[string]string) *ImageBuilder {
	for k, v := range annotations {
		ib.annotations[k] = v
	}
	return ib
}

// Build creates the image manifest.
func (ib *ImageBuilder) Build(ctx context.Context) (*models.Image, error) {
	if ib.config == nil {
		return nil, errors.New("config is required for image manifest")
	}

	// Validate no nil layers.
	for i, layer := range ib.layers {
		if layer == nil {
			return nil, fmt.Errorf("layer at index %d is nil", i)
		}
	}

	// Build layer descriptors
	layerDescs := make([]ocispec.Descriptor, len(ib.layers))
	for i, layer := range ib.layers {
		layerDescs[i] = layer.Descriptor()
	}

	// Build image manifest
	imageManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       ib.config.Descriptor(),
		Layers:       layerDescs,
		Annotations:  ib.annotations,
		ArtifactType: ib.artifactType,
	}

	// Add subject if present
	if ib.subject != nil {
		subjectDesc := ib.subject.Descriptor()
		imageManifest.Subject = &subjectDesc
	}

	// Marshal to JSON
	manifestBytes, err := json.Marshal(imageManifest)
	if err != nil {
		return nil, err
	}

	// Create descriptor
	desc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		Digest:       digest.FromBytes(manifestBytes),
		Size:         int64(len(manifestBytes)),
		Platform:     ib.platform,
		ArtifactType: ib.artifactType,
	}

	// Push manifest to storage
	if ib.pusher != nil {
		if err := ib.pusher.Push(ctx, desc, bytes.NewReader(manifestBytes)); err != nil {
			return nil, err
		}
	}

	// Create and return image model
	image := models.NewImage(desc, ib.fetcher, ib.pusher, ib.client)
	return image, nil
}

// BuildAndPush creates the image and pushes it with a reference.
func (ib *ImageBuilder) BuildAndPush(ctx context.Context, ref string) (*models.Image, error) {
	image, err := ib.Build(ctx)
	if err != nil {
		return nil, err
	}

	if err := image.Push(ctx, ref); err != nil {
		return nil, err
	}

	return image, nil
}
