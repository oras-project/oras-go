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
			name: "config with oras-specific attributes",
			content: `
[[registry]]
prefix = "basic-auth.example.com"
force-basic-auth = true

[[registry]]
prefix = "referrers-supported.example.com"
referrers-api = "supported"

[[registry]]
prefix = "referrers-unsupported.example.com"
referrers-api = "unsupported"
`,
			want: &RegistriesConfig{
				Registries: []Registry{
					{
						Prefix:         "basic-auth.example.com",
						ForceBasicAuth: true,
					},
					{
						Prefix:       "referrers-supported.example.com",
						ReferrersAPI: "supported",
					},
					{
						Prefix:       "referrers-unsupported.example.com",
						ReferrersAPI: "unsupported",
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
			{Prefix: "quay.io", Insecure: true},               // Override
			{Prefix: "gcr.io", Location: "mirror.gcr.io"},     // Add new
		},
		Aliases: map[string]string{
			"nginx":  "quay.io/nginx/nginx",              // Override
			"ubuntu": "docker.io/library/ubuntu",         // Add new
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

func TestLoadSystemRegistriesConfigWithStrategy_UAPI_DropInOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two drop-in directories: "system" and "user".
	sysDirPath := filepath.Join(tmpDir, "sys.conf.d")
	userDirPath := filepath.Join(tmpDir, "user.conf.d")
	if err := os.MkdirAll(sysDirPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userDirPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a drop-in with the same filename in both dirs; user should override sys.
	sysDrop := `
[[registry]]
prefix = "docker.io"
insecure = false
`
	userDrop := `
[[registry]]
prefix = "docker.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(sysDirPath, "10-docker.conf"), []byte(sysDrop), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDirPath, "10-docker.conf"), []byte(userDrop), 0644); err != nil {
		t.Fatal(err)
	}

	// Use loadDropInConfigsUAPI indirectly via loadRegistriesConfigFromResolver with a
	// custom resolver. Since that function isn't exported, test it through
	// LoadSystemRegistriesConfigWithStrategy by manipulating system paths.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()
	systemRegistriesConfPath = "/nonexistent"
	systemRegistriesConfDirPath = sysDirPath

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Create user registries.conf.d at ~/.config/containers/registries.conf.d
	userConfD := filepath.Join(tmpDir, ".config", "containers", "registries.conf.d")
	if err := os.MkdirAll(userConfD, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userConfD, "10-docker.conf"), []byte(userDrop), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadSystemRegistriesConfig()
	if err != nil {
		t.Fatalf("LoadSystemRegistriesConfig() error = %v", err)
	}

	var dockerReg *Registry
	for i := range config.Registries {
		if config.Registries[i].Prefix == "docker.io" {
			dockerReg = &config.Registries[i]
			break
		}
	}
	if dockerReg == nil {
		t.Fatal("docker.io registry not found")
	}
	if !dockerReg.Insecure {
		t.Error("user drop-in should override system drop-in: Insecure should be true")
	}
}

func TestLoadSystemRegistriesConfigWithStrategy_ContainersImage(t *testing.T) {
	tmpDir := t.TempDir()

	// The ContainersImage resolver uses $HOME/.config/containers/registries.conf.
	// Create user config at that location.
	userConfigDir := filepath.Join(tmpDir, ".config", "containers")
	if err := os.MkdirAll(userConfigDir, 0755); err != nil {
		t.Fatal(err)
	}

	userConfig := `
unqualified-search-registries = ["docker.io"]
short-name-mode = "permissive"

[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
`
	if err := os.WriteFile(filepath.Join(userConfigDir, "registries.conf"), []byte(userConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create user drop-in directory.
	userDropInDir := filepath.Join(userConfigDir, "registries.conf.d")
	if err := os.MkdirAll(userDropInDir, 0755); err != nil {
		t.Fatal(err)
	}

	dropIn := `
[[registry]]
prefix = "quay.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(userDropInDir, "01-quay.conf"), []byte(dropIn), 0644); err != nil {
		t.Fatal(err)
	}

	// Override system paths to nonexistent.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()
	systemRegistriesConfPath = filepath.Join(tmpDir, "nonexistent")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "nonexistent.d")

	t.Setenv("HOME", tmpDir)

	config, err := LoadSystemRegistriesConfigWithStrategy(StrategyContainersImage)
	if err != nil {
		t.Fatalf("LoadSystemRegistriesConfigWithStrategy() error = %v", err)
	}

	if len(config.Registries) != 2 {
		t.Errorf("len(Registries) = %d, want 2", len(config.Registries))
	}

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

func TestLoadSystemRegistriesConfigWithStrategy_UAPI(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a main config that the UAPI resolver should find.
	// We set up the user config at ~/.config/containers/registries.conf
	userConfigDir := filepath.Join(tmpDir, ".config", "containers")
	if err := os.MkdirAll(userConfigDir, 0755); err != nil {
		t.Fatal(err)
	}

	userConfig := `
unqualified-search-registries = ["quay.io"]

[[registry]]
prefix = "quay.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(userConfigDir, "registries.conf"), []byte(userConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up drop-in directory.
	userDropInDir := filepath.Join(userConfigDir, "registries.conf.d")
	if err := os.MkdirAll(userDropInDir, 0755); err != nil {
		t.Fatal(err)
	}
	dropIn := `
[[registry]]
prefix = "gcr.io"
location = "mirror.gcr.io"
`
	if err := os.WriteFile(filepath.Join(userDropInDir, "10-gcr.conf"), []byte(dropIn), 0644); err != nil {
		t.Fatal(err)
	}

	// Override system paths to nonexistent so only user config is found.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()
	systemRegistriesConfPath = filepath.Join(tmpDir, "nonexistent")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "nonexistent.d")

	t.Setenv("HOME", tmpDir)

	config, err := LoadSystemRegistriesConfigWithStrategy(StrategyUAPI)
	if err != nil {
		t.Fatalf("LoadSystemRegistriesConfigWithStrategy(UAPI) error = %v", err)
	}

	if config == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoadSystemRegistriesConfigWithStrategy_NotFound(t *testing.T) {
	// Skip if the system registries.conf exists; this test requires that no
	// system-level file is present so it can verify the not-found path.
	if _, err := os.Stat("/etc/containers/registries.conf"); err == nil {
		t.Skip("skipping: /etc/containers/registries.conf exists on this system")
	}

	tmpDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmpDir, "nonexistent-home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "nonexistent-xdg"))

	_, err := LoadSystemRegistriesConfigWithStrategy(StrategyContainersImage)
	if err != ErrRegistriesConfigNotFound {
		t.Errorf("error = %v, want %v", err, ErrRegistriesConfigNotFound)
	}
}

func TestLoadRegistriesConfigFromResolver_MergeAll_MultipleMainConfigs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two main config files.
	mainConf1 := `
unqualified-search-registries = ["docker.io"]

[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
`
	mainConf2 := `
short-name-mode = "enforcing"

[[registry]]
prefix = "quay.io"
insecure = true
`
	path1 := filepath.Join(tmpDir, "main1.conf")
	path2 := filepath.Join(tmpDir, "main2.conf")
	if err := os.WriteFile(path1, []byte(mainConf1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte(mainConf2), 0644); err != nil {
		t.Fatal(err)
	}

	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()
	systemRegistriesConfPath = path1
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "nonexistent.d")

	// Create user config at standard location.
	userDir := filepath.Join(tmpDir, "home")
	userConfigDir := filepath.Join(userDir, ".config", "containers")
	if err := os.MkdirAll(userConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userConfigDir, "registries.conf"), []byte(mainConf2), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", userDir)

	config, err := LoadSystemRegistriesConfig()
	if err != nil {
		t.Fatalf("LoadSystemRegistriesConfig() error = %v", err)
	}

	// Should have merged both registries.
	if config.ShortNameMode != "enforcing" {
		t.Errorf("ShortNameMode = %q, want %q", config.ShortNameMode, "enforcing")
	}
}

func TestLoadDropInConfigsUAPI_FilenameOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two directories with the same filename.
	dir1 := filepath.Join(tmpDir, "dir1")
	dir2 := filepath.Join(tmpDir, "dir2")
	if err := os.MkdirAll(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0755); err != nil {
		t.Fatal(err)
	}

	// dir1 has docker.io as insecure=false.
	conf1 := `
[[registry]]
prefix = "docker.io"
insecure = false
`
	// dir2 has docker.io as insecure=true (should override).
	conf2 := `
[[registry]]
prefix = "docker.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(dir1, "10-docker.conf"), []byte(conf1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "10-docker.conf"), []byte(conf2), 0644); err != nil {
		t.Fatal(err)
	}

	// Also add a unique file in dir1.
	uniqueConf := `
[[registry]]
prefix = "quay.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(dir1, "20-quay.conf"), []byte(uniqueConf), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := loadDropInConfigsUAPI(nil, []string{dir1, dir2})
	if err != nil {
		t.Fatalf("loadDropInConfigsUAPI() error = %v", err)
	}

	if config == nil {
		t.Fatal("expected non-nil config")
	}

	// Should have docker.io from dir2 (override) and quay.io from dir1.
	if len(config.Registries) != 2 {
		t.Fatalf("len(Registries) = %d, want 2", len(config.Registries))
	}

	var dockerReg *Registry
	for i := range config.Registries {
		if config.Registries[i].Prefix == "docker.io" {
			dockerReg = &config.Registries[i]
			break
		}
	}
	if dockerReg == nil {
		t.Fatal("docker.io registry not found")
	}
	if !dockerReg.Insecure {
		t.Error("docker.io should be insecure (overridden by dir2)")
	}
}

func TestLoadDropInConfigsUAPI_SkipsNonConfFiles(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "dropin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a .conf file and a non-conf file and a directory.
	conf := `
[[registry]]
prefix = "docker.io"
`
	if err := os.WriteFile(filepath.Join(dir, "valid.conf"), []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a config"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir.conf"), 0755); err != nil {
		t.Fatal(err)
	}

	config, err := loadDropInConfigsUAPI(nil, []string{dir})
	if err != nil {
		t.Fatalf("loadDropInConfigsUAPI() error = %v", err)
	}

	if config == nil {
		t.Fatal("expected non-nil config")
	}
	if len(config.Registries) != 1 {
		t.Errorf("len(Registries) = %d, want 1 (only valid.conf)", len(config.Registries))
	}
}

func TestLoadDropInConfigsUAPI_EmptyDirs(t *testing.T) {
	config, err := loadDropInConfigsUAPI(nil, []string{"/nonexistent/dir1", "/nonexistent/dir2"})
	if err != nil {
		t.Fatalf("loadDropInConfigsUAPI() error = %v", err)
	}
	if config != nil {
		t.Error("expected nil config when all dirs are nonexistent")
	}
}

func TestLoadDropInConfigsUAPI_NilBaseConfig(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "dropin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	conf := `
[[registry]]
prefix = "docker.io"
location = "mirror.example.com"
`
	if err := os.WriteFile(filepath.Join(dir, "10-docker.conf"), []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := loadDropInConfigsUAPI(nil, []string{dir})
	if err != nil {
		t.Fatalf("loadDropInConfigsUAPI() error = %v", err)
	}

	if config == nil {
		t.Fatal("expected non-nil config")
	}
	if len(config.Registries) != 1 {
		t.Errorf("len(Registries) = %d, want 1", len(config.Registries))
	}
	if config.Registries[0].Location != "mirror.example.com" {
		t.Errorf("Location = %q, want %q", config.Registries[0].Location, "mirror.example.com")
	}
}

func TestLoadDropInConfigsUAPI_MergesWithBaseConfig(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "dropin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	conf := `
[[registry]]
prefix = "quay.io"
insecure = true
`
	if err := os.WriteFile(filepath.Join(dir, "10-quay.conf"), []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}

	base := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"docker.io"},
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
		},
		Aliases: map[string]string{},
	}

	config, err := loadDropInConfigsUAPI(base, []string{dir})
	if err != nil {
		t.Fatalf("loadDropInConfigsUAPI() error = %v", err)
	}

	if len(config.Registries) != 2 {
		t.Errorf("len(Registries) = %d, want 2", len(config.Registries))
	}
}

func TestMergeRegistriesConfig_EmptyDropIn(t *testing.T) {
	base := &RegistriesConfig{
		UnqualifiedSearchRegistries: []string{"docker.io"},
		ShortNameMode:               "permissive",
		Registries: []Registry{
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
		},
		Aliases: map[string]string{
			"alpine": "docker.io/library/alpine",
		},
	}

	dropIn := &RegistriesConfig{
		Aliases: map[string]string{},
	}

	result := mergeRegistriesConfig(base, dropIn)

	// Should keep base values when drop-in is empty.
	if result.ShortNameMode != "permissive" {
		t.Errorf("ShortNameMode = %q, want %q", result.ShortNameMode, "permissive")
	}
	if len(result.UnqualifiedSearchRegistries) != 1 || result.UnqualifiedSearchRegistries[0] != "docker.io" {
		t.Errorf("UnqualifiedSearchRegistries = %v, want [docker.io]", result.UnqualifiedSearchRegistries)
	}
	if result.Aliases["alpine"] != "docker.io/library/alpine" {
		t.Errorf("Aliases[alpine] = %q, want %q", result.Aliases["alpine"], "docker.io/library/alpine")
	}
}

func TestRegistriesConfig_FindRegistry_EmptyPrefix(t *testing.T) {
	config := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "", Location: "default.example.com"},
			{Prefix: "docker.io", Location: "registry-1.docker.io"},
		},
	}

	got := config.FindRegistry("docker.io/nginx")
	if got == nil || got.Prefix != "docker.io" {
		t.Errorf("FindRegistry() should skip empty prefix, got %v", got)
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
