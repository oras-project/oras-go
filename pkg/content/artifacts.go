package content

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// GenerateArtifactsManifest is a function that generates an artifact-spec Manifest
func GenerateArtifactsManifest(artifactType string, subject artifactspec.Descriptor, annotations map[string]string, blobs ...ocispec.Descriptor) *artifactspec.Manifest {
	return &artifactspec.Manifest{
		ArtifactType: artifactType,
		Blobs:        ConvertImageDescriptorsToArtifactDescriptors(blobs, artifactType),
		Annotations:  annotations,
		Subject:      subject,
	}
}

// ConvertImageDescriptorsToArtifactDescriptors is a function that converts a list of image descriptors to an artifact descriptor
// By default pushed artifacts/images to a registry are assigned an image descriptor. An artifact descriptor includes an artifact type field.
func ConvertImageDescriptorsToArtifactDescriptors(descs []ocispec.Descriptor, artifactType string) []artifactspec.Descriptor {
	results := make([]artifactspec.Descriptor, 0, len(descs))
	for _, desc := range descs {
		results = append(results, ConvertImageDescriptorToArtifactDescriptor(desc, artifactType))
	}
	return results
}

// ConvertImageDescriptorToArtifactDescriptor is a function that converts an image descriptors to an artifact descriptor
// By default pushed artifacts/images to a registry are assigned an image descriptor. An artifact descriptor includes an artifact type field.
func ConvertImageDescriptorToArtifactDescriptor(desc ocispec.Descriptor, artifactType string) artifactspec.Descriptor {
	return artifactspec.Descriptor{
		ArtifactType: artifactType,
		MediaType:    desc.MediaType,
		Digest:       desc.Digest,
		Size:         desc.Size,
		URLs:         desc.URLs,
		Annotations:  desc.Annotations,
	}
}
