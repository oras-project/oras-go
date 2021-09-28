package content

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

func GenerateArtifactsManifest(artifactType string, subject artifactspec.Descriptor, annotations map[string]string, blobs ...ocispec.Descriptor) *artifactspec.Manifest {
	m := &artifactspec.Manifest{}

	m.ArtifactType = artifactType
	m.Blobs = ConvertV1DescriptorsToV2(blobs, artifactType)
	m.Annotations = annotations
	m.Subject = subject

	return m
}

func ConvertV1DescriptorsToV2(descs []ocispec.Descriptor, artifactType string) []artifactspec.Descriptor {
	results := make([]artifactspec.Descriptor, 0, len(descs))
	for _, desc := range descs {
		results = append(results, ConvertV1DescriptorToV2(desc, artifactType))
	}
	return results
}

func ConvertV1DescriptorToV2(desc ocispec.Descriptor, artifactType string) artifactspec.Descriptor {
	return artifactspec.Descriptor{
		ArtifactType: artifactType,
		MediaType:    desc.MediaType,
		Digest:       desc.Digest,
		Size:         desc.Size,
		URLs:         desc.URLs,
		Annotations:  desc.Annotations,
	}
}
