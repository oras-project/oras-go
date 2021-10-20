package images

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// AppendArtifactsHandler will append artifacts desc to descs
func AppendArtifactsHandler(provider content.Provider) images.Handler {
	return images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		descs := make([]ocispec.Descriptor, 0)

		switch desc.MediaType {

		case artifactspec.MediaTypeArtifactManifest:
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}

			artifact := &manifest_extended{}
			if err := json.Unmarshal(p, artifact); err != nil {
				return nil, err
			}

			for _, desc := range artifact.Blobs {
				descs = append(descs, ocispec.Descriptor{
					MediaType:   desc.MediaType,
					Digest:      desc.Digest,
					Size:        desc.Size,
					URLs:        desc.URLs,
					Annotations: desc.Annotations,
				})
			}
		}

		return descs, nil
	})
}

type manifest_extended struct {
	// MediaType is the media type of this descriptor, this isn't included in the artifactspec, but can appear
	MediaType string `json:"mediaType,omitempty"`

	// ArtifactType is the artifact type of the object this schema refers to.
	ArtifactType string `json:"artifactType"`

	// Blobs is a collection of blobs referenced by this manifest.
	Blobs []ocispec.Descriptor `json:"blobs"`

	// Subject is an optional reference to any existing manifest within the repository.
	// When specified, the artifact is said to be dependent upon the referenced subject.
	Subject ocispec.Descriptor `json:"subject"`

	// Annotations contains arbitrary metadata for the artifact manifest.
	Annotations map[string]string `json:"annotations,omitempty"`
}
