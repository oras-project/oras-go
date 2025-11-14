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

package registry

import (
	"fmt"
	"strings"

	"github.com/oras-project/oras-go/v3/errdef"
)

// ParseReferenceList parses a string containing a base reference with
// comma-separated tags or digests and returns a slice of references.
//
// The input string can be in one of two formats:
//   - Tag list: "registry/repository:tag1,tag2,tag3"
//   - Digest list: "registry/repository@digest1,digest2,digest3"
//
// Examples:
//   - "localhost:5000/hello:v1,v2,v3"
//   - "localhost:5000/hello@sha256:digest1,sha256:digest2"
//
// All references in the list must be of the same type (either all tags or all digests).
func ParseReferenceList(artifact string) ([]Reference, error) {
	registry, path := splitRegistry(artifact)
	if path == "" {
		return nil, fmt.Errorf("%w: missing registry or repository", errdef.ErrInvalidReference)
	}

	repository, references, isTag := splitRepository(path)

	delimiter := "@"
	if isTag {
		delimiter = ":"
	}
	base := registry + "/" + repository

	// Split the reference part by commas
	refItems := strings.Split(references, ",")
	if len(refItems) == 0 {
		return nil, fmt.Errorf("%w: empty reference list", errdef.ErrInvalidReference)
	}

	// Create a Reference for each item
	refs := make([]Reference, 0, len(refItems))
	for _, item := range refItems {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("%w: empty reference in list", errdef.ErrInvalidReference)
		}

		// Construct the full reference string
		fullRef := base + delimiter + item

		// Parse the reference
		ref, err := ParseReference(fullRef)
		if err != nil {
			return nil, fmt.Errorf("failed to parse reference %q: %w", fullRef, err)
		}

		refs = append(refs, ref)
	}

	return refs, nil
}
