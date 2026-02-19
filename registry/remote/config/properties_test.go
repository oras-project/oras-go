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

package config

import (
	"errors"
	"testing"
)

func TestRegistriesConfig_RegistryProperties(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
			{Prefix: "insecure.example.com", Insecure: true},
			{Prefix: "blocked.example.com", Blocked: true},
			{Prefix: "*.wildcard.example.com", Insecure: true},
		},
		Aliases: map[string]string{
			"myapp": "docker.io/library/myapp",
		},
	}

	tests := []struct {
		name         string
		ref          string
		wantRegistry string
		wantRepo     string
		wantInsecure bool
		wantErr      bool
		wantBlocked  bool
	}{
		{
			name:         "basic reference with no matching registry",
			ref:          "ghcr.io/user/repo:v1",
			wantRegistry: "ghcr.io",
			wantRepo:     "user/repo",
			wantInsecure: false,
		},
		{
			name:         "reference rewriting via location",
			ref:          "docker.io/nginx:latest",
			wantRegistry: "registry-1.docker.io",
			wantRepo:     "nginx",
			wantInsecure: false,
		},
		{
			name:         "insecure registry",
			ref:          "insecure.example.com/myimage:v1",
			wantRegistry: "insecure.example.com",
			wantRepo:     "myimage",
			wantInsecure: true,
		},
		{
			name:        "blocked registry",
			ref:         "blocked.example.com/image:latest",
			wantErr:     true,
			wantBlocked: true,
		},
		{
			name:         "alias resolution",
			ref:          "myapp",
			wantRegistry: "registry-1.docker.io",
			wantRepo:     "library/myapp",
			wantInsecure: false,
		},
		{
			name:         "wildcard prefix match",
			ref:          "sub.wildcard.example.com/image:tag",
			wantRegistry: "sub.wildcard.example.com",
			wantRepo:     "image",
			wantInsecure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props, err := config.RegistryProperties(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("RegistryProperties() expected error, got nil")
				}
				if tt.wantBlocked && !errors.Is(err, ErrRegistryBlocked) {
					t.Errorf("RegistryProperties() error = %v, want ErrRegistryBlocked", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RegistryProperties() unexpected error: %v", err)
			}
			if props.Reference.Registry != tt.wantRegistry {
				t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, tt.wantRegistry)
			}
			if props.Reference.Repository != tt.wantRepo {
				t.Errorf("Reference.Repository = %q, want %q", props.Reference.Repository, tt.wantRepo)
			}
			if props.Transport.Insecure != tt.wantInsecure {
				t.Errorf("Transport.Insecure = %v, want %v", props.Transport.Insecure, tt.wantInsecure)
			}
		})
	}
}

func TestNewRegistryProperties_NilConfig(t *testing.T) {
	props, err := NewRegistryProperties("ghcr.io/user/repo:v1", nil)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Reference.Registry != "ghcr.io" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "ghcr.io")
	}
	if props.Reference.Repository != "user/repo" {
		t.Errorf("Reference.Repository = %q, want %q", props.Reference.Repository, "user/repo")
	}
	if props.Reference.Tag != "v1" {
		t.Errorf("Reference.Tag = %q, want %q", props.Reference.Tag, "v1")
	}
	if props.Transport.Insecure {
		t.Error("Transport.Insecure should be false with nil config")
	}
}

func TestNewRegistryProperties_InvalidReference(t *testing.T) {
	_, err := NewRegistryProperties("", nil)
	if err == nil {
		t.Error("NewRegistryProperties() expected error for empty reference")
	}
}

func TestNewRegistryProperties_NoMatchingRegistry(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("ghcr.io/user/repo:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Reference.Registry != "ghcr.io" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "ghcr.io")
	}
	if props.Transport.Insecure {
		t.Error("Transport.Insecure should be false when no registry matches")
	}
}

func TestRegistriesConfig_SearchRegistryProperties(t *testing.T) {
	config := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"docker.io", "quay.io"},
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
			{Prefix: "quay.io", Insecure: true},
		},
		Aliases: map[string]string{},
	}

	results, err := config.SearchRegistryProperties("library/alpine:latest")
	if err != nil {
		t.Fatalf("SearchRegistryProperties() unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchRegistryProperties() returned %d results, want 2", len(results))
	}

	// First result: docker.io (rewritten to registry-1.docker.io)
	if results[0].Reference.Registry != "registry-1.docker.io" {
		t.Errorf("results[0].Reference.Registry = %q, want %q", results[0].Reference.Registry, "registry-1.docker.io")
	}
	if results[0].Reference.Repository != "library/alpine" {
		t.Errorf("results[0].Reference.Repository = %q, want %q", results[0].Reference.Repository, "library/alpine")
	}

	// Second result: quay.io (insecure)
	if results[1].Reference.Registry != "quay.io" {
		t.Errorf("results[1].Reference.Registry = %q, want %q", results[1].Reference.Registry, "quay.io")
	}
	if !results[1].Transport.Insecure {
		t.Error("results[1].Transport.Insecure should be true")
	}
}

func TestRegistriesConfig_SearchRegistryProperties_WithBlockedRegistry(t *testing.T) {
	config := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"blocked.example.com", "quay.io"},
		Registries: []Registry{
			{Prefix: "blocked.example.com", Blocked: true},
		},
		Aliases: map[string]string{},
	}

	results, err := config.SearchRegistryProperties("library/alpine:latest")
	if err != nil {
		t.Fatalf("SearchRegistryProperties() unexpected error: %v", err)
	}
	// Blocked registry should be skipped
	if len(results) != 1 {
		t.Fatalf("SearchRegistryProperties() returned %d results, want 1", len(results))
	}
	if results[0].Reference.Registry != "quay.io" {
		t.Errorf("results[0].Reference.Registry = %q, want %q", results[0].Reference.Registry, "quay.io")
	}
}

func TestRegistriesConfig_SearchRegistryProperties_NilConfig(t *testing.T) {
	var config *RegistriesConfig
	results, err := config.SearchRegistryProperties("library/alpine:latest")
	if err != nil {
		t.Fatalf("SearchRegistryProperties() unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("SearchRegistryProperties() on nil config = %v, want nil", results)
	}
}

func TestRegistriesConfig_SearchRegistryProperties_EmptySearchRegistries(t *testing.T) {
	config := &RegistriesConfig{
		Aliases: map[string]string{},
	}
	results, err := config.SearchRegistryProperties("library/alpine:latest")
	if err != nil {
		t.Fatalf("SearchRegistryProperties() unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("SearchRegistryProperties() with empty search registries = %v, want nil", results)
	}
}

func TestRegistriesConfig_RegistryProperties_InsecureNotAppliedAfterRewrite(t *testing.T) {
	// When a reference is rewritten to a different registry, the insecure
	// setting should be based on the original match, not the rewritten ref.
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "internal.example.com", Location: "mirror.example.com", Insecure: true},
		},
		Aliases: map[string]string{},
	}

	props, err := config.RegistryProperties("internal.example.com/image:v1")
	if err != nil {
		t.Fatalf("RegistryProperties() unexpected error: %v", err)
	}

	// The reference should be rewritten.
	if props.Reference.Registry != "mirror.example.com" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "mirror.example.com")
	}
}

func TestNewRegistryProperties_MirrorsPopulated(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix: "docker.io",
				Mirrors: []Mirror{
					{Location: "mirror1.example.com"},
					{Location: "mirror2.example.com", Insecure: true},
				},
			},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("docker.io/library/alpine:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if len(props.Mirrors) != 2 {
		t.Fatalf("Mirrors length = %d, want 2", len(props.Mirrors))
	}
	if props.Mirrors[0].Location != "mirror1.example.com" {
		t.Errorf("Mirrors[0].Location = %q, want %q", props.Mirrors[0].Location, "mirror1.example.com")
	}
	if props.Mirrors[1].Location != "mirror2.example.com" {
		t.Errorf("Mirrors[1].Location = %q, want %q", props.Mirrors[1].Location, "mirror2.example.com")
	}
}

func TestNewRegistryProperties_MirrorInsecurePropagated(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix: "docker.io",
				Mirrors: []Mirror{
					{Location: "secure.example.com", Insecure: false},
					{Location: "insecure.example.com", Insecure: true},
				},
			},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("docker.io/library/alpine:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Mirrors[0].Transport.Insecure {
		t.Error("Mirrors[0].Transport.Insecure should be false")
	}
	if !props.Mirrors[1].Transport.Insecure {
		t.Error("Mirrors[1].Transport.Insecure should be true")
	}
}

func TestNewRegistryProperties_MirrorPullFromMirrorValues(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix: "docker.io",
				Mirrors: []Mirror{
					{Location: "m1.example.com", PullFromMirror: "all"},
					{Location: "m2.example.com", PullFromMirror: "digest-only"},
					{Location: "m3.example.com", PullFromMirror: "tag-only"},
				},
			},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("docker.io/library/alpine:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Mirrors[0].PullFromMirror != "all" {
		t.Errorf("Mirrors[0].PullFromMirror = %q, want %q", props.Mirrors[0].PullFromMirror, "all")
	}
	if props.Mirrors[1].PullFromMirror != "digest-only" {
		t.Errorf("Mirrors[1].PullFromMirror = %q, want %q", props.Mirrors[1].PullFromMirror, "digest-only")
	}
	if props.Mirrors[2].PullFromMirror != "tag-only" {
		t.Errorf("Mirrors[2].PullFromMirror = %q, want %q", props.Mirrors[2].PullFromMirror, "tag-only")
	}
}

func TestNewRegistryProperties_MirrorByDigestOnlyDefault(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix:             "docker.io",
				MirrorByDigestOnly: true,
				Mirrors: []Mirror{
					{Location: "m1.example.com"},                              // empty → should default to "digest-only"
					{Location: "m2.example.com", PullFromMirror: "tag-only"},  // explicit → should stay "tag-only"
					{Location: "m3.example.com", PullFromMirror: "all"},       // explicit → should stay "all"
				},
			},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("docker.io/library/alpine:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Mirrors[0].PullFromMirror != "digest-only" {
		t.Errorf("Mirrors[0].PullFromMirror = %q, want %q", props.Mirrors[0].PullFromMirror, "digest-only")
	}
	if props.Mirrors[1].PullFromMirror != "tag-only" {
		t.Errorf("Mirrors[1].PullFromMirror = %q, want %q", props.Mirrors[1].PullFromMirror, "tag-only")
	}
	if props.Mirrors[2].PullFromMirror != "all" {
		t.Errorf("Mirrors[2].PullFromMirror = %q, want %q", props.Mirrors[2].PullFromMirror, "all")
	}
}

func TestNewRegistryProperties_NoMirrors(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "docker.io"},
		},
		Aliases: map[string]string{},
	}

	props, err := NewRegistryProperties("docker.io/library/alpine:latest", config)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	if props.Mirrors != nil {
		t.Errorf("Mirrors = %v, want nil", props.Mirrors)
	}
}
