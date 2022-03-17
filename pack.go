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

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

// MediaTypeUnknownConfig is the default mediaType used when no
// config media type is specified.
const MediaTypeUnknownConfig = "application/vnd.unknown.config.v1+json"

// PackOptions contains parameters for oras.Pack.
type PackOptions struct {
	ConfigDescriptor    *ocispec.Descriptor
	ConfigMediaType     string
	ConfigAnnotations   map[string]string
	ManifestAnnotations map[string]string
}

// Pack packs the given layers, generates a manifest for the pack,
// and pushes it to a content storage. If succeeded, returns a descriptor of the manifest.
func Pack(ctx context.Context, pusher content.Pusher, layers []ocispec.Descriptor, opts PackOptions) (ocispec.Descriptor, error) {
	configMediaType := MediaTypeUnknownConfig
	if opts.ConfigMediaType != "" {
		configMediaType = opts.ConfigMediaType
	}

	var configDesc ocispec.Descriptor
	if opts.ConfigDescriptor != nil {
		configDesc = *opts.ConfigDescriptor
	} else {
		configBytes := []byte("{}")
		configDesc = ocispec.Descriptor{
			MediaType:   configMediaType,
			Digest:      digest.FromBytes(configBytes),
			Size:        int64(len(configBytes)),
			Annotations: opts.ConfigAnnotations,
		}

		// push config.
		if err := pusher.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("failed to push config: %w", err)
		}
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
		Annotations: opts.ManifestAnnotations,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// push manifest.
	if err := pusher.Push(ctx, manifestDesc, bytes.NewReader(manifestBytes)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, fmt.Errorf("failed to push manifest: %w", err)
	}

	return manifestDesc, nil
}
