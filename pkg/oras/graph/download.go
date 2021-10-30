package graph

import (
	"context"
	"errors"

	"github.com/containerd/containerd/errdefs"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/target"
)

// DownloadToArtifact is a function that returns a graph walking function that will download each object to the artifact
func DownloadToArtifact(ctx context.Context, subject string, registry target.Target, artifact target.Artifact) oras.GraphWalkFunc {
	return func(parent artifactspec.Descriptor, parentManifest artifactspec.Manifest, objects []target.Object) error {
		parentObject := target.FromArtifactDescriptor(subject, parent.ArtifactType, parent, nil)

		err := parentObject.Download(ctx, registry, artifact)
		if err != nil {
			if !errors.Is(err, errdefs.ErrAlreadyExists) {
				return err
			}
		}

		for _, o := range objects {
			err := o.Download(ctx, registry, artifact)
			if err != nil {
				if !errors.Is(err, errdefs.ErrAlreadyExists) {
					return err
				}
			}
		}

		return nil
	}
}
