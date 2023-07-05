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

package manifestutil

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/spec"
)

func Parse(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) (subject, config *ocispec.Descriptor, items []ocispec.Descriptor, err error) {
	switch desc.MediaType {
	case docker.MediaTypeManifest:
		content, err := content.FetchAll(ctx, fetcher, desc)
		if err != nil {
			return nil, nil, nil, err
		}
		// OCI manifest schema can be used to marshal docker manifest
		var manifest ocispec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal %s: %s: %w", desc.Digest.String(), desc.MediaType, err)
		}
		config = &manifest.Config
		items = manifest.Layers
	case ocispec.MediaTypeImageManifest:
		content, err := content.FetchAll(ctx, fetcher, desc)
		if err != nil {
			return nil, nil, nil, err
		}
		var manifest ocispec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal %s: %s: %w", desc.Digest.String(), desc.MediaType, err)
		}
		subject = manifest.Subject
		config = &manifest.Config
		items = manifest.Layers
	case docker.MediaTypeManifestList:
		content, err := content.FetchAll(ctx, fetcher, desc)
		if err != nil {
			return nil, nil, nil, err
		}

		// OCI Index schema can be used to marshal docker manifest list
		var index ocispec.Index
		if err := json.Unmarshal(content, &index); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal %s: %s: %w", desc.Digest.String(), desc.MediaType, err)
		}
		items = index.Manifests
	case ocispec.MediaTypeImageIndex:
		content, err := content.FetchAll(ctx, fetcher, desc)
		if err != nil {
			return nil, nil, nil, err
		}

		var index ocispec.Index
		if err := json.Unmarshal(content, &index); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal %s: %s: %w", desc.Digest.String(), desc.MediaType, err)
		}
		subject = index.Subject
		items = index.Manifests
	case spec.MediaTypeArtifactManifest:
		content, err := content.FetchAll(ctx, fetcher, desc)
		if err != nil {
			return nil, nil, nil, err
		}

		var manifest spec.Artifact
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal %s: %s: %w", desc.Digest.String(), desc.MediaType, err)
		}
		subject = manifest.Subject
		items = manifest.Blobs
	}
	return
}
