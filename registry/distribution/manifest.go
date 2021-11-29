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
