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

import "testing"

func TestNewRegistry(t *testing.T) {
	registry := "docker.io"
	namespace := "library"

	reg := NewRegistry(registry, namespace)

	if reg == nil {
		t.Fatal("NewRegistry() returned nil")
	}

	if reg.Registry != registry {
		t.Errorf("Registry = %q, want %q", reg.Registry, registry)
	}

	if reg.Namespace != namespace {
		t.Errorf("Namespace = %q, want %q", reg.Namespace, namespace)
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
	if !reg.Credential.IsEmpty() {
		t.Error("Credential should be empty by default")
	}
}

func TestNewRegistry_WithDifferentValues(t *testing.T) {
	tests := []struct {
		name      string
		registry  string
		namespace string
	}{
		{
			name:      "docker hub",
			registry:  "docker.io",
			namespace: "library",
		},
		{
			name:      "ghcr",
			registry:  "ghcr.io",
			namespace: "myorg",
		},
		{
			name:      "localhost",
			registry:  "localhost:5000",
			namespace: "test",
		},
		{
			name:      "empty namespace",
			registry:  "example.com",
			namespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry(tt.registry, tt.namespace)

			if reg.Registry != tt.registry {
				t.Errorf("Registry = %q, want %q", reg.Registry, tt.registry)
			}

			if reg.Namespace != tt.namespace {
				t.Errorf("Namespace = %q, want %q", reg.Namespace, tt.namespace)
			}
		})
	}
}

func TestRegistry_Fields(t *testing.T) {
	reg := &Registry{
		Registry:  "test.io",
		Namespace: "testns",
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
		Credential: Credential{
			Username: "testuser",
			Password: "testpass",
		},
		Attributes: Attributes{
			ReferrersAPI: ReferrersAPIYes,
		},
	}

	// Test Registry field
	if reg.Registry != "test.io" {
		t.Errorf("Registry = %q, want %q", reg.Registry, "test.io")
	}

	// Test Namespace field
	if reg.Namespace != "testns" {
		t.Errorf("Namespace = %q, want %q", reg.Namespace, "testns")
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
	if reg.Attributes.ReferrersAPI != ReferrersAPIYes {
		t.Errorf("Attributes.ReferrersAPI = %v, want %v", reg.Attributes.ReferrersAPI, ReferrersAPIYes)
	}
}
