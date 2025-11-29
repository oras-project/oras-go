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
)

func TestCredential_EmptyCredential(t *testing.T) {
	// Test that EmptyCredential is indeed empty
	if EmptyCredential.Username != "" {
		t.Errorf("EmptyCredential.Username = %q, want empty string", EmptyCredential.Username)
	}
	if EmptyCredential.Password != "" {
		t.Errorf("EmptyCredential.Password = %q, want empty string", EmptyCredential.Password)
	}
	if EmptyCredential.RefreshToken != "" {
		t.Errorf("EmptyCredential.RefreshToken = %q, want empty string", EmptyCredential.RefreshToken)
	}
	if EmptyCredential.AccessToken != "" {
		t.Errorf("EmptyCredential.AccessToken = %q, want empty string", EmptyCredential.AccessToken)
	}
}

func TestCredential_Fields(t *testing.T) {
	cred := Credential{
		Username:     "testuser",
		Password:     "testpass",
		RefreshToken: "refresh123",
		AccessToken:  "access456",
	}

	if cred.Username != "testuser" {
		t.Errorf("Username = %q, want %q", cred.Username, "testuser")
	}
	if cred.Password != "testpass" {
		t.Errorf("Password = %q, want %q", cred.Password, "testpass")
	}
	if cred.RefreshToken != "refresh123" {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, "refresh123")
	}
	if cred.AccessToken != "access456" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "access456")
	}
}

func TestCredential_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cred Credential
		want bool
	}{
		{
			name: "empty credential",
			cred: Credential{},
			want: true,
		},
		{
			name: "EmptyCredential variable",
			cred: EmptyCredential,
			want: true,
		},
		{
			name: "credential with username only",
			cred: Credential{
				Username: "testuser",
			},
			want: false,
		},
		{
			name: "credential with password only",
			cred: Credential{
				Password: "testpass",
			},
			want: false,
		},
		{
			name: "credential with refresh token only",
			cred: Credential{
				RefreshToken: "refresh123",
			},
			want: false,
		},
		{
			name: "credential with access token only",
			cred: Credential{
				AccessToken: "access456",
			},
			want: false,
		},
		{
			name: "credential with all fields",
			cred: Credential{
				Username:     "testuser",
				Password:     "testpass",
				RefreshToken: "refresh123",
				AccessToken:  "access456",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cred.IsEmpty(); got != tt.want {
				t.Errorf("Credential.IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}
