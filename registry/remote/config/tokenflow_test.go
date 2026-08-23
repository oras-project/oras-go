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
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func TestNewRegistryProperties_TokenFlow(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    properties.TokenFlow
		wantErr bool
	}{
		{"unset leaves the client default", "", properties.TokenFlowDefault, false},
		{"oauth2", "oauth2", properties.TokenFlowOAuth2, false},
		{"distribution", "distribution", properties.TokenFlowDistribution, false},
		// Rejected rather than ignored: a typo must not silently authenticate
		// a way the operator did not ask for.
		{"unrecognized is rejected", "legacy", properties.TokenFlowDefault, true},
		{"case sensitive", "OAuth2", properties.TokenFlowDefault, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RegistriesConfig{
				Registries: []Registry{
					{Prefix: "example.com", TokenFlow: tt.value},
				},
				Aliases: map[string]string{},
			}

			props, err := NewRegistryProperties("example.com/image:v1", cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NewRegistryProperties() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), "token-flow") {
					t.Errorf("error = %v, want it to name the token-flow field", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
			}
			if props.Attributes.TokenFlow != tt.want {
				t.Errorf("Attributes.TokenFlow = %v, want %v", props.Attributes.TokenFlow, tt.want)
			}
		})
	}
}

func TestLoadRegistriesConfig_TokenFlow(t *testing.T) {
	content := `
[[registry]]
prefix = "legacy.example.com"
token-flow = "distribution"
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
	if got := cfg.Registries[0].TokenFlow; got != "distribution" {
		t.Errorf("TokenFlow = %q, want %q", got, "distribution")
	}
}
