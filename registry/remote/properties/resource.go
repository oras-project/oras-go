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

package properties

import (
	"fmt"
	"strings"

	"github.com/oras-project/oras-go/v3/errdef"
)

// Resource represents a registry host, optionally narrowed to a namespace or
// repository. It never carries a tag or digest.
type Resource struct {
	// Host is the name of the registry, usually a domain name optionally with a
	// port.
	Host string

	// Path is the namespace or repository path, or empty for the whole registry.
	Path string
}

// ParseResource parses a registry resource. The resource may contain an
// optional oci://, http://, or https:// scheme, and may be narrowed to a
// namespace or repository path. Tags and digests are not accepted.
func ParseResource(resource string) (Resource, error) {
	resource = strings.TrimPrefix(resource, "oci://")
	resource = strings.TrimPrefix(resource, "http://")
	resource = strings.TrimPrefix(resource, "https://")
	if strings.HasSuffix(resource, "/") {
		return Resource{}, fmt.Errorf("%w: invalid resource path %q", errdef.ErrInvalidReference, resource)
	}

	host, path := splitRegistry(resource)
	if host == "" {
		return Resource{}, fmt.Errorf("%w: invalid registry resource %q", errdef.ErrInvalidReference, resource)
	}

	// Validate the host independently so a port is not mistaken for a tag.
	if err := (Reference{Registry: host}).ValidateRegistry(); err != nil {
		return Resource{}, err
	}
	if path == "" {
		return Resource{Host: host}, nil
	}
	if strings.ContainsAny(path, ":@") {
		return Resource{}, fmt.Errorf("%w: resource must not include a tag or digest", errdef.ErrInvalidReference)
	}
	if err := (Reference{Repository: path}).ValidateRepository(); err != nil {
		return Resource{}, err
	}
	return Resource{Host: host, Path: path}, nil
}
