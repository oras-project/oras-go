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

package content

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/spec"
)

// PredecessorFinder finds out the nodes directly pointing to a given node of a
// directed acyclic graph.
// In other words, returns the "parents" of the current descriptor.
// PredecessorFinder is an extension of Storage.
type PredecessorFinder interface {
	// Predecessors returns the nodes directly pointing to the current node.
	Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error)
}

// GraphStorage represents a CAS that supports direct predecessor node finding.
type GraphStorage interface {
	Storage
	PredecessorFinder
}

// ReadOnlyGraphStorage represents a read-only GraphStorage.
type ReadOnlyGraphStorage interface {
	ReadOnlyStorage
	PredecessorFinder
}

// Successors returns the nodes directly pointed by the current node.
// In other words, returns the "children" of the current descriptor.
func Successors(ctx context.Context, fetcher Fetcher, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	switch node.MediaType {
	case docker.MediaTypeManifest:
		_, config, layers, err := SuccessorsParts(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}
		return append([]ocispec.Descriptor{*config}, layers...), nil
	case ocispec.MediaTypeImageManifest:
		subject, config, layers, err := SuccessorsParts(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}
		var nodes []ocispec.Descriptor
		if subject != nil {
			nodes = append(nodes, *subject)
		}
		nodes = append(nodes, *config)
		return append(nodes, layers...), nil
	case docker.MediaTypeManifestList:
		_, _, manifests, err := SuccessorsParts(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}
		return manifests, nil
	case ocispec.MediaTypeImageIndex:
		subject, _, manifests, err := SuccessorsParts(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}
		var nodes []ocispec.Descriptor
		if subject != nil {
			nodes = append(nodes, *subject)
		}
		return append(nodes, manifests...), nil
	case spec.MediaTypeArtifactManifest:
		subject, _, blobs, err := SuccessorsParts(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}
		var nodes []ocispec.Descriptor
		if subject != nil {
			nodes = append(nodes, *subject)
		}
		return append(nodes, blobs...), nil
	}
	return nil, nil
}

func SuccessorsParts(ctx context.Context, fetcher Fetcher, desc ocispec.Descriptor) (subject, config *ocispec.Descriptor, items []ocispec.Descriptor, err error) {
	switch desc.MediaType {
	case docker.MediaTypeManifest:
		content, err := FetchAll(ctx, fetcher, desc)
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
		content, err := FetchAll(ctx, fetcher, desc)
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
		content, err := FetchAll(ctx, fetcher, desc)
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
		content, err := FetchAll(ctx, fetcher, desc)
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
		content, err := FetchAll(ctx, fetcher, desc)
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
