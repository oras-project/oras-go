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
	"net/url"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func Test_requestResource(t *testing.T) {
	tests := []struct {
		name string
		url  string
		host string // overrides the Host header, as http.Request.Host does
		want properties.Resource
	}{
		{
			name: "manifest by tag",
			url:  "https://example.com/v2/myspace/app/manifests/v1",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "manifest by digest",
			url:  "https://example.com/v2/myspace/app/manifests/sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "blob",
			url:  "https://example.com/v2/myspace/app/blobs/sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "blob upload",
			url:  "https://example.com/v2/myspace/app/blobs/uploads/",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "blob upload session",
			url:  "https://example.com/v2/myspace/app/blobs/uploads/c5dbf9a8-b53d-4e04-9e2f-9e4cd6ad0a05?_state=xyz",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "tag list",
			url:  "https://example.com/v2/myspace/app/tags/list",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "tag list with query",
			url:  "https://example.com/v2/myspace/app/tags/list?n=10&last=v1",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "referrers",
			url:  "https://example.com/v2/myspace/app/referrers/sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
			want: properties.Resource{Registry: "example.com", Path: "myspace/app"},
		},
		{
			name: "single segment repository",
			url:  "https://example.com/v2/app/manifests/v1",
			want: properties.Resource{Registry: "example.com", Path: "app"},
		},
		{
			name: "deeply nested repository",
			url:  "https://example.com/v2/a/b/c/d/manifests/v1",
			want: properties.Resource{Registry: "example.com", Path: "a/b/c/d"},
		},
		{
			name: "repository segment named blobs",
			url:  "https://example.com/v2/myspace/blobs/manifests/v1",
			want: properties.Resource{Registry: "example.com", Path: "myspace/blobs"},
		},
		{
			name: "repository segment named manifests",
			url:  "https://example.com/v2/myspace/manifests/blobs/sha256:b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c",
			want: properties.Resource{Registry: "example.com", Path: "myspace/manifests"},
		},
		{
			name: "registry with port",
			url:  "http://localhost:5000/v2/myspace/app/manifests/v1",
			want: properties.Resource{Registry: "localhost:5000", Path: "myspace/app"},
		},
		{
			name: "host header wins over url host",
			url:  "https://example.com/v2/myspace/app/manifests/v1",
			host: "registry.example.com",
			want: properties.Resource{Registry: "registry.example.com", Path: "myspace/app"},
		},
		{
			name: "base api endpoint",
			url:  "https://example.com/v2/",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "base api endpoint without trailing slash",
			url:  "https://example.com/v2",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "catalog",
			url:  "https://example.com/v2/_catalog",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "repository without endpoint",
			url:  "https://example.com/v2/myspace/app",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "registry behind a path prefix",
			url:  "https://example.com/prefix/v2/myspace/app/manifests/v1",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "non-registry path",
			url:  "https://example.com/some/other/path",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "root path",
			url:  "https://example.com/",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "empty repository",
			url:  "https://example.com/v2//manifests/v1",
			want: properties.Resource{Registry: "example.com"},
		},
		{
			name: "invalid repository",
			url:  "https://example.com/v2/UPPER/manifests/v1",
			want: properties.Resource{Registry: "example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Host = tt.host
			if got := requestResource(req); got != tt.want {
				t.Errorf("requestResource() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func Test_crossesRepository(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{
			name: "same repository",
			from: "/v2/team-a/image/manifests/latest",
			to:   "/v2/team-a/image/blobs/sha256:deadbeef",
		},
		{
			name: "different repository",
			from: "/v2/team-a/image/manifests/latest",
			to:   "/v2/team-b/image/manifests/latest",
			want: true,
		},
		{
			name: "repository to storage path",
			from: "/v2/team-a/image/blobs/sha256:deadbeef",
			to:   "/storage/sha256:deadbeef",
		},
		{
			name: "storage path to repository",
			from: "/storage/sha256:deadbeef",
			to:   "/v2/team-a/image/blobs/sha256:deadbeef",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := &url.URL{Path: tt.from}
			to := &url.URL{Path: tt.to}
			if got := crossesRepository(from, to); got != tt.want {
				t.Errorf("crossesRepository(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
