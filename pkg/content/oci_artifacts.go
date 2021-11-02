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
package content

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/target"
)

// Discover is a function that emulates the discover function a registtry would host
func (s *OCI) Discover(ctx context.Context, subject ocispec.Descriptor, artifactType string) ([]artifactspec.Descriptor, error) {
	var output []artifactspec.Descriptor

	for k, v := range s.ListReferences() {
		if strings.Contains(k, "_ext/referrers") && strings.HasSuffix(k, subject.Digest.String()) {
			start := strings.Index(k, "referrers/")
			end := strings.Index(k, v.Digest.String())

			parsedArtifactType := k[start+len("referrers/") : end-1]

			if artifactType != "" && artifactType != parsedArtifactType {
				continue
			}

			output = append(output, artifactspec.Descriptor{
				MediaType:    v.MediaType,
				Size:         v.Size,
				Digest:       v.Digest,
				ArtifactType: parsedArtifactType,
			})
		}
	}

	return output, nil
}

// ociArtifactsExtension is an internal type that extends the content.Writer Commit function
type ociArtifactsExtension struct {
	content.Writer
	oci      *OCI
	artifact target.Object
}

// Commit is a function that will add references to the index to support graph discovery
func (a *ociArtifactsExtension) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	err := a.oci.LoadIndex()
	if err != nil {
		return err
	}

	subject := a.artifact.Subject()
	if subject == nil {
		return nil
	}

	subjectref, host, namespace, object, err := subject.ReferenceSpec()
	if err != nil {
		return err
	}

	adesc := a.artifact.ArtifactDescriptor()

	a.oci.AddReference(fmt.Sprintf("%s/%s/_ext/referrers/%s/%s%s", host, namespace, adesc.ArtifactType, adesc.Digest, object), a.artifact.Descriptor())
	a.oci.AddReference(subjectref, subject.Descriptor())
	a.oci.AddReference(fmt.Sprintf("%s/%s@%s", host, namespace, expected), a.artifact.Descriptor())

	err = a.oci.SaveIndex()
	if err != nil {
		return err
	}

	if a.Writer != nil {
		return a.Writer.Commit(ctx, size, expected, opts...)
	}

	return nil
}
