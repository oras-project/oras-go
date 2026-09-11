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

package auth

import (
	"net/http"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// apiPrefix is the path prefix of the distribution API.
const apiPrefix = "/v2/"

// endpointSeparators separate a repository name from the endpoint acting on it.
//
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#endpoints
var endpointSeparators = []string{
	"/manifests/",
	"/blobs/",
	"/tags/list",
	"/referrers/",
}

// requestResource derives the registry resource addressed by req.
// A URL that does not address a repository yields the whole registry.
func requestResource(req *http.Request) properties.Resource {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	return properties.Resource{
		Registry: host,
		Path:     repositoryFromPath(req.URL.Path),
	}
}

// repositoryFromPath returns the repository named by a distribution API path,
// or an empty string if there is none.
func repositoryFromPath(path string) string {
	path, ok := strings.CutPrefix(path, apiPrefix)
	if !ok {
		return ""
	}

	// Split at the rightmost separator, as the repository name may itself
	// contain a segment like "blobs" or "manifests".
	end := -1
	for _, separator := range endpointSeparators {
		if index := strings.LastIndex(path, separator); index > end {
			end = index
		}
	}
	if end <= 0 {
		return ""
	}

	// Degrade to the whole registry rather than name a bogus repository.
	repository := path[:end]
	if err := (properties.Reference{Repository: repository}).ValidateRepository(); err != nil {
		return ""
	}
	return repository
}
