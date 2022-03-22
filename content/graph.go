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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/docker"
)

// UpEdgeFinder finds out the parent nodes of a given node of a directed acyclic
// graph.
// In other words, returns the "parents" of the current descriptor.
// UpEdgeFinder is an extension of Storage.
type UpEdgeFinder interface {
	// UpEdges returns the nodes directly pointing to the current node.
	UpEdges(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error)
}

// DownEdges returns the nodes directly pointed by the current node.
// In other words, returns the "children" of the current descriptor.
func DownEdges(ctx context.Context, fetcher Fetcher, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	switch node.MediaType {
	case docker.MediaTypeManifest, ocispec.MediaTypeImageManifest:
		content, err := FetchAll(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}

		// docker manifest and oci manifest are equivalent for down edges.
		var manifest ocispec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, err
		}
		return append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...), nil
	case docker.MediaTypeManifestList, ocispec.MediaTypeImageIndex:
		content, err := FetchAll(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}

		// docker manifest list and oci index are equivalent for down edges.
		var index ocispec.Index
		if err := json.Unmarshal(content, &index); err != nil {
			return nil, err
		}
		return index.Manifests, nil
	case artifactspec.MediaTypeArtifactManifest:
		content, err := FetchAll(ctx, fetcher, node)
		if err != nil {
			return nil, err
		}

		var manifest artifactspec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, err
		}
		var nodes []ocispec.Descriptor
		if descriptor.FromArtifact(manifest.Subject) != descriptor.Empty {
			nodes = append(nodes, descriptor.ArtifactToOCI(manifest.Subject))
		}
		for _, blob := range manifest.Blobs {
			nodes = append(nodes, descriptor.ArtifactToOCI(blob))
		}
		return nodes, nil
	}
	return nil, nil
}
