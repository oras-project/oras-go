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
package distribution

import (
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/docker"
)

// manifestMediaTypes contains the media type of manifests.
var manifestMediaTypes = []string{
	docker.MediaTypeManifest,
	docker.MediaTypeManifestList,
	ocispec.MediaTypeImageManifest,
	ocispec.MediaTypeImageIndex,
	artifactspec.MediaTypeArtifactManifest,
}

// manifestAcceptHeader is set in the `Accept` header for resolving the
// manifest by the tag.
var manifestAcceptHeader = strings.Join(manifestMediaTypes, ", ") + ", */*"

// isManifest determines if the given descriptor points to a manifest.
func isManifest(desc ocispec.Descriptor) bool {
	for _, mediaType := range manifestMediaTypes {
		if desc.MediaType == mediaType {
			return true
		}
	}
	return false
}
