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
	"strings"
	"testing"
)

func TestParseResource(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Resource
	}{
		{name: "registry", input: "example.com", want: Resource{Registry: "example.com"}},
		{name: "namespace", input: "example.com/myspace", want: Resource{Registry: "example.com", Path: "myspace"}},
		{name: "repository", input: "example.com/myspace/app", want: Resource{Registry: "example.com", Path: "myspace/app"}},
		{name: "port", input: "localhost:5000/ns", want: Resource{Registry: "localhost:5000", Path: "ns"}},
		{name: "oci scheme", input: "oci://example.com/ns", want: Resource{Registry: "example.com", Path: "ns"}},
		{name: "http scheme", input: "http://example.com/ns", want: Resource{Registry: "example.com", Path: "ns"}},
		{name: "https scheme", input: "https://example.com/ns", want: Resource{Registry: "example.com", Path: "ns"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseResource(tt.input)
			if err != nil {
				t.Fatalf("ParseResource() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseResource() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseResourceRejectsReferencesAndInvalidPaths(t *testing.T) {
	tests := []string{
		"example.com/myspace:v1",
		"example.com/myspace@sha256:deadbeef",
		"example.com/",
		"example.com/UPPER",
		"example.com/a//b",
		"example.com/a b",
		"invalid host/ns",
		"",
	}
	for _, input := range tests {
		t.Run(strings.ReplaceAll(input, "/", "_"), func(t *testing.T) {
			if _, err := ParseResource(input); err == nil {
				t.Fatalf("ParseResource(%q) expected an error", input)
			}
		})
	}
}

func TestParseResourceTagDigestError(t *testing.T) {
	for _, input := range []string{"example.com/myspace:v1", "example.com/myspace@sha256:deadbeef"} {
		t.Run(input, func(t *testing.T) {
			_, err := ParseResource(input)
			if err == nil || !strings.Contains(err.Error(), "must not include a tag or digest") {
				t.Fatalf("ParseResource(%q) error = %v, want tag/digest error", input, err)
			}
		})
	}
}

func TestResourceHost(t *testing.T) {
	tests := []struct {
		name     string
		resource Resource
		want     string
	}{
		{name: "docker hub", resource: Resource{Registry: "docker.io"}, want: "registry-1.docker.io"},
		{name: "other registry", resource: Resource{Registry: "ghcr.io"}, want: "ghcr.io"},
		{name: "registry port", resource: Resource{Registry: "localhost:5000"}, want: "localhost:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Host(); got != tt.want {
				t.Errorf("Resource.Host() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResourceString(t *testing.T) {
	tests := []struct {
		name     string
		resource Resource
		want     string
	}{
		{name: "registry", resource: Resource{Registry: "example.com"}, want: "example.com"},
		{name: "namespace", resource: Resource{Registry: "example.com", Path: "myspace"}, want: "example.com/myspace"},
		{name: "repository", resource: Resource{Registry: "example.com", Path: "myspace/app"}, want: "example.com/myspace/app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.String(); got != tt.want {
				t.Errorf("Resource.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
