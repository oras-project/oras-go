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
	_ "crypto/sha256"
	"reflect"
	"testing"
)

func TestParseReference(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Reference
		wantErr bool
	}{
		{
			name: "basic reference",
			raw:  "localhost/hello-world",
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
			},
		},
		{
			name: "tag reference",
			raw:  "localhost/hello-world:v1",
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name: "digest reference",
			raw:  "localhost/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "tag with digest",
			raw:  "localhost/hello-world:v2@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "basic reference with non local DNS name",
			raw:  "registry.example.com/hello-world",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
		},
		{
			name: "tag reference with non local DNS name",
			raw:  "registry.example.com/hello-world:v1",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name: "digest reference with non local DNS name",
			raw:  "registry.example.com/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "tag with digest with non local DNS name",
			raw:  "registry.example.com/hello-world:v2@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "basic reference with port",
			raw:  "localhost:5000/hello-world",
			want: Reference{
				Registry:   "localhost:5000",
				Repository: "hello-world",
			},
		},
		{
			name: "tag reference with port",
			raw:  "localhost:5000/hello-world:v1",
			want: Reference{
				Registry:   "localhost:5000",
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name: "digest reference with port",
			raw:  "localhost:5000/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "localhost:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "tag with digest with port",
			raw:  "localhost:5000/hello-world:v2@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "localhost:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "basic reference with IP and port",
			raw:  "127.0.0.1:5000/hello-world",
			want: Reference{
				Registry:   "127.0.0.1:5000",
				Repository: "hello-world",
			},
		},
		{
			name: "tag reference with IP and port",
			raw:  "127.0.0.1:5000/hello-world:v1",
			want: Reference{
				Registry:   "127.0.0.1:5000",
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name: "digest reference with IP and port",
			raw:  "127.0.0.1:5000/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "127.0.0.1:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "tag with digest with IP and port",
			raw:  "127.0.0.1:5000/hello-world:v2@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "127.0.0.1:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "basic reference with IPv6 and port",
			raw:  "[::1]:5000/hello-world",
			want: Reference{
				Registry:   "[::1]:5000",
				Repository: "hello-world",
			},
		},
		{
			name: "tag reference with IPv6 and port",
			raw:  "[::1]:5000/hello-world:v1",
			want: Reference{
				Registry:   "[::1]:5000",
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name: "digest reference with IPv6 and port",
			raw:  "[::1]:5000/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "[::1]:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name: "tag with digest with IPv6 and port",
			raw:  "[::1]:5000/hello-world:v2@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: Reference{
				Registry:   "[::1]:5000",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name:    "no repo name",
			raw:     "localhost",
			wantErr: true,
		},
		{
			name:    "missing registry",
			raw:     "hello-world:linux",
			wantErr: true,
		},
		{
			name:    "invalid repo name",
			raw:     "localhost/UPPERCASE/test",
			wantErr: true,
		},
		{
			name:    "invalid port",
			raw:     "localhost:v1/hello-world",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReference(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseReference() = %v, want %v", got, tt.want)
			}
		})
	}
}
