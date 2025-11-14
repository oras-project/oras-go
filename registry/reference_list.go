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

	"oras.land/oras-go/v2/errdef"
)

// NewReferenceList parses a string containing a base reference with
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
func NewReferenceList(s string) ([]Reference, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty reference list string", errdef.ErrInvalidReference)
	}

	// Find the delimiter (: for tags, @ for digests)
	var base string
	var delimiter string
	var refPart string

	// Check for digest delimiter first (@ appears after repository)
	if idx := strings.Index(s, "@"); idx != -1 {
		base = s[:idx]
		delimiter = "@"
		refPart = s[idx+1:]
	} else if idx := strings.LastIndex(s, ":"); idx != -1 {
		// For tags, we need to find the last colon to handle registry ports
		// e.g., "localhost:5000/hello:v1,v2,v3"
		// We need to distinguish between registry port and tag delimiter

		// Check if there's a slash after this colon - if not, it might be a port
		slashIdx := strings.Index(s, "/")
		if slashIdx == -1 || idx < slashIdx {
			return nil, fmt.Errorf("%w: missing repository in reference list", errdef.ErrInvalidReference)
		}

		// Find the tag delimiter (the colon after the last slash)
		pathPart := s[slashIdx:]
		tagColonIdx := strings.Index(pathPart, ":")
		if tagColonIdx == -1 {
			return nil, fmt.Errorf("%w: missing tag or digest in reference list", errdef.ErrInvalidReference)
		}

		idx = slashIdx + tagColonIdx
		base = s[:idx]
		delimiter = ":"
		refPart = s[idx+1:]
	} else {
		return nil, fmt.Errorf("%w: missing tag or digest delimiter in reference list", errdef.ErrInvalidReference)
	}

	// Split the reference part by commas
	refItems := strings.Split(refPart, ",")
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
