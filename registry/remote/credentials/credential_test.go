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
)

func TestStaticCredential_BasicAuth(t *testing.T) {
	ctx := context.Background()
	registry := "example.com:5000"
	expectedCred := Credential{
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
	if cred != EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, EmptyCredential)
	}
}

func TestStaticCredential_BearerToken(t *testing.T) {
	ctx := context.Background()
	registry := "registry.example.com"
	expectedCred := Credential{
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
	expectedCred := Credential{
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
	if cred != EmptyCredential {
		t.Errorf("StaticCredentialFunc() for docker.io = %+v, want %+v", cred, EmptyCredential)
	}
}

func TestStaticCredential_EmptyCredential(t *testing.T) {
	ctx := context.Background()
	registry := "test.registry.io"

	credFunc := StaticCredentialFunc(registry, EmptyCredential)

	cred, err := credFunc(ctx, registry)
	if err != nil {
		t.Fatalf("StaticCredentialFunc() error = %v, want nil", err)
	}
	if cred != EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, EmptyCredential)
	}
}

func TestStaticCredential_MixedCredential(t *testing.T) {
	ctx := context.Background()
	registry := "mixed.example.com"
	expectedCred := Credential{
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
	expectedCred := Credential{
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
	if cred != EmptyCredential {
		t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, EmptyCredential)
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

	expectedCred := Credential{
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
				if cred != EmptyCredential {
					t.Errorf("StaticCredentialFunc() = %+v, want %+v", cred, EmptyCredential)
				}
			}
		})
	}
}

func TestCredentialFunc_Interface(t *testing.T) {
	// Test that CredentialFunc is a valid function type
	var credFunc CredentialFunc = func(ctx context.Context, hostport string) (Credential, error) {
		return EmptyCredential, nil
	}

	ctx := context.Background()
	cred, err := credFunc(ctx, "test.example.com")
	if err != nil {
		t.Fatalf("CredentialFunc() error = %v, want nil", err)
	}
	if cred != EmptyCredential {
		t.Errorf("CredentialFunc() = %+v, want %+v", cred, EmptyCredential)
	}
}

func TestCredential(t *testing.T) {
	tests := []struct {
		name    string
		authCfg AuthConfig
		want    Credential
		wantErr bool
	}{
		{
			name: "Username and password",
			authCfg: AuthConfig{
				Username: "username",
				Password: "password",
			},
			want: Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name: "Identity token",
			authCfg: AuthConfig{
				IdentityToken: "identity_token",
			},
			want: Credential{
				RefreshToken: "identity_token",
			},
		},
		{
			name: "Registry token",
			authCfg: AuthConfig{
				RegistryToken: "registry_token",
			},
			want: Credential{
				AccessToken: "registry_token",
			},
		},
		{
			name: "All fields",
			authCfg: AuthConfig{
				Username:      "username",
				Password:      "password",
				IdentityToken: "identity_token",
				RegistryToken: "registry_token",
			},
			want: Credential{
				Username:     "username",
				Password:     "password",
				RefreshToken: "identity_token",
				AccessToken:  "registry_token",
			},
		},
		{
			name:    "Empty auth config",
			authCfg: AuthConfig{},
			want:    Credential{},
		},
		{
			name: "Auth field overrides username and password",
			authCfg: AuthConfig{
				Auth:     "dXNlcm5hbWU6cGFzc3dvcmQ=", // username:password
				Username: "old_username",
				Password: "old_password",
			},
			want: Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name: "Auth field with identity and registry tokens",
			authCfg: AuthConfig{
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=", // username:password
				IdentityToken: "identity_token",
				RegistryToken: "registry_token",
			},
			want: Credential{
				Username:     "username",
				Password:     "password",
				RefreshToken: "identity_token",
				AccessToken:  "registry_token",
			},
		},
		{
			name: "Invalid auth field",
			authCfg: AuthConfig{
				Auth: "invalid_base64!@#",
			},
			want:    EmptyCredential,
			wantErr: true,
		},
		{
			name: "Auth field bad format",
			authCfg: AuthConfig{
				Auth: "d2hhdGV2ZXI=", // whatever (no colon)
			},
			want:    EmptyCredential,
			wantErr: true,
		},
		{
			name: "Auth field username only",
			authCfg: AuthConfig{
				Auth: "dXNlcm5hbWU6", // username:
			},
			want: Credential{
				Username: "username",
				Password: "",
			},
		},
		{
			name: "Auth field password only",
			authCfg: AuthConfig{
				Auth: "OnBhc3N3b3Jk", // :password
			},
			want: Credential{
				Username: "",
				Password: "password",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewCredential(tt.authCfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Credential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Credential() = %v, want %v", got, tt.want)
			}
		})
	}
}
