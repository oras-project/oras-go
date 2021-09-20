package content

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

func GenerateArtifactsManifest(artifactType string, subject artifactspec.Descriptor, annotations map[string]string, blobs []ocispec.Descriptor) *artifactspec.Manifest {
	m := &artifactspec.Manifest{}

	m.ArtifactType = artifactType
	m.Blobs = convertV1DescriptorsToV2(blobs)
	m.Annotations = annotations
	m.Subject = subject

	return m
}

func convertV1DescriptorsToV2(descs []ocispec.Descriptor) []artifactspec.Descriptor {
	results := make([]artifactspec.Descriptor, 0, len(descs))
	for _, desc := range descs {
		results = append(results, convertV1DescriptorToV2(desc))
	}
	return results
}

func convertV1DescriptorToV2(desc ocispec.Descriptor) artifactspec.Descriptor {
	return artifactspec.Descriptor{
		MediaType:   desc.MediaType,
		Digest:      desc.Digest,
		Size:        desc.Size,
		URLs:        desc.URLs,
		Annotations: desc.Annotations,
	}
}
