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

package configuration

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadRegistriesConfig(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		want        *RegistriesConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid basic config",
			content: `
unqualified-search-registries = ["docker.io", "quay.io"]
short-name-mode = "permissive"

[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
insecure = false
blocked = false

[aliases]
"alpine" = "docker.io/library/alpine"
`,
			want: &RegistriesConfig{
				UnqualifiedSearchRegistries: []string{"docker.io", "quay.io"},
				ShortNameMode:               "permissive",
				Registries: []Registry{
					{
						Prefix:   "docker.io",
						Location: "registry-1.docker.io",
						Insecure: false,
						Blocked:  false,
					},
				},
				Aliases: map[string]string{
					"alpine": "docker.io/library/alpine",
				},
			},
			wantErr: false,
		},
		{
			name: "config with mirrors",
			content: `
[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"

[[registry.mirror]]
location = "mirror.gcr.io"
insecure = false
pull-from-mirror = "all"

[[registry.mirror]]
location = "mirror2.example.com"
insecure = true
pull-from-mirror = "digest-only"
`,
			want: &RegistriesConfig{
				Registries: []Registry{
					{
						Prefix:   "docker.io",
						Location: "registry-1.docker.io",
						Mirrors: []Mirror{
							{
								Location:       "mirror.gcr.io",
								Insecure:       false,
								PullFromMirror: "all",
							},
							{
								Location:       "mirror2.example.com",
								Insecure:       true,
								PullFromMirror: "digest-only",
							},
						},
					},
				},
				Aliases: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "config with blocked registry",
			content: `
[[registry]]
prefix = "malicious.registry.com"
blocked = true
`,
			want: &RegistriesConfig{
				Registries: []Registry{
					{
						Prefix:  "malicious.registry.com",
						Blocked: true,
					},
				},
				Aliases: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "config with wildcard prefix",
			content: `
[[registry]]
prefix = "*.example.com"
insecure = true
`,
			want: &RegistriesConfig{
				Registries: []Registry{
					{
						Prefix:   "*.example.com",
						Insecure: true,
					},
				},
				Aliases: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "config with mirror-by-digest-only",
			content: `
[[registry]]
prefix = "docker.io"
mirror-by-digest-only = true

[[registry.mirror]]
location = "mirror.example.com"
`,
			want: &RegistriesConfig{
				Registries: []Registry{
					{
						Prefix:             "docker.io",
						MirrorByDigestOnly: true,
						Mirrors: []Mirror{
							{
								Location: "mirror.example.com",
							},
						},
					},
				},
				Aliases: map[string]string{},
			},
			wantErr: false,
		},
		{
			name:    "empty config",
			content: "",
			want: &RegistriesConfig{
				Aliases: map[string]string{},
			},
			wantErr: false,
		},
		{
			name:        "invalid TOML",
			content:     "this is not valid toml [[[",
			wantErr:     true,
			errContains: "failed to parse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "registries.conf")
			if err := os.WriteFile(configPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test config: %v", err)
			}

			got, err := LoadRegistriesConfig(configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadRegistriesConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil || !contains(err.Error(), tt.errContains) {
					t.Errorf("LoadRegistriesConfig() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LoadRegistriesConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadRegistriesConfig_FileNotFound(t *testing.T) {
	_, err := LoadRegistriesConfig("/nonexistent/path/registries.conf")
	if err == nil {
		t.Error("LoadRegistriesConfig() expected error for nonexistent file")
	}
}

func TestRegistriesConfig_FindRegistry(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
			{Prefix: "docker.io/library", Location: "library-mirror.example.com"},
			{Prefix: "quay.io", Insecure: true},
			{Prefix: "*.internal.example.com", Insecure: true},
			{Prefix: "registry.example.com:5000", Location: "localhost:5000"},
		},
	}

	tests := []struct {
		name       string
		ref        string
		wantPrefix string
		wantNil    bool
	}{
		{
			name:       "exact match",
			ref:        "docker.io/nginx:latest",
			wantPrefix: "docker.io",
		},
		{
			name:       "longer prefix match",
			ref:        "docker.io/library/alpine:latest",
			wantPrefix: "docker.io/library",
		},
		{
			name:       "match with port",
			ref:        "registry.example.com:5000/myimage:v1",
			wantPrefix: "registry.example.com:5000",
		},
		{
			name:       "wildcard match",
			ref:        "sub.internal.example.com/image:tag",
			wantPrefix: "*.internal.example.com",
		},
		{
			name:       "wildcard match nested",
			ref:        "deep.sub.internal.example.com/image:tag",
			wantPrefix: "*.internal.example.com",
		},
		{
			name:    "no match",
			ref:     "gcr.io/myproject/myimage",
			wantNil: true,
		},
		{
			name:       "quay.io match",
			ref:        "quay.io/coreos/etcd:v3.4.0",
			wantPrefix: "quay.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.FindRegistry(tt.ref)
			if tt.wantNil {
				if got != nil {
					t.Errorf("FindRegistry(%q) = %+v, want nil", tt.ref, got)
				}
				return
			}
			if got == nil {
				t.Errorf("FindRegistry(%q) = nil, want prefix %q", tt.ref, tt.wantPrefix)
				return
			}
			if got.Prefix != tt.wantPrefix {
				t.Errorf("FindRegistry(%q).Prefix = %q, want %q", tt.ref, got.Prefix, tt.wantPrefix)
			}
		})
	}
}

func TestRegistriesConfig_FindRegistry_NilConfig(t *testing.T) {
	var config *RegistriesConfig
	got := config.FindRegistry("docker.io/nginx")
	if got != nil {
		t.Errorf("FindRegistry() on nil config = %+v, want nil", got)
	}
}

func TestRegistriesConfig_ResolveAlias(t *testing.T) {
	config := &RegistriesConfig{
		Aliases: map[string]string{
			"alpine":       "docker.io/library/alpine",
			"nginx":        "docker.io/library/nginx",
			"myapp":        "quay.io/myorg/myapp",
			"complex/name": "registry.example.com/path/to/image",
		},
	}

	tests := []struct {
		name      string
		shortName string
		want      string
		wantOk    bool
	}{
		{
			name:      "existing alias",
			shortName: "alpine",
			want:      "docker.io/library/alpine",
			wantOk:    true,
		},
		{
			name:      "another alias",
			shortName: "myapp",
			want:      "quay.io/myorg/myapp",
			wantOk:    true,
		},
		{
			name:      "alias with slash",
			shortName: "complex/name",
			want:      "registry.example.com/path/to/image",
			wantOk:    true,
		},
		{
			name:      "nonexistent alias",
			shortName: "unknown",
			want:      "",
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := config.ResolveAlias(tt.shortName)
			if ok != tt.wantOk {
				t.Errorf("ResolveAlias(%q) ok = %v, want %v", tt.shortName, ok, tt.wantOk)
			}
			if got != tt.want {
				t.Errorf("ResolveAlias(%q) = %q, want %q", tt.shortName, got, tt.want)
			}
		})
	}
}

func TestRegistriesConfig_ResolveAlias_NilConfig(t *testing.T) {
	var config *RegistriesConfig
	_, ok := config.ResolveAlias("alpine")
	if ok {
		t.Error("ResolveAlias() on nil config should return false")
	}

	config = &RegistriesConfig{Aliases: nil}
	_, ok = config.ResolveAlias("alpine")
	if ok {
		t.Error("ResolveAlias() with nil aliases should return false")
	}
}

func TestRegistriesConfig_IsBlocked(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "blocked.registry.com", Blocked: true},
			{Prefix: "allowed.registry.com", Blocked: false},
			{Prefix: "*.blocked.example.com", Blocked: true},
		},
	}

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{
			name: "blocked registry",
			ref:  "blocked.registry.com/image:tag",
			want: true,
		},
		{
			name: "allowed registry",
			ref:  "allowed.registry.com/image:tag",
			want: false,
		},
		{
			name: "wildcard blocked",
			ref:  "sub.blocked.example.com/image:tag",
			want: true,
		},
		{
			name: "unknown registry",
			ref:  "unknown.registry.com/image:tag",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.IsBlocked(tt.ref)
			if got != tt.want {
				t.Errorf("IsBlocked(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestRegistriesConfig_GetMirrors(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix: "docker.io",
				Mirrors: []Mirror{
					{Location: "mirror1.example.com", PullFromMirror: "all"},
					{Location: "mirror2.example.com", PullFromMirror: "digest-only"},
				},
			},
			{
				Prefix:  "quay.io",
				Mirrors: []Mirror{},
			},
		},
	}

	tests := []struct {
		name    string
		ref     string
		wantLen int
	}{
		{
			name:    "registry with mirrors",
			ref:     "docker.io/nginx:latest",
			wantLen: 2,
		},
		{
			name:    "registry without mirrors",
			ref:     "quay.io/coreos/etcd:v3.4.0",
			wantLen: 0,
		},
		{
			name:    "unknown registry",
			ref:     "gcr.io/myproject/image",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.GetMirrors(tt.ref)
			if len(got) != tt.wantLen {
				t.Errorf("GetMirrors(%q) returned %d mirrors, want %d", tt.ref, len(got), tt.wantLen)
			}
		})
	}
}

func TestRegistriesConfig_RewriteReference(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
			{Prefix: "docker.io/library", Location: "library-mirror.example.com"},
			{Prefix: "quay.io", Location: ""}, // No location, should not rewrite
			{Prefix: "*.internal.example.com", Location: "internal-mirror.example.com"},
		},
	}

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "rewrite with location",
			ref:  "docker.io/nginx:latest",
			want: "registry-1.docker.io/nginx:latest",
		},
		{
			name: "rewrite longer prefix",
			ref:  "docker.io/library/alpine:latest",
			want: "library-mirror.example.com/alpine:latest",
		},
		{
			name: "no location - no rewrite",
			ref:  "quay.io/coreos/etcd:v3.4.0",
			want: "quay.io/coreos/etcd:v3.4.0",
		},
		{
			name: "wildcard prefix - no rewrite",
			ref:  "sub.internal.example.com/image:tag",
			want: "sub.internal.example.com/image:tag",
		},
		{
			name: "unknown registry - no rewrite",
			ref:  "gcr.io/myproject/image:v1",
			want: "gcr.io/myproject/image:v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.RewriteReference(tt.ref)
			if got != tt.want {
				t.Errorf("RewriteReference(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestRegistry_GetLocation(t *testing.T) {
	tests := []struct {
		name     string
		registry Registry
		want     string
	}{
		{
			name:     "with location",
			registry: Registry{Prefix: "docker.io", Location: "registry-1.docker.io"},
			want:     "registry-1.docker.io",
		},
		{
			name:     "without location",
			registry: Registry{Prefix: "quay.io"},
			want:     "quay.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.registry.GetLocation()
			if got != tt.want {
				t.Errorf("GetLocation() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchesPrefix(t *testing.T) {
	tests := []struct {
		name   string
		ref    string
		prefix string
		want   bool
	}{
		{
			name:   "exact match",
			ref:    "docker.io",
			prefix: "docker.io",
			want:   true,
		},
		{
			name:   "prefix with path",
			ref:    "docker.io/nginx",
			prefix: "docker.io",
			want:   true,
		},
		{
			name:   "prefix with tag",
			ref:    "docker.io:latest",
			prefix: "docker.io",
			want:   true,
		},
		{
			name:   "wildcard match",
			ref:    "sub.example.com/image",
			prefix: "*.example.com",
			want:   true,
		},
		{
			name:   "wildcard nested",
			ref:    "deep.sub.example.com/image",
			prefix: "*.example.com",
			want:   true,
		},
		{
			name:   "wildcard no match",
			ref:    "other.domain.com/image",
			prefix: "*.example.com",
			want:   false,
		},
		{
			name:   "no match different registry",
			ref:    "quay.io/image",
			prefix: "docker.io",
			want:   false,
		},
		{
			name:   "partial string no match",
			ref:    "docker.io.evil.com/image",
			prefix: "docker.io",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPrefix(tt.ref, tt.prefix)
			if got != tt.want {
				t.Errorf("matchesPrefix(%q, %q) = %v, want %v", tt.ref, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "simple host",
			ref:  "docker.io",
			want: "docker.io",
		},
		{
			name: "host with path",
			ref:  "docker.io/library/alpine",
			want: "docker.io",
		},
		{
			name: "host with port",
			ref:  "localhost:5000/image",
			want: "localhost:5000",
		},
		{
			name: "host with tag",
			ref:  "docker.io/nginx:latest",
			want: "docker.io",
		},
		{
			name: "host with digest",
			ref:  "docker.io/nginx@sha256:abc123",
			want: "docker.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHost(tt.ref)
			if got != tt.want {
				t.Errorf("extractHost(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestMergeRegistriesConfig(t *testing.T) {
	base := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"docker.io"},
		ShortNameMode:               "permissive",
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
			{Prefix: "quay.io", Insecure: false},
		},
		Aliases: map[string]string{
			"alpine": "docker.io/library/alpine",
			"nginx":  "docker.io/library/nginx",
		},
	}

	dropIn := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"quay.io", "docker.io"},
		ShortNameMode:               "enforcing",
		Registries: []Registry{
			{Prefix: "quay.io", Insecure: true}, // Override
			{Prefix: "gcr.io", Location: "mirror.gcr.io"}, // Add new
		},
		Aliases: map[string]string{
			"nginx":  "quay.io/nginx/nginx", // Override
			"ubuntu": "docker.io/library/ubuntu", // Add new
		},
	}

	result := mergeRegistriesConfig(base, dropIn)

	// Check search registries override
	if !reflect.DeepEqual(result.UnqualifiedSearchRegistries, []string{"quay.io", "docker.io"}) {
		t.Errorf("UnqualifiedSearchRegistries = %v, want [quay.io docker.io]", result.UnqualifiedSearchRegistries)
	}

	// Check short name mode override
	if result.ShortNameMode != "enforcing" {
		t.Errorf("ShortNameMode = %q, want %q", result.ShortNameMode, "enforcing")
	}

	// Check registries
	if len(result.Registries) != 3 {
		t.Errorf("len(Registries) = %d, want 3", len(result.Registries))
	}

	// Check quay.io was overridden
	var quayReg *Registry
	for i := range result.Registries {
		if result.Registries[i].Prefix == "quay.io" {
			quayReg = &result.Registries[i]
			break
		}
	}
	if quayReg == nil || !quayReg.Insecure {
		t.Error("quay.io registry should be insecure after merge")
	}

	// Check aliases
	if result.Aliases["nginx"] != "quay.io/nginx/nginx" {
		t.Errorf("Aliases[nginx] = %q, want %q", result.Aliases["nginx"], "quay.io/nginx/nginx")
	}
	if result.Aliases["alpine"] != "docker.io/library/alpine" {
		t.Errorf("Aliases[alpine] = %q, want %q", result.Aliases["alpine"], "docker.io/library/alpine")
	}
	if result.Aliases["ubuntu"] != "docker.io/library/ubuntu" {
		t.Errorf("Aliases[ubuntu] = %q, want %q", result.Aliases["ubuntu"], "docker.io/library/ubuntu")
	}
}

func TestLoadSystemRegistriesConfig_NotFound(t *testing.T) {
	// Save and restore system paths
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	// Set to nonexistent paths
	systemRegistriesConfPath = "/nonexistent/registries.conf"
	systemRegistriesConfDirPath = "/nonexistent/registries.conf.d"

	// Also ensure user config doesn't exist by using a test that doesn't have HOME set properly
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", "/nonexistent")
	defer os.Setenv("HOME", oldHome)

	_, err := LoadSystemRegistriesConfig()
	if err != ErrRegistriesConfigNotFound {
		t.Errorf("LoadSystemRegistriesConfig() error = %v, want %v", err, ErrRegistriesConfigNotFound)
	}
}

func TestLoadSystemRegistriesConfig_WithDropIns(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "registries.conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("failed to create conf.d directory: %v", err)
	}

	// Create base config
	baseConfig := `
unqualified-search-registries = ["docker.io"]
short-name-mode = "permissive"

[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
`
	baseConfigPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(baseConfigPath, []byte(baseConfig), 0644); err != nil {
		t.Fatalf("failed to write base config: %v", err)
	}

	// Create drop-in config
	dropInConfig := `
[[registry]]
prefix = "quay.io"
insecure = true
`
	dropInPath := filepath.Join(confDir, "01-quay.conf")
	if err := os.WriteFile(dropInPath, []byte(dropInConfig), 0644); err != nil {
		t.Fatalf("failed to write drop-in config: %v", err)
	}

	// Save and restore system paths
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	systemRegistriesConfPath = baseConfigPath
	systemRegistriesConfDirPath = confDir

	// Ensure user config doesn't exist
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", "/nonexistent")
	defer os.Setenv("HOME", oldHome)

	config, err := LoadSystemRegistriesConfig()
	if err != nil {
		t.Fatalf("LoadSystemRegistriesConfig() error = %v", err)
	}

	// Check that both registries are present
	if len(config.Registries) != 2 {
		t.Errorf("len(Registries) = %d, want 2", len(config.Registries))
	}

	// Find quay.io registry
	var quayReg *Registry
	for i := range config.Registries {
		if config.Registries[i].Prefix == "quay.io" {
			quayReg = &config.Registries[i]
			break
		}
	}
	if quayReg == nil {
		t.Error("quay.io registry not found after merge")
	} else if !quayReg.Insecure {
		t.Error("quay.io should be insecure")
	}
}

// Helper function to check if a string contains another string.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
