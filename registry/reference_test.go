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
				Digest:     ValidDigest,
			},
		},
		{
			name:  "tag with digest (valid form B)",
			image: fmt.Sprintf("hello-world:v2@%s", ValidDigest),
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  ValidDigest,
				Tag:        "v2",
				Digest:     ValidDigest,
			},
		},
		{
			name:  "empty tag with digest (valid form B)",
			image: fmt.Sprintf("hello-world:@%s", ValidDigest),
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  ValidDigest,
				Digest:     ValidDigest,
			},
		},
		{
			name:  "tag reference (valid form C)",
			image: "hello-world:v1",
			wantTemplate: Reference{
				Repository: "hello-world",
				Reference:  "v1",
				Tag:        "v1",
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

func TestParseReference_FormB_TagAndDigest(t *testing.T) {
	// Test Form B: repository:tag@digest
	// Validates that both tag and digest are captured correctly
	tests := []struct {
		name         string
		artifact     string
		wantRegistry string
		wantRepo     string
		wantTag      string
		wantDigest   string
		wantRef      string
	}{
		{
			name:         "tag with digest",
			artifact:     fmt.Sprintf("localhost/hello-world:v2@%s", ValidDigest),
			wantRegistry: "localhost",
			wantRepo:     "hello-world",
			wantTag:      "v2",
			wantDigest:   ValidDigest,
			wantRef:      ValidDigest,
		},
		{
			name:         "tag with digest - different tag",
			artifact:     fmt.Sprintf("registry.example.com/myapp:1.0.0@%s", ValidDigest),
			wantRegistry: "registry.example.com",
			wantRepo:     "myapp",
			wantTag:      "1.0.0",
			wantDigest:   ValidDigest,
			wantRef:      ValidDigest,
		},
		{
			name:         "tag with digest - complex tag",
			artifact:     fmt.Sprintf("localhost:5000/org/repo:v1.2.3-alpha@%s", ValidDigest),
			wantRegistry: "localhost:5000",
			wantRepo:     "org/repo",
			wantTag:      "v1.2.3-alpha",
			wantDigest:   ValidDigest,
			wantRef:      ValidDigest,
		},
		{
			name:         "empty tag with digest",
			artifact:     fmt.Sprintf("localhost/hello-world:@%s", ValidDigest),
			wantRegistry: "localhost",
			wantRepo:     "hello-world",
			wantTag:      "",
			wantDigest:   ValidDigest,
			wantRef:      ValidDigest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReference(tt.artifact)
			if err != nil {
				t.Fatalf("ParseReference() unexpected error: %v", err)
			}

			// Validate Registry
			if got.Registry != tt.wantRegistry {
				t.Errorf("Registry = %q, want %q", got.Registry, tt.wantRegistry)
			}

			// Validate Repository
			if got.Repository != tt.wantRepo {
				t.Errorf("Repository = %q, want %q", got.Repository, tt.wantRepo)
			}

			// Validate Tag field
			if got.Tag != tt.wantTag {
				t.Errorf("Tag = %q, want %q", got.Tag, tt.wantTag)
			}

			// Validate Digest field
			if got.Digest != tt.wantDigest {
				t.Errorf("Digest = %q, want %q", got.Digest, tt.wantDigest)
			}

			// Validate Reference field (should be digest for Form B)
			if got.Reference != tt.wantRef {
				t.Errorf("Reference = %q, want %q", got.Reference, tt.wantRef)
			}

			// Validate GetReference() method
			if got.GetReference() != tt.wantRef {
				t.Errorf("GetReference() = %q, want %q", got.GetReference(), tt.wantRef)
			}
		})
	}
}

func TestReference_GetReference(t *testing.T) {
	tests := []struct {
		name      string
		reference Reference
		want      string
	}{
		{
			name: "Form A: digest only",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  ValidDigest,
				Digest:     ValidDigest,
			},
			want: ValidDigest,
		},
		{
			name: "Form B: tag with digest",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  ValidDigest,
				Tag:        "v1",
				Digest:     ValidDigest,
			},
			want: ValidDigest,
		},
		{
			name: "Form C: tag only",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "v1.0.0",
				Tag:        "v1.0.0",
			},
			want: "v1.0.0",
		},
		{
			name: "Form D: no reference",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			want: "",
		},
		{
			name: "empty Digest returns Reference field",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "latest",
				Digest:     "",
			},
			want: "latest",
		},
		{
			name: "Digest takes precedence over Reference",
			reference: Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "some-tag",
				Digest:     ValidDigest,
			},
			want: ValidDigest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reference.GetReference(); got != tt.want {
				t.Errorf("Reference.GetReference() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReference_GetReference_ParsedReferences(t *testing.T) {
	// Test GetReference with references created via ParseReference
	tests := []struct {
		name     string
		artifact string
		want     string
	}{
		{
			name:     "Form A: digest reference",
			artifact: fmt.Sprintf("localhost/hello-world@%s", ValidDigest),
			want:     ValidDigest,
		},
		{
			name:     "Form B: tag with digest",
			artifact: fmt.Sprintf("localhost/hello-world:v2@%s", ValidDigest),
			want:     ValidDigest,
		},
		{
			name:     "Form C: tag reference",
			artifact: "localhost/hello-world:v1",
			want:     "v1",
		},
		{
			name:     "Form D: no reference",
			artifact: "localhost/hello-world",
			want:     "",
		},
		{
			name:     "complex tag",
			artifact: "registry.example.com/org/repo:v1.2.3-alpha.1_build.123",
			want:     "v1.2.3-alpha.1_build.123",
		},
		{
			name:     "nested repository with digest",
			artifact: fmt.Sprintf("localhost:5000/org/team/project@%s", ValidDigest),
			want:     ValidDigest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseReference(tt.artifact)
			if err != nil {
				t.Fatalf("ParseReference() unexpected error: %v", err)
			}
			if got := ref.GetReference(); got != tt.want {
				t.Errorf("Reference.GetReference() = %v, want %v", got, tt.want)
			}
		})
	}
}
