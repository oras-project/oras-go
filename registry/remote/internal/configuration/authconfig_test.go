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
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func Test_EncodeAuth(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     string
	}{
		{
			name:     "Username and password",
			username: "username",
			password: "password",
			want:     "dXNlcm5hbWU6cGFzc3dvcmQ=",
		},
		{
			name:     "Username only",
			username: "username",
			password: "",
			want:     "dXNlcm5hbWU6",
		},
		{
			name:     "Password only",
			username: "",
			password: "password",
			want:     "OnBhc3N3b3Jk",
		},
		{
			name:     "Empty username and empty password",
			username: "",
			password: "",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeAuth(tt.username, tt.password); got != tt.want {
				t.Errorf("EncodeAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthConfig_DecodeAuth(t *testing.T) {
	tests := []struct {
		name     string
		authStr  string
		username string
		password string
		wantErr  bool
	}{
		{
			name:     "Valid base64",
			authStr:  "dXNlcm5hbWU6cGFzc3dvcmQ=", // username:password
			username: "username",
			password: "password",
		},
		{
			name:     "Valid base64, username only",
			authStr:  "dXNlcm5hbWU6", // username:
			username: "username",
		},
		{
			name:     "Valid base64, password only",
			authStr:  "OnBhc3N3b3Jk", // :password
			password: "password",
		},
		{
			name:     "Valid base64, bad format",
			authStr:  "d2hhdGV2ZXI=", // whatever
			username: "",
			password: "",
			wantErr:  true,
		},
		{
			name:     "Invalid base64",
			authStr:  "whatever",
			username: "",
			password: "",
			wantErr:  true,
		},
		{
			name:     "Empty string",
			authStr:  "",
			username: "",
			password: "",
			wantErr:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authCfg := AuthConfig{Auth: tt.authStr}
			gotUsername, gotPassword, err := authCfg.DecodeAuth()
			if (err != nil) != tt.wantErr {
				t.Errorf("AuthConfig.DecodeAuth() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotUsername != tt.username {
				t.Errorf("AuthConfig.DecodeAuth() got username = %v, want %v", gotUsername, tt.username)
			}
			if gotPassword != tt.password {
				t.Errorf("AuthConfig.DecodeAuth() got password = %v, want %v", gotPassword, tt.password)
			}
		})
	}
}

func TestCredential(t *testing.T) {
	tests := []struct {
		name    string
		authCfg AuthConfig
		want    properties.Credential
		wantErr bool
	}{
		{
			name: "Username and password",
			authCfg: AuthConfig{
				Username: "username",
				Password: "password",
			},
			want: properties.Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name: "Identity token",
			authCfg: AuthConfig{
				IdentityToken: "identity_token",
			},
			want: properties.Credential{
				RefreshToken: "identity_token",
			},
		},
		{
			name: "Registry token",
			authCfg: AuthConfig{
				RegistryToken: "registry_token",
			},
			want: properties.Credential{
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
			want: properties.Credential{
				Username:     "username",
				Password:     "password",
				RefreshToken: "identity_token",
				AccessToken:  "registry_token",
			},
		},
		{
			name:    "Empty auth config",
			authCfg: AuthConfig{},
			want:    properties.Credential{},
		},
		{
			name: "Auth field overrides username and password",
			authCfg: AuthConfig{
				Auth:     "dXNlcm5hbWU6cGFzc3dvcmQ=", // username:password
				Username: "old_username",
				Password: "old_password",
			},
			want: properties.Credential{
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
			want: properties.Credential{
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
			want:    properties.EmptyCredential,
			wantErr: true,
		},
		{
			name: "Auth field bad format",
			authCfg: AuthConfig{
				Auth: "d2hhdGV2ZXI=", // whatever (no colon)
			},
			want:    properties.EmptyCredential,
			wantErr: true,
		},
		{
			name: "Auth field username only",
			authCfg: AuthConfig{
				Auth: "dXNlcm5hbWU6", // username:
			},
			want: properties.Credential{
				Username: "username",
				Password: "",
			},
		},
		{
			name: "Auth field password only",
			authCfg: AuthConfig{
				Auth: "OnBhc3N3b3Jk", // :password
			},
			want: properties.Credential{
				Username: "",
				Password: "password",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.authCfg.Credential()
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
