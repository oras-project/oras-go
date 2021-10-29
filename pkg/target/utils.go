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

// FromImageManifest is a function that returns an array of Objects parsed from an image style manifest
func FromImageManifest(reference, artifactType string, desc ocispec.Descriptor, manifest struct {
	Version   int                  `json:"schemaVersion"`
	MediaType string               `json:"mediaType"`
	Config    ocispec.Descriptor   `json:"config"`
	Layers    []ocispec.Descriptor `json:"layers"`
}) ([]Object, error) {
	objects := make([]Object, len(manifest.Layers)+2)

	// Manifest
	objects[0] = FromOCIDescriptor(reference, desc, artifactType, nil)
	// Config
	objects[1] = FromOCIDescriptor(reference, manifest.Config, "image/config", nil)

	// Layers
	for i, l := range manifest.Layers {
		objects[i+2] = FromOCIDescriptor(reference, l, "image/layer", nil)
	}

	return objects, nil
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
