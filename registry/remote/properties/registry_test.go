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

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

func TestNewRegistry(t *testing.T) {
	reference := "docker.io/library/alpine:latest"

	reg, err := NewRegistry(reference)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	if reg == nil {
		t.Fatal("NewRegistry() returned nil")
	}

	if reg.Reference.Registry != "docker.io" {
		t.Errorf("Reference.Registry = %q, want %q", reg.Reference.Registry, "docker.io")
	}

	if reg.Reference.Repository != "library/alpine" {
		t.Errorf("Reference.Repository = %q, want %q", reg.Reference.Repository, "library/alpine")
	}

	if reg.Reference.Tag != "latest" {
		t.Errorf("Reference.Tag = %q, want %q", reg.Reference.Tag, "latest")
	}

	// Test Transport defaults
	if reg.Transport.Insecure != false {
		t.Errorf("Transport.Insecure = %v, want false", reg.Transport.Insecure)
	}

	if reg.Transport.PlainHTTP != false {
		t.Errorf("Transport.PlainHTTP = %v, want false", reg.Transport.PlainHTTP)
	}

	if reg.Transport.HeaderFlags == nil {
		t.Error("Transport.HeaderFlags is nil, want initialized map")
	}

	if len(reg.Transport.HeaderFlags) != 0 {
		t.Errorf("Transport.HeaderFlags length = %d, want 0", len(reg.Transport.HeaderFlags))
	}

	// Test Attributes defaults
	if reg.Attributes.ReferrersAPI != ReferrersAPIUnknown {
		t.Errorf("Attributes.ReferrersAPI = %v, want %v", reg.Attributes.ReferrersAPI, ReferrersAPIUnknown)
	}

	// Test Credential defaults (should be empty)
	if reg.Credential != credentials.EmptyCredential {
		t.Error("Credential should be empty by default")
	}
}

func TestNewRegistry_InvalidReference(t *testing.T) {
	_, err := NewRegistry("invalid reference with spaces")
	if err == nil {
		t.Error("NewRegistry() should return error for invalid reference")
	}
}

func TestNewRegistry_WithDifferentValues(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		wantReg    string
		wantRepo   string
		wantTag    string
		wantDigest string
	}{
		{
			name:      "docker hub with tag",
			reference: "docker.io/library:v1",
			wantReg:   "docker.io",
			wantRepo:  "library",
			wantTag:   "v1",
		},
		{
			name:      "ghcr",
			reference: "ghcr.io/myorg/myrepo:latest",
			wantReg:   "ghcr.io",
			wantRepo:  "myorg/myrepo",
			wantTag:   "latest",
		},
		{
			name:      "localhost with port",
			reference: "localhost:5000/test:v1",
			wantReg:   "localhost:5000",
			wantRepo:  "test",
			wantTag:   "v1",
		},
		{
			name:       "with digest",
			reference:  "example.com/repo@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantReg:    "example.com",
			wantRepo:   "repo",
			wantDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := NewRegistry(tt.reference)
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}

			if reg.Reference.Registry != tt.wantReg {
				t.Errorf("Reference.Registry = %q, want %q", reg.Reference.Registry, tt.wantReg)
			}

			if reg.Reference.Repository != tt.wantRepo {
				t.Errorf("Reference.Repository = %q, want %q", reg.Reference.Repository, tt.wantRepo)
			}

			if reg.Reference.Tag != tt.wantTag {
				t.Errorf("Reference.Tag = %q, want %q", reg.Reference.Tag, tt.wantTag)
			}

			if reg.Reference.Digest != tt.wantDigest {
				t.Errorf("Reference.Digest = %q, want %q", reg.Reference.Digest, tt.wantDigest)
			}
		})
	}
}

func TestNewRegistryFromReference(t *testing.T) {
	ref := Reference{
		Registry:   "test.io",
		Repository: "testns",
		Tag:        "v1",
	}

	reg := NewRegistryFromReference(ref)

	if reg == nil {
		t.Fatal("NewRegistryFromReference() returned nil")
	}

	if reg.Reference.Registry != "test.io" {
		t.Errorf("Reference.Registry = %q, want %q", reg.Reference.Registry, "test.io")
	}

	if reg.Reference.Repository != "testns" {
		t.Errorf("Reference.Repository = %q, want %q", reg.Reference.Repository, "testns")
	}

	if reg.Reference.Tag != "v1" {
		t.Errorf("Reference.Tag = %q, want %q", reg.Reference.Tag, "v1")
	}
}

func TestRegistry_Fields(t *testing.T) {
	reg := &Registry{
		Reference: Reference{
			Registry:   "test.io",
			Repository: "testns",
			Tag:        "v1",
		},
		Transport: Transport{
			CACert:    "/path/to/ca.crt",
			Cert:      "/path/to/client.crt",
			Key:       "/path/to/client.key",
			Insecure:  true,
			PlainHTTP: true,
			HeaderFlags: map[string]string{
				"X-Custom-Header": "custom-value",
			},
		},
		Credential: credentials.Credential{
			Username: "testuser",
			Password: "testpass",
		},
		Attributes: Attributes{
			ReferrersAPI: ReferrersAPISupported,
		},
	}

	// Test Reference fields
	if reg.Reference.Registry != "test.io" {
		t.Errorf("Reference.Registry = %q, want %q", reg.Reference.Registry, "test.io")
	}

	if reg.Reference.Repository != "testns" {
		t.Errorf("Reference.Repository = %q, want %q", reg.Reference.Repository, "testns")
	}

	// Test Transport fields
	if reg.Transport.CACert != "/path/to/ca.crt" {
		t.Errorf("Transport.CACert = %q, want %q", reg.Transport.CACert, "/path/to/ca.crt")
	}
	if reg.Transport.Cert != "/path/to/client.crt" {
		t.Errorf("Transport.Cert = %q, want %q", reg.Transport.Cert, "/path/to/client.crt")
	}
	if reg.Transport.Key != "/path/to/client.key" {
		t.Errorf("Transport.Key = %q, want %q", reg.Transport.Key, "/path/to/client.key")
	}
	if !reg.Transport.Insecure {
		t.Error("Transport.Insecure = false, want true")
	}
	if !reg.Transport.PlainHTTP {
		t.Error("Transport.PlainHTTP = false, want true")
	}
	if reg.Transport.HeaderFlags["X-Custom-Header"] != "custom-value" {
		t.Errorf("Transport.HeaderFlags[X-Custom-Header] = %q, want %q",
			reg.Transport.HeaderFlags["X-Custom-Header"], "custom-value")
	}

	// Test Credential fields
	if reg.Credential.Username != "testuser" {
		t.Errorf("Credential.Username = %q, want %q", reg.Credential.Username, "testuser")
	}
	if reg.Credential.Password != "testpass" {
		t.Errorf("Credential.Password = %q, want %q", reg.Credential.Password, "testpass")
	}

	// Test Attributes fields
	if reg.Attributes.ReferrersAPI != ReferrersAPISupported {
		t.Errorf("Attributes.ReferrersAPI = %v, want %v", reg.Attributes.ReferrersAPI, ReferrersAPISupported)
	}
}
