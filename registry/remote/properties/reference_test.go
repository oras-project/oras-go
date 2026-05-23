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
	"testing"
)

func TestNewReference(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReg    string
		wantRepo   string
		wantTag    string
		wantDigest string
		wantErr    bool
	}{
		{
			name:     "registry and repository with tag",
			input:    "docker.io/library/alpine:latest",
			wantReg:  "docker.io",
			wantRepo: "library/alpine",
			wantTag:  "latest",
		},
		{
			name:       "registry and repository with digest",
			input:      "docker.io/library/alpine@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantReg:    "docker.io",
			wantRepo:   "library/alpine",
			wantDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:       "tag and digest (digest takes precedence)",
			input:      "docker.io/library/alpine:latest@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantReg:    "docker.io",
			wantRepo:   "library/alpine",
			wantTag:    "latest",
			wantDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "registry with port",
			input:    "localhost:5000/repo:v1",
			wantReg:  "localhost:5000",
			wantRepo: "repo",
			wantTag:  "v1",
		},
		{
			name:     "oci scheme",
			input:    "oci://ghcr.io/myorg/myrepo:latest",
			wantReg:  "ghcr.io",
			wantRepo: "myorg/myrepo",
			wantTag:  "latest",
		},
		{
			name:     "https scheme",
			input:    "https://registry.example.com/repo:v1",
			wantReg:  "registry.example.com",
			wantRepo: "repo",
			wantTag:  "v1",
		},
		{
			name:     "http scheme",
			input:    "http://localhost:5000/repo:v1",
			wantReg:  "localhost:5000",
			wantRepo: "repo",
			wantTag:  "v1",
		},
		{
			name:     "no reference (Form D)",
			input:    "docker.io/library/alpine",
			wantReg:  "docker.io",
			wantRepo: "library/alpine",
		},
		{
			name:    "invalid - missing repository",
			input:   "docker.io",
			wantErr: true,
		},
		{
			name:    "invalid - spaces in reference",
			input:   "docker.io/library/alpine with spaces",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := NewReference(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if ref.Registry != tt.wantReg {
				t.Errorf("Registry = %q, want %q", ref.Registry, tt.wantReg)
			}
			if ref.Repository != tt.wantRepo {
				t.Errorf("Repository = %q, want %q", ref.Repository, tt.wantRepo)
			}
			if ref.Tag != tt.wantTag {
				t.Errorf("Tag = %q, want %q", ref.Tag, tt.wantTag)
			}
			if ref.Digest != tt.wantDigest {
				t.Errorf("Digest = %q, want %q", ref.Digest, tt.wantDigest)
			}
		})
	}
}

func TestNewReferenceList(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantTags  []string
		wantErr   bool
	}{
		{
			name:      "multiple tags",
			input:     "localhost:5000/repo:v1,v2,v3",
			wantCount: 3,
			wantTags:  []string{"v1", "v2", "v3"},
		},
		{
			name:      "single tag",
			input:     "localhost:5000/repo:latest",
			wantCount: 1,
			wantTags:  []string{"latest"},
		},
		{
			name:    "empty list",
			input:   "localhost:5000/repo:",
			wantErr: true,
		},
		{
			name:    "empty item in list",
			input:   "localhost:5000/repo:v1,,v3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs, err := NewReferenceList(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewReferenceList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(refs) != tt.wantCount {
				t.Errorf("len(refs) = %d, want %d", len(refs), tt.wantCount)
			}
			for i, ref := range refs {
				if ref.Tag != tt.wantTags[i] {
					t.Errorf("refs[%d].Tag = %q, want %q", i, ref.Tag, tt.wantTags[i])
				}
			}
		})
	}
}

func TestReference_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     Reference
		wantErr bool
	}{
		{
			name: "valid with tag",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
				Tag:        "latest",
			},
			wantErr: false,
		},
		{
			name: "valid with digest",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
				Digest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			wantErr: false,
		},
		{
			name: "invalid registry",
			ref: Reference{
				Registry:   "invalid registry with spaces",
				Repository: "repo",
			},
			wantErr: true,
		},
		{
			name: "invalid repository",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "INVALID_UPPERCASE",
			},
			wantErr: true,
		},
		{
			name: "invalid tag",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "repo",
				Tag:        "invalid tag with spaces",
			},
			wantErr: true,
		},
		{
			name: "invalid digest",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "repo",
				Digest:     "not-a-valid-digest",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReference_GetReference(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
		want string
	}{
		{
			name: "digest takes precedence",
			ref: Reference{
				Tag:    "latest",
				Digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "tag only",
			ref: Reference{
				Tag: "latest",
			},
			want: "latest",
		},
		{
			name: "empty",
			ref:  Reference{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.GetReference(); got != tt.want {
				t.Errorf("GetReference() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReference_ReferenceOrDefault(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
		want string
	}{
		{
			name: "with tag",
			ref: Reference{
				Tag: "v1",
			},
			want: "v1",
		},
		{
			name: "with digest",
			ref: Reference{
				Digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "empty returns latest",
			ref:  Reference{},
			want: "latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.ReferenceOrDefault(); got != tt.want {
				t.Errorf("ReferenceOrDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReference_Host(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
		want string
	}{
		{
			name: "docker.io returns registry-1.docker.io",
			ref: Reference{
				Registry: "docker.io",
			},
			want: "registry-1.docker.io",
		},
		{
			name: "other registry unchanged",
			ref: Reference{
				Registry: "ghcr.io",
			},
			want: "ghcr.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.Host(); got != tt.want {
				t.Errorf("Host() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReference_String(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
		want string
	}{
		{
			name: "registry only",
			ref: Reference{
				Registry: "docker.io",
			},
			want: "docker.io",
		},
		{
			name: "with repository",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
			},
			want: "docker.io/library/alpine",
		},
		{
			name: "with tag",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
				Tag:        "latest",
			},
			want: "docker.io/library/alpine:latest",
		},
		{
			name: "with digest",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
				Digest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want: "docker.io/library/alpine@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "with tag and digest",
			ref: Reference{
				Registry:   "docker.io",
				Repository: "library/alpine",
				Tag:        "latest",
				Digest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want: "docker.io/library/alpine:latest@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReference_GetDigest(t *testing.T) {
	tests := []struct {
		name    string
		ref     Reference
		wantErr bool
	}{
		{
			name: "valid digest",
			ref: Reference{
				Digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			wantErr: false,
		},
		{
			name:    "empty digest",
			ref:     Reference{},
			wantErr: true,
		},
		{
			name: "invalid digest",
			ref: Reference{
				Digest: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.ref.GetDigest()
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDigest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
