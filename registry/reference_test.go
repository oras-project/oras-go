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
	"fmt"
	"reflect"
	"testing"
)

const ValidDigest = "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
const InvalidDigest = "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde"

// For a definition of what a "valid form [ABCD]" means, see reference.go.
func TestParseReferenceGoodies(t *testing.T) {
	tests := []struct {
		name         string
		image        string
		wantTemplate Reference
	}{
		{
			name:  "digest reference (valid form A)",
			image: fmt.Sprintf("hello-world@%s", ValidDigest),
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
		},
		{
			name:  "tag with digest (valid form B)",
			image: fmt.Sprintf("hello-world:v2@%s", ValidDigest),
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
		},
		{
			name:  "empty tag with digest (valid form B)",
			image: fmt.Sprintf("hello-world:@%s", ValidDigest),
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
		},
		{
			name:  "tag reference (valid form C)",
			image: "hello-world:v1",
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  "v1",
			},
		},
		{
			name:  "basic reference (valid form D)",
			image: "hello-world",
			wantTemplate: Reference{
				Repository: "hello-world",
			},
		},
	}

	registries := []string{
		"localhost",
		"registry.example.com",
		"localhost:5000",
		"127.0.0.1:5000",
		"[::1]:5000",
	}

	for _, tt := range tests {
		want := tt.wantTemplate
		for _, registry := range registries {
			want.Registry = registry
			t.Run(tt.name, func(t *testing.T) {
				got, err := ParseReference(fmt.Sprintf("%s/%s", registry, tt.image))
				if err != nil {
					t.Errorf("ParseReference() encountered unexpected error: %v", err)
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("ParseReference() = %v, want %v", got, tt.wantTemplate)
				}
			})
		}
	}
}

func TestParseReferenceUglies(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Reference
	}{
		{
			name: "no repo name",
			raw:  "localhost",
		},
		{
			name: "missing registry",
			raw:  "hello-world:linux",
		},
		{
			name: "missing registry (issue #698)",
			raw:  "/hello-world:linux",
		},
		{
			name: "invalid repo name",
			raw:  "localhost/UPPERCASE/test",
		},
		{
			name: "invalid port",
			raw:  "localhost:v1/hello-world",
		},
		{
			name: "invalid digest",
			raw:  fmt.Sprintf("registry.example.com/foobar@%s", InvalidDigest),
		},
		{
			name: "invalid digest prefix: colon instead of the at sign",
			raw:  fmt.Sprintf("registry.example.com/hello-world:foobar:%s", ValidDigest),
		},
		{
			name: "invalid digest prefix: double at sign",
			raw:  fmt.Sprintf("registry.example.com/hello-world@@%s", ValidDigest),
		},
		{
			name: "invalid digest prefix: space",
			raw:  fmt.Sprintf("registry.example.com/hello-world @%s", ValidDigest),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseReference(tt.raw); err == nil {
				t.Errorf("ParseReference() expected an error, but got none")
				return
			}
		})
	}
}

func TestReference_Validate(t *testing.T) {
	tests := []struct {
		name      string
		reference Reference
		wantErr   bool
	}{
		{
			name: "valid reference with tag",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1.0.0",
			},
			wantErr: false,
		},
		{
			name: "valid reference with digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			wantErr: false,
		},
		{
			name: "valid reference without tag or digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			wantErr: false,
		},
		{
			name: "invalid registry",
			reference: Reference{
				Registry:   "invalid registry",
				Repository: "hello-world",
				Reference:  "v1.0.0",
			},
			wantErr: true,
		},
		{
			name: "invalid repository",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "INVALID_REPO",
				Reference:  "v1.0.0",
			},
			wantErr: true,
		},
		{
			name: "invalid tag",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "INVALID_TAG!",
			},
			wantErr: true,
		},
		{
			name: "invalid digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  InvalidDigest,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.reference.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Reference.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReference_Host(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{
			name:     "docker.io",
			registry: "docker.io",
			want:     "registry-1.docker.io",
		},
		{
			name:     "other registry",
			registry: "registry.example.com",
			want:     "registry.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := Reference{
				Registry: tt.registry,
			}
			if got := ref.Host(); got != tt.want {
				t.Errorf("Reference.Host() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestReference_ReferenceOrDefault(t *testing.T) {
	tests := []struct {
		name      string
		reference Reference
		want      string
	}{
		{
			name: "empty reference",
			reference: Reference{
				Reference: "",
			},
			want: "latest",
		},
		{
			name: "non-empty reference",
			reference: Reference{
				Reference: "v1.0.0",
			},
			want: "v1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reference.ReferenceOrDefault(); got != tt.want {
				t.Errorf("Reference.ReferenceOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReference_String(t *testing.T) {
	tests := []struct {
		name      string
		reference Reference
		want      string
	}{
		{
			name: "only registry",
			reference: Reference{
				Registry: "registry.example.com",
			},
			want: "registry.example.com",
		},
		{
			name: "registry and repository",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			want: "registry.example.com/hello-world",
		},
		{
			name: "registry, repository and tag",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1.0.0",
			},
			want: "registry.example.com/hello-world:v1.0.0",
		},
		{
			name: "registry, repository and digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			want: fmt.Sprintf("registry.example.com/hello-world@%s", ValidDigest),
		},
		{
			name: "registry, repository and invalid digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  InvalidDigest,
			},
			want: fmt.Sprintf("registry.example.com/hello-world:%s", InvalidDigest),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reference.String(); got != tt.want {
				t.Errorf("Reference.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseReferenceWithSchemes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Reference
		wantErr bool
	}{
		// oci:// scheme tests
		{
			name:  "oci scheme with digest (valid form A)",
			input: fmt.Sprintf("oci://localhost/hello-world@%s", ValidDigest),
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			wantErr: false,
		},
		{
			name:  "oci scheme with tag (valid form C)",
			input: "oci://registry.example.com/hello-world:v1",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1",
			},
			wantErr: false,
		},
		{
			name:  "oci scheme with tag and digest (valid form B)",
			input: fmt.Sprintf("oci://registry.example.com/hello-world:v2@%s", ValidDigest),
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			wantErr: false,
		},
		{
			name:  "oci scheme basic reference (valid form D)",
			input: "oci://127.0.0.1:5000/hello-world",
			want: Reference{
				Registry:   "127.0.0.1:5000",
				Repository: "hello-world",
			},
			wantErr: false,
		},
		{
			name:  "oci scheme with IPv6 registry",
			input: "oci://[::1]:5000/hello-world:latest",
			want: Reference{
				Registry:   "[::1]:5000",
				Repository: "hello-world",
				Reference:  "latest",
			},
			wantErr: false,
		},
		{
			name:  "oci scheme with multi-level repository",
			input: "oci://ghcr.io/oras-project/oras-go:v3.0.0",
			want: Reference{
				Registry:   "ghcr.io",
				Repository: "oras-project/oras-go",
				Reference:  "v3.0.0",
			},
			wantErr: false,
		},

		// http:// scheme tests
		{
			name:  "http scheme with digest",
			input: fmt.Sprintf("http://localhost/hello-world@%s", ValidDigest),
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			wantErr: false,
		},
		{
			name:  "http scheme with tag",
			input: "http://registry.example.com:8080/hello-world:v1",
			want: Reference{
				Registry:   "registry.example.com:8080",
				Repository: "hello-world",
				Reference:  "v1",
			},
			wantErr: false,
		},

		// https:// scheme tests
		{
			name:  "https scheme with digest",
			input: fmt.Sprintf("https://localhost/hello-world@%s", ValidDigest),
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  ValidDigest,
			},
			wantErr: false,
		},
		{
			name:  "https scheme with tag",
			input: "https://registry.example.com/hello-world:v1",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1",
			},
			wantErr: false,
		},

		// Backward compatibility - no scheme
		{
			name:  "no scheme (backward compatibility)",
			input: "localhost/hello-world:v1",
			want: Reference{
				Registry:   "localhost",
				Repository: "hello-world",
				Reference:  "v1",
			},
			wantErr: false,
		},

		// Edge cases - invalid inputs should still fail validation
		{
			name:    "oci scheme but missing repository",
			input:   "oci://localhost",
			wantErr: true,
		},
		{
			name:    "oci scheme but invalid registry",
			input:   "oci://invalid registry/repo",
			wantErr: true,
		},
		{
			name:    "oci scheme but invalid repository name",
			input:   "oci://localhost/UPPERCASE",
			wantErr: true,
		},
		{
			name:    "scheme in wrong position (middle)",
			input:   "localhost/oci://hello-world",
			wantErr: true,
		},

		// Case sensitivity - schemes should be lowercase
		{
			name:    "uppercase OCI scheme not stripped",
			input:   "OCI://localhost/hello-world",
			wantErr: true, // "OCI:" will be parsed as invalid registry
		},
		{
			name:    "mixed case scheme not stripped",
			input:   "Oci://localhost/hello-world",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReference(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseReference() = %v, want %v", got, tt.want)
			}
		})
	}
}
