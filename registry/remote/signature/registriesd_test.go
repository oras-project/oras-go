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

import "testing"

func TestRegistriesDConfig_GetLookasideURLs(t *testing.T) {
	cfg := &RegistriesDConfig{
		DefaultDocker: &RegistriesDDockerConfig{
			Lookaside: "https://sigstore.example.com",
		},
		Docker: map[string]RegistriesDDockerConfig{
			"registry.example.com": {
				Lookaside: "https://sigstore.example.com/registry",
			},
			"registry.example.com/namespace": {
				Lookaside:        "https://sigstore.example.com/namespace",
				LookasideStaging: "https://sigstore-staging.example.com/namespace",
			},
		},
	}

	tests := []struct {
		name      string
		scope     string
		wantRead  string
		wantWrite string
	}{
		{
			name:      "Exact match on namespace",
			scope:     "registry.example.com/namespace",
			wantRead:  "https://sigstore.example.com/namespace",
			wantWrite: "https://sigstore-staging.example.com/namespace",
		},
		{
			name:      "Prefix match on namespace",
			scope:     "registry.example.com/namespace/repo",
			wantRead:  "https://sigstore.example.com/namespace",
			wantWrite: "https://sigstore-staging.example.com/namespace",
		},
		{
			name:      "Exact match on registry",
			scope:     "registry.example.com",
			wantRead:  "https://sigstore.example.com/registry",
			wantWrite: "https://sigstore.example.com/registry",
		},
		{
			name:      "Registry prefix for unknown namespace",
			scope:     "registry.example.com/other-ns",
			wantRead:  "https://sigstore.example.com/registry",
			wantWrite: "https://sigstore.example.com/registry",
		},
		{
			name:      "Fallback to default",
			scope:     "unknown.example.com/repo",
			wantRead:  "https://sigstore.example.com",
			wantWrite: "https://sigstore.example.com",
		},
		{
			name:      "No match partial hostname",
			scope:     "registry.example.com.evil.com/repo",
			wantRead:  "https://sigstore.example.com",
			wantWrite: "https://sigstore.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRead, gotWrite := cfg.GetLookasideURLs(tt.scope)
			if gotRead != tt.wantRead {
				t.Errorf("GetLookasideURLs(%s) readURL = %v, want %v", tt.scope, gotRead, tt.wantRead)
			}
			if gotWrite != tt.wantWrite {
				t.Errorf("GetLookasideURLs(%s) writeURL = %v, want %v", tt.scope, gotWrite, tt.wantWrite)
			}
		})
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Legacy(t *testing.T) {
	cfg := &RegistriesDConfig{
		DefaultDocker: &RegistriesDDockerConfig{
			SigStore: "https://legacy-sigstore.example.com",
		},
		Docker: map[string]RegistriesDDockerConfig{
			"legacy.example.com": {
				SigStore:        "https://legacy-sigstore.example.com/legacy",
				SigStoreStaging: "https://legacy-staging.example.com/legacy",
			},
		},
	}

	readURL, writeURL := cfg.GetLookasideURLs("legacy.example.com/repo")
	if readURL != "https://legacy-sigstore.example.com/legacy" {
		t.Errorf("readURL = %v, want https://legacy-sigstore.example.com/legacy", readURL)
	}
	if writeURL != "https://legacy-staging.example.com/legacy" {
		t.Errorf("writeURL = %v, want https://legacy-staging.example.com/legacy", writeURL)
	}

	// Unknown scope falls back to default.
	readURL, _ = cfg.GetLookasideURLs("unknown.example.com")
	if readURL != "https://legacy-sigstore.example.com" {
		t.Errorf("readURL = %v, want https://legacy-sigstore.example.com", readURL)
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Nil(t *testing.T) {
	var cfg *RegistriesDConfig
	readURL, writeURL := cfg.GetLookasideURLs("anything")
	if readURL != "" || writeURL != "" {
		t.Errorf("nil config GetLookasideURLs() = (%v, %v), want (\"\", \"\")", readURL, writeURL)
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Empty(t *testing.T) {
	cfg := &RegistriesDConfig{
		Docker: make(map[string]RegistriesDDockerConfig),
	}
	readURL, writeURL := cfg.GetLookasideURLs("anything")
	if readURL != "" || writeURL != "" {
		t.Errorf("empty config GetLookasideURLs() = (%v, %v), want (\"\", \"\")", readURL, writeURL)
	}
}
