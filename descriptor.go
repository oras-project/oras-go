package oras

import (
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// GenerateDescriptor returns an OCI descriptor, given the content and media type.
func GenerateDescriptor(content []byte, mediaType string) (ocispec.Descriptor, error) {
	if mediaType == "" {
		return ocispec.Descriptor{}, fmt.Errorf("missing media type")
	}
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}, nil
}

// Equal returns true if two OCI descriptors point to the same content.
func Equal(a, b ocispec.Descriptor) bool {
	return a.Digest == b.Digest && a.Size == b.Size && a.MediaType == b.MediaType
}
