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

package descriptor

import (
	"errors"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var ErrMissingMediaType error = errors.New("missing media type")

// GenerateDescriptor returns a descriptor, given the content and media type.
func GenerateDescriptor(content []byte, mediaType string) (ocispec.Descriptor, error) {
	if mediaType == "" {
		return ocispec.Descriptor{}, ErrMissingMediaType
	}
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}, nil
}

// Equal returns true if two descriptors point to the same content.
func Equal(a, b ocispec.Descriptor) bool {
	return a.Digest == b.Digest && a.Size == b.Size && a.MediaType == b.MediaType
}
