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

package authtype

import (
	"encoding/base64"
	"testing"
)

func TestEncodeAuth(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     string
	}{
		{
			name:     "both empty",
			username: "",
			password: "",
			want:     "",
		},
		{
			name:     "username only",
			username: "user",
			password: "",
			want:     base64.StdEncoding.EncodeToString([]byte("user:")),
		},
		{
			name:     "password only",
			username: "",
			password: "pass",
			want:     base64.StdEncoding.EncodeToString([]byte(":pass")),
		},
		{
			name:     "both set",
			username: "user",
			password: "pass",
			want:     base64.StdEncoding.EncodeToString([]byte("user:pass")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeAuth(tt.username, tt.password)
			if got != tt.want {
				t.Errorf("EncodeAuth() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthConfig_DecodeAuth(t *testing.T) {
	tests := []struct {
		name         string
		auth         string
		wantUsername string
		wantPassword string
		wantErr      bool
	}{
		{
			name:         "empty auth",
			auth:         "",
			wantUsername: "",
			wantPassword: "",
		},
		{
			name:         "valid credentials",
			auth:         base64.StdEncoding.EncodeToString([]byte("user:pass")),
			wantUsername: "user",
			wantPassword: "pass",
		},
		{
			name:         "colon in password",
			auth:         base64.StdEncoding.EncodeToString([]byte("user:p:a:s:s")),
			wantUsername: "user",
			wantPassword: "p:a:s:s",
		},
		{
			name:    "invalid base64",
			auth:    "not-valid-base64!!!",
			wantErr: true,
		},
		{
			name:    "no colon separator",
			auth:    base64.StdEncoding.EncodeToString([]byte("userpassword")),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := AuthConfig{Auth: tt.auth}
			u, p, err := ac.DecodeAuth()
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeAuth() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if u != tt.wantUsername {
					t.Errorf("DecodeAuth() username = %q, want %q", u, tt.wantUsername)
				}
				if p != tt.wantPassword {
					t.Errorf("DecodeAuth() password = %q, want %q", p, tt.wantPassword)
				}
			}
		})
	}
}

func TestNewAuthConfig(t *testing.T) {
	username := "user"
	password := "pass"
	refreshToken := "myrefresh"
	accessToken := "myaccess"

	ac := NewAuthConfig(username, password, refreshToken, accessToken)

	if ac.IdentityToken != refreshToken {
		t.Errorf("NewAuthConfig() IdentityToken = %q, want %q", ac.IdentityToken, refreshToken)
	}
	if ac.RegistryToken != accessToken {
		t.Errorf("NewAuthConfig() RegistryToken = %q, want %q", ac.RegistryToken, accessToken)
	}
	u, p, err := ac.DecodeAuth()
	if err != nil {
		t.Fatalf("DecodeAuth() error = %v", err)
	}
	if u != username {
		t.Errorf("decoded username = %q, want %q", u, username)
	}
	if p != password {
		t.Errorf("decoded password = %q, want %q", p, password)
	}
}
