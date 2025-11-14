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

import "testing"

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
