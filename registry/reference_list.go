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

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
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
	ps, err := properties.NewReferenceList(artifact)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	refs := make([]Reference, 0, len(ps))
	for _, p := range ps {
		reference := p.Digest
		if reference == "" {
			reference = p.Tag
		}
		refs = append(refs, Reference{
			Registry:   p.Registry,
			Repository: p.Repository,
			Reference:  reference,
			Tag:        p.Tag,
			Digest:     p.Digest,
		})
	}

	return refs, nil
}
