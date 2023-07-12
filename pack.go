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

package oras

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/spec"
)

const (
	// MediaTypeUnknownConfig is the default mediaType used when no
	// config media type is specified.
	MediaTypeUnknownConfig = "application/vnd.unknown.config.v1+json"

	// MediaTypeUnknownArtifact is the default artifactType used when no
	// artifact type is specified.
	MediaTypeUnknownArtifact = "application/vnd.unknown.artifact.v1"
)

// PackManifestType represents the manifest type used for [Pack].
type PackManifestType = int32

const (
	// PackManifestTypeImageManifestLegacy represents the OCI Image Manifest
	// type defined in image-spec v1.1.0-rc2.
	// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc2/manifest.md
	PackManifestTypeImageManifestLegacy PackManifestType = iota

	// PackManifestTypeImageManifest represents the OCI Image Manifest type
	// defined since image-spec v1.1.0-rc4.
	// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc4/manifest.md
	PackManifestTypeImageManifest
)

// ErrInvalidDateTimeFormat is returned by [Pack] when
// AnnotationArtifactCreated or AnnotationCreated is provided, but its value
// is not in RFC 3339 format.
// Reference: https://www.rfc-editor.org/rfc/rfc3339#section-5.6
var ErrInvalidDateTimeFormat = errors.New("invalid date and time format")

// PackOptions contains parameters for [Pack].
type PackOptions struct {
	// Subject is the subject of the manifest.
	Subject *ocispec.Descriptor

	// ManifestAnnotations is the annotation map of the manifest.
	ManifestAnnotations map[string]string

	// PackImageManifest controls whether to pack an image manifest or not.
	//   - If true, pack an OCI Image Manifest
	//   - If false, pack an OCI Artifact Manifest
	// Default: false.
	PackImageManifest bool

	// PackManifestType controls which type of manifest to pack.
	// This option is valid only when PackImageManifest is true.
	// Default: PackManifestTypeImageManifestLegacy.
	PackManifestType

	// ConfigDescriptor is a pointer to the descriptor of the config blob.
	// If not nil, artifactType will be implied by the mediaType of the
	// specified ConfigDescriptor, and ConfigAnnotations will be ignored.
	// This option is valid only when PackImageManifest is true.
	ConfigDescriptor *ocispec.Descriptor

	// ConfigAnnotations is the annotation map of the config descriptor.
	// This option is valid only when PackImageManifest is true
	// and ConfigDescriptor is nil.
	ConfigAnnotations map[string]string
}

// DefaultPackOptions provides the default PackOptions.
var DefaultPackOptions PackOptions = PackOptions{
	PackImageManifest: true,
	PackManifestType:  PackManifestTypeImageManifest,
}

// Pack packs the given blobs, generates a manifest for the pack,
// and pushes it to a content storage.
//
// When opts.PackImageManifest is true and opts.PackManifestType is
// PackManifestTypeImageManifestLegacy, artifactType will be used as the
// the config descriptor mediaType of the image manifest.
//
// If succeeded, returns a descriptor of the manifest.
func Pack(ctx context.Context, pusher content.Pusher, artifactType string, blobs []ocispec.Descriptor, opts PackOptions) (ocispec.Descriptor, error) {
	if !opts.PackImageManifest {
		return packArtifact(ctx, pusher, artifactType, blobs, opts)
	}

	switch opts.PackManifestType {
	case PackManifestTypeImageManifestLegacy:
		return packImageLegacy(ctx, pusher, artifactType, blobs, opts)
	case PackManifestTypeImageManifest:
		return packImage(ctx, pusher, artifactType, blobs, opts)
	default:
		return ocispec.Descriptor{}, fmt.Errorf("PackManifestType %v is unsupported: %w", opts.PackManifestType, errdef.ErrUnsupported)
	}
}

// packArtifact packs the given blobs, generates an artifact manifest for the
// pack, and pushes it to a content storage.
// If succeeded, returns a descriptor of the manifest.
func packArtifact(ctx context.Context, pusher content.Pusher, artifactType string, blobs []ocispec.Descriptor, opts PackOptions) (ocispec.Descriptor, error) {
	if artifactType == "" {
		artifactType = MediaTypeUnknownArtifact
	}

	annotations, err := ensureAnnotationCreated(opts.ManifestAnnotations, spec.AnnotationArtifactCreated)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	manifest := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		ArtifactType: artifactType,
		Blobs:        blobs,
		Subject:      opts.Subject,
		Annotations:  annotations,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, manifestJSON)
	// populate ArtifactType and Annotations of the manifest into manifestDesc
	manifestDesc.ArtifactType = manifest.ArtifactType
	manifestDesc.Annotations = manifest.Annotations

	// push manifest
	if err := pusher.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, fmt.Errorf("failed to push manifest: %w", err)
	}

	return manifestDesc, nil
}

// packImageLegacy packs the given blobs, generates an image manifest for the
// pack, and pushes it to a content storage.
// artifactType will be used as the config descriptor mediaType of the image
// manifest.
// If succeeded, returns a descriptor of the manifest.
func packImageLegacy(ctx context.Context, pusher content.Pusher, configMediaType string, layers []ocispec.Descriptor, opts PackOptions) (ocispec.Descriptor, error) {
	if configMediaType == "" {
		configMediaType = MediaTypeUnknownConfig
	}

	var configDesc ocispec.Descriptor
	if opts.ConfigDescriptor != nil {
		configDesc = *opts.ConfigDescriptor
	} else {
		// Use an empty JSON object here, because some registries may not accept
		// empty config blob.
		// As of September 2022, GAR is known to return 400 on empty blob upload.
		// See https://github.com/oras-project/oras-go/issues/294 for details.
		configBytes := []byte("{}")
		configDesc = content.NewDescriptorFromBytes(configMediaType, configBytes)
		configDesc.Annotations = opts.ConfigAnnotations
		// push config
		if err := pusher.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("failed to push config: %w", err)
		}
	}

	annotations, err := ensureAnnotationCreated(opts.ManifestAnnotations, ocispec.AnnotationCreated)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if layers == nil {
		layers = []ocispec.Descriptor{} // make it an empty array to prevent potential server-side bugs
	}
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config:      configDesc,
		MediaType:   ocispec.MediaTypeImageManifest,
		Layers:      layers,
		Subject:     opts.Subject,
		Annotations: annotations,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestJSON)
	// populate ArtifactType and Annotations of the manifest into manifestDesc
	manifestDesc.ArtifactType = manifest.Config.MediaType
	manifestDesc.Annotations = manifest.Annotations

	// push manifest
	if err := pusher.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, fmt.Errorf("failed to push manifest: %w", err)
	}

	return manifestDesc, nil
}

// packImage packs the given blobs, generates an image manifest for the pack,
// and pushes it to a content storage.
// If succeeded, returns a descriptor of the manifest.
// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc4/manifest.md#guidelines-for-artifact-usage
func packImage(ctx context.Context, pusher content.Pusher, artifactType string, layers []ocispec.Descriptor, opts PackOptions) (ocispec.Descriptor, error) {
	var configDesc ocispec.Descriptor
	if opts.ConfigDescriptor != nil {
		configDesc = *opts.ConfigDescriptor
	} else {
		// use the empty descriptor for config
		configDesc = ocispec.DescriptorEmptyJSON
		configDesc.Annotations = opts.ConfigAnnotations
		configData := ocispec.DescriptorEmptyJSON.Data
		if err := pusher.Push(ctx, configDesc, bytes.NewReader(configData)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("failed to push config: %w", err)
		}
	}
	if artifactType == "" {
		// artifactType MUST be set when config.mediaType is set to the empty value
		artifactType = configDesc.MediaType
		if configDesc.MediaType == ocispec.MediaTypeEmptyJSON {
			artifactType = MediaTypeUnknownArtifact
		}
	}

	annotations, err := ensureAnnotationCreated(opts.ManifestAnnotations, ocispec.AnnotationCreated)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(layers) == 0 {
		// use the empty descriptor as the single layer
		layerDesc := ocispec.DescriptorEmptyJSON
		layerData := ocispec.DescriptorEmptyJSON.Data
		if err := pusher.Push(ctx, layerDesc, bytes.NewReader(layerData)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("failed to push empty layer: %w", err)
		}
		layers = []ocispec.Descriptor{layerDesc}
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config:       configDesc,
		MediaType:    ocispec.MediaTypeImageManifest,
		Layers:       layers,
		Subject:      opts.Subject,
		ArtifactType: artifactType,
		Annotations:  annotations,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestJSON)
	// populate ArtifactType and Annotations of the manifest into manifestDesc
	manifestDesc.ArtifactType = manifest.ArtifactType
	manifestDesc.Annotations = manifest.Annotations
	// push manifest
	if err := pusher.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, fmt.Errorf("failed to push manifest: %w", err)
	}
	return manifestDesc, nil
}

// ensureAnnotationCreated ensures that annotationCreatedKey is in annotations,
// and that its value conforms to RFC 3339. Otherwise returns a new annotation
// map with annotationCreatedKey created.
func ensureAnnotationCreated(annotations map[string]string, annotationCreatedKey string) (map[string]string, error) {
	if createdTime, ok := annotations[annotationCreatedKey]; ok {
		// if annotationCreatedKey is provided, validate its format
		if _, err := time.Parse(time.RFC3339, createdTime); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDateTimeFormat, err)
		}
		return annotations, nil
	}

	// copy the original annotation map
	copied := make(map[string]string, len(annotations)+1)
	for k, v := range annotations {
		copied[k] = v
	}
	// set creation time in RFC 3339 format
	// reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc2/annotations.md#pre-defined-annotation-keys
	now := time.Now().UTC()
	copied[annotationCreatedKey] = now.Format(time.RFC3339)
	return copied, nil
}
