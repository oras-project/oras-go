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

import "testing"

func Test_encodeAuth(t *testing.T) {
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
			if got := encodeAuth(tt.username, tt.password); got != tt.want {
				t.Errorf("encodeAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_decodeAuth(t *testing.T) {
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
			gotUsername, gotPassword, err := decodeAuth(tt.authStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeAuth() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotUsername != tt.username {
				t.Errorf("decodeAuth() got = %v, want %v", gotUsername, tt.username)
			}
			if gotPassword != tt.password {
				t.Errorf("decodeAuth() got1 = %v, want %v", gotPassword, tt.password)
			}
		})
	}
}

func TestNewAuthConfig(t *testing.T) {
	tests := []struct {
		name string
		cred Credential
		want authConfig
	}{
		{
			name: "Basic auth credential",
			cred: Credential{
				Username: "testuser",
				Password: "testpass",
			},
			want: authConfig{
				Auth:          "dGVzdHVzZXI6dGVzdHBhc3M=", // base64("testuser:testpass")
				IdentityToken: "",
				RegistryToken: "",
			},
		},
		{
			name: "Bearer token credential",
			cred: Credential{
				RefreshToken: "refresh123",
				AccessToken:  "access456",
			},
			want: authConfig{
				Auth:          "",
				IdentityToken: "refresh123",
				RegistryToken: "access456",
			},
		},
		{
			name: "Mixed credential",
			cred: Credential{
				Username:     "user",
				Password:     "pass",
				RefreshToken: "refresh",
				AccessToken:  "access",
			},
			want: authConfig{
				Auth:          "dXNlcjpwYXNz", // base64("user:pass")
				IdentityToken: "refresh",
				RegistryToken: "access",
			},
		},
		{
			name: "Empty credential",
			cred: EmptyCredential,
			want: authConfig{
				Auth:          "",
				IdentityToken: "",
				RegistryToken: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewAuthConfig(tt.cred)
			if got.Auth != tt.want.Auth {
				t.Errorf("NewAuthConfig().Auth = %v, want %v", got.Auth, tt.want.Auth)
			}
			if got.IdentityToken != tt.want.IdentityToken {
				t.Errorf("NewAuthConfig().IdentityToken = %v, want %v", got.IdentityToken, tt.want.IdentityToken)
			}
			if got.RegistryToken != tt.want.RegistryToken {
				t.Errorf("NewAuthConfig().RegistryToken = %v, want %v", got.RegistryToken, tt.want.RegistryToken)
			}
		})
	}
}

func TestAuthConfig_Credential(t *testing.T) {
	tests := []struct {
		name    string
		ac      authConfig
		want    Credential
		wantErr bool
	}{
		{
			name: "Auth field takes precedence",
			ac: authConfig{
				Auth:     "dGVzdDpwYXNz", // base64("test:pass")
				Username: "old_user",
				Password: "old_pass",
			},
			want: Credential{
				Username: "test",
				Password: "pass",
			},
		},
		{
			name: "Fallback to Username/Password fields",
			ac: authConfig{
				Username: "legacy_user",
				Password: "legacy_pass",
			},
			want: Credential{
				Username: "legacy_user",
				Password: "legacy_pass",
			},
		},
		{
			name: "Bearer tokens",
			ac: authConfig{
				IdentityToken: "identity123",
				RegistryToken: "registry456",
			},
			want: Credential{
				RefreshToken: "identity123",
				AccessToken:  "registry456",
			},
		},
		{
			name: "Mixed auth config",
			ac: authConfig{
				Auth:          "dXNlcjpwYXNz", // base64("user:pass")
				IdentityToken: "refresh_token",
				RegistryToken: "access_token",
				Username:      "ignored_user",  // should be ignored because Auth is present
				Password:      "ignored_pass", // should be ignored because Auth is present
			},
			want: Credential{
				Username:     "user",
				Password:     "pass",
				RefreshToken: "refresh_token",
				AccessToken:  "access_token",
			},
		},
		{
			name: "Invalid Auth field",
			ac: authConfig{
				Auth: "invalid_base64!",
			},
			wantErr: true,
		},
		{
			name: "Auth field with bad format",
			ac: authConfig{
				Auth: "dmFsaWRfYmFzZTY0", // base64("valid_base64") but no colon
			},
			wantErr: true,
		},
		{
			name: "Empty auth config",
			ac:   authConfig{},
			want: EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.ac.Credential()
			if (err != nil) != tt.wantErr {
				t.Errorf("authConfig.Credential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("authConfig.Credential() = %+v, want %+v", got, tt.want)
			}
		})
	}
}