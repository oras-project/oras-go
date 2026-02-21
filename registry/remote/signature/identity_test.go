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

package signature

import (
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/config"
)

func TestMatchSignedIdentity_Default(t *testing.T) {
	tests := []struct {
		name             string
		imageRef         string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Exact match",
			imageRef:        "registry.example.com/repo:latest",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
		{
			name:            "Different tag",
			imageRef:        "registry.example.com/repo:v1",
			signedDockerRef: "registry.example.com/repo:v2",
			want:            false,
		},
		{
			name:            "Digest ref matches same repo with tag",
			imageRef:        "registry.example.com/repo@sha256:abc123",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
		{
			name:            "Digest ref different repo",
			imageRef:        "registry.example.com/repo@sha256:abc123",
			signedDockerRef: "registry.example.com/other:latest",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(nil, tt.imageRef, tt.signedDockerRef)
			if err != nil {
				t.Fatalf("MatchSignedIdentity() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchSignedIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_MatchExact(t *testing.T) {
	si := &config.SignedIdentity{Type: config.IdentityMatchExact}

	tests := []struct {
		name             string
		imageRef         string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Exact match",
			imageRef:        "registry.example.com/repo:latest",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
		{
			name:            "Different tag",
			imageRef:        "registry.example.com/repo:v1",
			signedDockerRef: "registry.example.com/repo:v2",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(si, tt.imageRef, tt.signedDockerRef)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_MatchRepository(t *testing.T) {
	si := &config.SignedIdentity{Type: config.IdentityMatchRepository}

	tests := []struct {
		name             string
		imageRef         string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Same repo different tag",
			imageRef:        "registry.example.com/repo:v1",
			signedDockerRef: "registry.example.com/repo:v2",
			want:            true,
		},
		{
			name:            "Different repo",
			imageRef:        "registry.example.com/repo1:v1",
			signedDockerRef: "registry.example.com/repo2:v1",
			want:            false,
		},
		{
			name:            "Same repo, one with digest",
			imageRef:        "registry.example.com/repo@sha256:abc",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(si, tt.imageRef, tt.signedDockerRef)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_ExactReference(t *testing.T) {
	si := &config.SignedIdentity{
		Type:            config.IdentityMatchExactReference,
		DockerReference: "registry.example.com/repo:v1",
	}

	tests := []struct {
		name             string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Matches configured reference",
			signedDockerRef: "registry.example.com/repo:v1",
			want:            true,
		},
		{
			name:            "Does not match",
			signedDockerRef: "registry.example.com/repo:v2",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(si, "anything", tt.signedDockerRef)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_ExactRepository(t *testing.T) {
	si := &config.SignedIdentity{
		Type:             config.IdentityMatchExactRepository,
		DockerRepository: "registry.example.com/repo",
	}

	tests := []struct {
		name             string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Matches configured repository",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
		{
			name:            "Different repository",
			signedDockerRef: "registry.example.com/other:latest",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(si, "anything", tt.signedDockerRef)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_Remap(t *testing.T) {
	si := &config.SignedIdentity{
		Type:         config.IdentityMatchRemap,
		Prefix:       "mirror.example.com",
		SignedPrefix: "registry.example.com",
	}

	tests := []struct {
		name             string
		imageRef         string
		signedDockerRef  string
		want             bool
	}{
		{
			name:            "Remapped match",
			imageRef:        "mirror.example.com/repo:latest",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            true,
		},
		{
			name:            "No match, different suffix",
			imageRef:        "mirror.example.com/repo:v1",
			signedDockerRef: "registry.example.com/repo:v2",
			want:            false,
		},
		{
			name:            "No prefix match",
			imageRef:        "other.example.com/repo:latest",
			signedDockerRef: "registry.example.com/repo:latest",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSignedIdentity(si, tt.imageRef, tt.signedDockerRef)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_UnknownType(t *testing.T) {
	si := &config.SignedIdentity{Type: "unknownType"}
	_, err := MatchSignedIdentity(si, "ref", "signed")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestRepositoryOf(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"registry.example.com/repo:tag", "registry.example.com/repo"},
		{"registry.example.com/repo@sha256:abc", "registry.example.com/repo"},
		{"registry.example.com/repo", "registry.example.com/repo"},
		{"registry.example.com/ns/repo:tag", "registry.example.com/ns/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := repositoryOf(tt.ref)
			if got != tt.want {
				t.Errorf("repositoryOf(%s) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}
