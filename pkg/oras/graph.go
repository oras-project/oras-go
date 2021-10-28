package oras

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/target"
)

type GraphWalkFunc func(artifactspec.Descriptor, artifactspec.Manifest, []target.Object) error

// Graph is a function that resolves the immediate children of the subject from a source
// The first element of what is returned will always be the subject descriptor
func Graph(ctx context.Context, subject string, artifactType string, source target.Target, walkfn GraphWalkFunc) ([]target.Object, error) {
	desc, discovered, err := Discover(ctx, source, subject, artifactType)
	if err != nil {
		return nil, err
	}

	fetcher, err := source.Fetcher(ctx, subject)
	if err != nil {
		return nil, err
	}

	var output []target.Object

	// the first element will always be the root
	output = append(output, target.FromOCIDescriptor(subject, desc, "", nil))

	for _, m := range discovered {
		var artifactManifest artifactspec.Manifest

		reader, err := fetcher.Fetch(ctx, ocispec.Descriptor{
			Digest:      m.Digest,
			Size:        m.Size,
			Annotations: m.Annotations,
			MediaType:   m.MediaType,
		})
		if err != nil {
			return nil, err
		}
		defer reader.Close()

		err = json.NewDecoder(reader).Decode(&artifactManifest)
		if err != nil {
			return nil, err
		}

		objects, err := target.FromArtifactManifest(subject, artifactspec.Descriptor{
			Digest:       m.Digest,
			Size:         m.Size,
			Annotations:  m.Annotations,
			MediaType:    m.MediaType,
			ArtifactType: artifactManifest.ArtifactType,
		}, artifactManifest)
		if err != nil {
			return nil, err
		}

		// The first element is always going to be the subject
		output = append(output, objects[1:]...)

		if walkfn != nil {
			err := walkfn(artifactspec.Descriptor{
				ArtifactType: artifactManifest.ArtifactType,
				Digest:       m.Digest,
				Size:         m.Size,
				Annotations:  m.Annotations,
				MediaType:    m.MediaType}, artifactManifest, objects)
			if err != nil {
				return nil, err
			}
		}

		_, host, namespace, _, err := objects[0].ReferenceSpec()
		if err != nil {
			return nil, err
		}

		additional, err := Graph(ctx, fmt.Sprintf("%s/%s@%s", host, namespace, m.Digest), artifactType, source, walkfn)
		if err != nil {
			return nil, err
		}

		if len(additional) > 1 {
			output = append(output, additional[1:]...)
		}
	}

	return output, nil
}
