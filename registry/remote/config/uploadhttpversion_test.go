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
	"testing"
)

func TestNewRegistryProperties_UploadHTTPVersion(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"unset defaults to HTTP/1.1", "", "1.1", false},
		{"1.1", "1.1", "1.1", false},
		{"2", "2", "2", false},
		{"unrecognized is rejected", "3", "", true},
		{"HTTP prefix is rejected", "HTTP/2", "", true},
		{"2.0 is rejected", "2.0", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RegistriesConfig{
				Registries: []Registry{
					{Prefix: "example.com", UploadHTTPVersion: tt.value},
				},
				Aliases: map[string]string{},
			}

			props, err := NewRegistryProperties("example.com/image:v1", cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NewRegistryProperties() error = nil, want an error")
				}
				want := `invalid upload-http-version "` + tt.value + `" for registry "example.com": must be "1.1" or "2"`
				if err.Error() != want {
					t.Errorf("error = %q, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
			}
			if got := props.Transport.UploadHTTPVersion; got != tt.want {
				t.Errorf("Transport.UploadHTTPVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRegistryProperties_UploadHTTPVersion_NoMatchingRegistry(t *testing.T) {
	cfg := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "other.example.com", UploadHTTPVersion: "2"},
		},
		Aliases: map[string]string{},
	}
	props, err := NewRegistryProperties("example.com/image:v1", cfg)
	if err != nil {
		t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
	}
	// Without a matching entry the field is left empty, which ClientBuilder
	// treats as the default.
	if got := props.Transport.UploadHTTPVersion; got != "" {
		t.Errorf("Transport.UploadHTTPVersion = %q, want empty", got)
	}
}

func TestLoadRegistriesConfig_UploadHTTPVersion(t *testing.T) {
	content := `
[[registry]]
prefix = "registry.example.com"
upload-http-version = "2"
`
	path := filepath.Join(t.TempDir(), "registries.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadRegistriesConfig(path)
	if err != nil {
		t.Fatalf("LoadRegistriesConfig() error: %v", err)
	}
	if len(cfg.Registries) != 1 {
		t.Fatalf("got %d registries, want 1", len(cfg.Registries))
	}
	if got := cfg.Registries[0].UploadHTTPVersion; got != "2" {
		t.Errorf("UploadHTTPVersion = %q, want %q", got, "2")
	}

	props, err := cfg.RegistryProperties("registry.example.com/repo:v1")
	if err != nil {
		t.Fatalf("RegistryProperties() error: %v", err)
	}
	if got := props.Transport.UploadHTTPVersion; got != "2" {
		t.Errorf("Transport.UploadHTTPVersion = %q, want %q", got, "2")
	}
}
