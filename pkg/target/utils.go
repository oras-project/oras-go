package target

import (
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// FromOCIDescriptor is a function that returns an Object from an oci spec descriptor
func FromOCIDescriptor(reference string, desc ocispec.Descriptor, artifactType string, subject *Object) Object {
	return Object{
		digest:       desc.Digest,
		size:         desc.Size,
		mediaType:    desc.MediaType,
		annotations:  desc.Annotations,
		artifactType: artifactType,
		subject:      subject,
		reference:    reference,
	}
}

// FromArtifactDescriptor is a function that returns an object from an artifact spec descriptor
func FromArtifactDescriptor(reference string, artifactType string, desc artifactspec.Descriptor, subject *Object) Object {
	return Object{
		reference:    reference,
		digest:       desc.Digest,
		size:         desc.Size,
		mediaType:    desc.MediaType,
		annotations:  desc.Annotations,
		artifactType: artifactType,
		subject:      subject,
	}
}

// FromArtifactManifest is a function that returns an array of Objects parsed from an artifact spec manifest
func FromArtifactManifest(reference string, manifestDesc artifactspec.Descriptor, artifacts artifactspec.Manifest) ([]Object, error) {
	objects := make([]Object, len(artifacts.Blobs)+1)

	subject := FromArtifactDescriptor(reference, "", artifacts.Subject, nil)

	_, host, namespace, _, err := parse(reference)
	if err != nil {
		return nil, err
	}

	ref := fmt.Sprintf("%s/%s@%s", host, namespace, manifestDesc.Digest)

	root := FromArtifactDescriptor(ref, artifacts.ArtifactType, manifestDesc, &subject)
	objects[0] = root

	for i, b := range artifacts.Blobs {
		objects[i+1] = FromArtifactDescriptor(fmt.Sprintf("%s/%s@%s", host, namespace, b.Digest), artifacts.ArtifactType, b, &objects[0])
	}

	return objects, nil
}
