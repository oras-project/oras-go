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

package credentials

import (
	"context"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func TestStaticCredential_BasicAuth(t *testing.T) {
	ctx := context.Background()
	registry := "example.com:5000"
	expectedCred := properties.Credential{
		Username: "testuser",
		Password: "testpass",
	}

	credFunc := StaticCredentialFunc(registry, expectedCred)

	// Test matching registry
	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != expectedCred {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, expectedCred)
	}

	// Test non-matching registry
	cred, err = credFunc(ctx, "different.com:5000")
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != properties.EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, properties.EmptyCredential)
	}
}

func TestStaticCredential_BearerToken(t *testing.T) {
	ctx := context.Background()
	registry := "registry.example.com"
	expectedCred := properties.Credential{
		RefreshToken: "refresh_token_123",
		AccessToken:  "access_token_456",
	}

	credFunc := StaticCredentialFunc(registry, expectedCred)

	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != expectedCred {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, expectedCred)
	}
}

func TestStaticCredential_DockerIORedirect(t *testing.T) {
	ctx := context.Background()
	expectedCred := properties.Credential{
		Username: "dockeruser",
		Password: "dockerpass",
	}

	// Create credential function for docker.io
	credFunc := StaticCredentialFunc("docker.io", expectedCred)

	// Test that docker.io is redirected to registry-1.docker.io
	cred, err := credFunc(ctx, "registry-1.docker.io")
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != expectedCred {
		t.Errorf("StaticCredentialFunc() for registry-1.docker.io = %+v, want %+v", cred, expectedCred)
	}

	// Test that docker.io itself doesn't match (because it gets redirected)
	cred, err = credFunc(ctx, "docker.io")
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != properties.EmptyCredential {
		t.Errorf("StaticCredentialFunc() for docker.io = %+v, want %+v", cred, properties.EmptyCredential)
	}
}

func TestStaticCredential_EmptyCredential(t *testing.T) {
	ctx := context.Background()
	registry := "test.registry.io"

	credFunc := StaticCredentialFunc(registry, properties.EmptyCredential)

	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != properties.EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, properties.EmptyCredential)
	}
}

func TestStaticCredential_MixedCredential(t *testing.T) {
	ctx := context.Background()
	registry := "mixed.example.com"
	expectedCred := properties.Credential{
		Username:     "mixeduser",
		RefreshToken: "mixed_refresh",
	}

	credFunc := StaticCredentialFunc(registry, expectedCred)

	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != expectedCred {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, expectedCred)
	}
}

func TestStaticCredential_CaseSensitive(t *testing.T) {
	ctx := context.Background()
	registry := "Example.Com:5000"
	expectedCred := properties.Credential{
		Username: "testuser",
		Password: "testpass",
	}

	credFunc := StaticCredentialFunc(registry, expectedCred)

	// Test exact match (case-sensitive)
	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != expectedCred {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, expectedCred)
	}

	// Test different case should not match
	cred, err = credFunc(ctx, "example.com:5000")
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != properties.EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, properties.EmptyCredential)
	}
}

func TestStaticCredential_WithPort(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		registry    string
		hostport    string
		shouldMatch bool
	}{
		{
			name:        "exact match with port",
			registry:    "example.com:5000",
			hostport:    "example.com:5000",
			shouldMatch: true,
		},
		{
			name:        "different port",
			registry:    "example.com:5000",
			hostport:    "example.com:443",
			shouldMatch: false,
		},
		{
			name:        "missing port in hostport",
			registry:    "example.com:5000",
			hostport:    "example.com",
			shouldMatch: false,
		},
		{
			name:        "missing port in registry",
			registry:    "example.com",
			hostport:    "example.com:443",
			shouldMatch: false,
		},
	}

	expectedCred := properties.Credential{
		Username: "testuser",
		Password: "testpass",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credFunc := StaticCredentialFunc(tt.registry, expectedCred)
			cred, err := credFunc(ctx, tt.hostport)
			if err != nil {
				t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
			}

			if tt.shouldMatch {
				if cred != expectedCred {
					t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, expectedCred)
				}
			} else {
				if cred != properties.EmptyCredential {
					t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, properties.EmptyCredential)
				}
			}
		})
	}
}

func TestCredentialFunc_Interface(t *testing.T) {
	// Test that CredentialFunc is a valid function type
	var credFunc CredentialFunc = func(ctx context.Context, hostport string) (properties.Credential, error) {
		return properties.EmptyCredential, nil
	}

	ctx := context.Background()
	cred, err := credFunc(ctx, "test.example.com")
	if err != nil {
		t.Fatalf("CredentialFunc() error = %v, want nil", err)
	}
	if cred != properties.EmptyCredential {
		t.Errorf("CredentialFunc() = %+v, want %+v", cred, properties.EmptyCredential)
	}
}
