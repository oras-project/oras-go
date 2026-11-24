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

package remote

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/internal/urlutil"
)

// originScopedTransport keeps registry-scoped credentials on the registry's
// own origin. Requests to that origin use registry, which may present a TLS
// client certificate. Requests to any other origin, such as a redirect to a
// blob storage backend or a token realm on another host, use other, which
// presents no client certificate, and have the registry's custom headers
// removed.
type originScopedTransport struct {
	origin           *url.URL
	registry         http.RoundTripper
	other            http.RoundTripper
	sensitiveHeaders []string
}

// RoundTrip implements http.RoundTripper.
func (t *originScopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if urlutil.SameOrigin(t.origin, req.URL) {
		return t.registry.RoundTrip(req)
	}
	if len(t.sensitiveHeaders) != 0 {
		// Clone deep-copies the header map, so the caller's request is untouched.
		req = req.Clone(req.Context())
		for key := range req.Header {
			for _, name := range t.sensitiveHeaders {
				if strings.EqualFold(key, name) {
					delete(req.Header, key)
					break
				}
			}
		}
	}
	return t.other.RoundTrip(req)
}
