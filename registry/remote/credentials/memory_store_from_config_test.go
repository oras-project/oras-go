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
	"errors"
	"os"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/internal/config"
)

func TestMemoryStore_Create_fromInvalidConfig(t *testing.T) {
	f, err := os.ReadFile("testdata/invalid_auths_entry_config.json")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	_, err = NewMemoryStoreFromDockerConfig(f)
	if !errors.Is(err, config.ErrInvalidConfigFormat) {
		t.Fatalf("Error: %s is expected", config.ErrInvalidConfigFormat)
	}
}

func TestMemoryStore_Get_validConfig(t *testing.T) {
	ctx := context.Background()
	f, err := os.ReadFile("testdata/valid_auths_config.json")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	cfg, err := NewMemoryStoreFromDockerConfig(f)
	if err != nil {
		t.Fatalf("NewMemoryStoreFromConfig() error = %v", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          auth.Credential
		wantErr       bool
	}{
		{
			name:          "Username and password",
			serverAddress: "registry1.example.com",
			want: auth.Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:          "Identity token",
			serverAddress: "registry2.example.com",
			want: auth.Credential{
				RefreshToken: "identity_token",
			},
		},
		{
			name:          "Registry token",
			serverAddress: "registry3.example.com",
			want: auth.Credential{
				AccessToken: "registry_token",
			},
		},
		{
			name:          "Username and password, identity token and registry token",
			serverAddress: "registry4.example.com",
			want: auth.Credential{
				Username:     "username",
				Password:     "password",
				RefreshToken: "identity_token",
				AccessToken:  "registry_token",
			},
		},
		{
			name:          "Empty credential",
			serverAddress: "registry5.example.com",
			want:          auth.EmptyCredential,
		},
		{
			name:          "Username and password, no auth",
			serverAddress: "registry6.example.com",
			want: auth.Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:          "Auth overriding Username and password",
			serverAddress: "registry7.example.com",
			want: auth.Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:          "Not in auths",
			serverAddress: "foo.example.com",
			want:          auth.EmptyCredential,
		},
		{
			name:          "No record",
			serverAddress: "registry999.example.com",
			want:          auth.EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" MemoryStore.Get()", func(t *testing.T) {
			got, err := cfg.Get(ctx, tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("MemoryStore.Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MemoryStore.Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryStore_Get_emptyConfig(t *testing.T) {
	ctx := context.Background()
	emptyValidJson := []byte("{}")
	cfg, err := NewMemoryStoreFromDockerConfig(emptyValidJson)
	if err != nil {
		t.Fatal("NewMemoryStoreFromConfig() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          auth.Credential
		wantErr       error
	}{
		{
			name:          "Not found",
			serverAddress: "registry.example.com",
			want:          auth.EmptyCredential,
			wantErr:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.Get(ctx, tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("MemoryStore.Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MemoryStore.Get() = %v, want %v", got, tt.want)
			}
		})
	}
}
