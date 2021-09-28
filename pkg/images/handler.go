package images

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/artifact"
	orasremotes "oras.land/oras-go/pkg/remotes"
)

// AppendArtifactsHandler will append artifacts desc to descs
func AppendArtifactsHandler(ref, artifactType string, provider content.Provider, discoverer orasremotes.Discoverer, filters ...artifact.Filter) images.Handler {
	return images.HandlerFunc(func(ctx context.Context, desc v1.Descriptor) ([]v1.Descriptor, error) {
		descs := make([]v1.Descriptor, 0)
		switch desc.MediaType {
		case v1.MediaTypeImageManifest:
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
					c := v1.Descriptor{
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

func findMore(ctx context.Context, desc v1.Descriptor, artifactType string, discoverer orasremotes.Discoverer, artifactFilter ...artifact.Filter) ([]v1.Descriptor, error) {
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

	out := make([]v1.Descriptor, 0)
	for _, r := range refs {
		if test(r, artifactFilter...) {
			adesc := v1.Descriptor{
				MediaType:   r.MediaType,
				Size:        r.Size,
				Annotations: r.Annotations,
				URLs:        r.URLs,
				Digest:      r.Digest,
			}
			out = append(out, adesc)

			more, err := findMore(ctx, adesc, artifactType, discoverer, artifactFilter...)
			if err != nil || more == nil {
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
