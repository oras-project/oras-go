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

package images

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/artifact"
	orasremotes "oras.land/oras-go/pkg/remotes"
)

// AppendArtifactsHandler is an images.Handler that will recursibly search for artifact-spec descriptors by calling the list referrers api
func AppendArtifactsHandler(ref, artifactType string, provider content.Provider, discoverer orasremotes.Discoverer, filters ...artifact.Filter) images.Handler {
	return images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		descs := make([]ocispec.Descriptor, 0)
		switch desc.MediaType {
		case ocispec.MediaTypeImageManifest:
			// Recursively find all artifacts starting with the image manifest
			refs, err := findMore(ctx, desc, artifactType, discoverer, filters...)
			if err != nil {
				return nil, err
			}
			descs = append(descs, refs...)
		case artifactspec.MediaTypeArtifactManifest:
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}

			artifact := &artifactspec.Manifest{}
			if err := json.Unmarshal(p, artifact); err != nil {
				return nil, err
			}

			appendDesc := func(artifacts ...artifactspec.Descriptor) {
				for _, desc := range artifacts {
					c := ocispec.Descriptor{
						MediaType:   desc.MediaType,
						Digest:      desc.Digest,
						Size:        desc.Size,
						URLs:        desc.URLs,
						Annotations: desc.Annotations,
					}
					descs = append(descs, c)

					more, err := findMore(ctx, c, artifactType, discoverer, filters...)
					if err != nil || len(more) <= 0 {
						continue
					}
					descs = append(descs, more...)
				}
			}

			appendDesc(artifact.Blobs...)
		}
		return descs, nil
	})
}

func findMore(ctx context.Context, desc ocispec.Descriptor, artifactType string, discoverer orasremotes.Discoverer, artifactFilters ...artifact.Filter) ([]ocispec.Descriptor, error) {
	if discoverer == nil {
		return nil, errors.New("discoverer is nil")
	}

	refs, err := discoverer.Discover(ctx, desc, artifactType)
	if err != nil {
		return nil, err
	}

	if len(refs) <= 0 {
		return nil, nil
	}

	out := make([]ocispec.Descriptor, 0)
	for _, r := range refs {
		if test(r, artifactFilters...) {
			adesc := ocispec.Descriptor{
				MediaType:   r.MediaType,
				Size:        r.Size,
				Annotations: r.Annotations,
				URLs:        r.URLs,
				Digest:      r.Digest,
			}
			out = append(out, adesc)

			more, err := findMore(ctx, adesc, artifactType, discoverer, artifactFilters...)
			if err != nil {
				return nil, err
			}

			if more == nil {
				continue
			}

			out = append(out, more...)
		}
	}

	return out, nil
}

func test(t artifactspec.Descriptor, artifactFilter ...artifact.Filter) bool {
	if artifactFilter == nil {
		return true
	}

	for _, af := range artifactFilter {
		if !af(t) {
			return false
		}
	}

	return true
}
