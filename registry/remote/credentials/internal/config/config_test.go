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

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/internal/config/configtest"
)

func TestLoad_badPath(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name       string
		configPath string
		wantErr    bool
	}{
		{
			name:       "Path is a directory",
			configPath: tempDir,
			wantErr:    true,
		},
		{
			name:       "Empty file name",
			configPath: filepath.Join(tempDir, ""),
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestLoad_badFormat(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
		wantErr    bool
	}{
		{
			name:       "Bad JSON format",
			configPath: "../../testdata/bad_config",
			wantErr:    true,
		},
		{
			name:       "Invalid auths format",
			configPath: "../../testdata/invalid_auths_config.json",
			wantErr:    true,
		},
		{
			name:       "No auths field",
			configPath: "../../testdata/no_auths_config.json",
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestConfig_GetCredential_validConfig(t *testing.T) {
	cfg, err := Load("../../testdata/valid_auths_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
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
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetCredential(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetCredential_legacyConfig(t *testing.T) {
	cfg, err := Load("../../testdata/legacy_auths_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          auth.Credential
		wantErr       bool
	}{
		{
			name:          "Regular address matched",
			serverAddress: "registry1.example.com",
			want: auth.Credential{
				Username: "username1",
				Password: "password1",
			},
		},
		{
			name:          "Another entry for the same address matched",
			serverAddress: "https://registry1.example.com/",
			want: auth.Credential{
				Username: "foo",
				Password: "bar",
			},
		},
		{
			name:          "Address with different scheme unmached",
			serverAddress: "http://registry1.example.com/",
			want:          auth.EmptyCredential,
		},
		{
			name:          "Address with http prefix matched",
			serverAddress: "registry2.example.com",
			want: auth.Credential{
				Username: "username2",
				Password: "password2",
			},
		},
		{
			name:          "Address with https prefix matched",
			serverAddress: "registry3.example.com",
			want: auth.Credential{
				Username: "username3",
				Password: "password3",
			},
		},
		{
			name:          "Address with http prefix and / suffix matched",
			serverAddress: "registry4.example.com",
			want: auth.Credential{
				Username: "username4",
				Password: "password4",
			},
		},
		{
			name:          "Address with https prefix and / suffix matched",
			serverAddress: "registry5.example.com",
			want: auth.Credential{
				Username: "username5",
				Password: "password5",
			},
		},
		{
			name:          "Address with https prefix and path suffix matched",
			serverAddress: "registry6.example.com",
			want: auth.Credential{
				Username: "username6",
				Password: "password6",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetCredential(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetCredential_invalidConfig(t *testing.T) {
	cfg, err := Load("../../testdata/invalid_auths_entry_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          auth.Credential
		wantErr       bool
	}{
		{
			name:          "Invalid auth encode",
			serverAddress: "registry1.example.com",
			want:          auth.EmptyCredential,
			wantErr:       true,
		},
		{
			name:          "Invalid auths format",
			serverAddress: "registry2.example.com",
			want:          auth.EmptyCredential,
			wantErr:       true,
		},
		{
			name:          "Invalid type",
			serverAddress: "registry3.example.com",
			want:          auth.EmptyCredential,
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetCredential(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetCredential_emptyConfig(t *testing.T) {
	cfg, err := Load("../../testdata/empty_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
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
			got, err := cfg.GetCredential(tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Config.GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetCredential_notExistConfig(t *testing.T) {
	cfg, err := Load("whatever")
	if err != nil {
		t.Fatal("Load() error =", err)
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
			got, err := cfg.GetCredential(tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Config.GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_PutCredential_notExistConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	server := "test.example.com"
	cred := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
	}

	// test put
	if err := cfg.PutCredential(server, cred); err != nil {
		t.Fatalf("Config.PutCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()

	var testCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&testCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	want := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: "refresh_token",
				RegistryToken: "access_token",
			},
		},
	}
	if !reflect.DeepEqual(testCfg, want) {
		t.Errorf("Decoded config = %v, want %v", testCfg, want)
	}

	// verify get
	got, err := cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential() = %v, want %v", got, want)
	}
}

func TestConfig_PutCredential_addNew(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	// prepare test content
	server1 := "registry1.example.com"
	cred1 := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server1: {
				SomeAuthField: "whatever",
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred1.RefreshToken,
				RegistryToken: cred1.AccessToken,
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// test put
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	server2 := "registry2.example.com"
	cred2 := auth.Credential{
		Username:     "username_2",
		Password:     "password_2",
		RefreshToken: "refresh_token_2",
		AccessToken:  "access_token_2",
	}
	if err := cfg.PutCredential(server2, cred2); err != nil {
		t.Fatalf("Config.PutCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()
	var gotCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	wantTestCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server1: {
				SomeAuthField: "whatever",
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred1.RefreshToken,
				RegistryToken: cred1.AccessToken,
			},
			server2: {
				Auth:          "dXNlcm5hbWVfMjpwYXNzd29yZF8y",
				IdentityToken: "refresh_token_2",
				RegistryToken: "access_token_2",
			},
		},
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotCfg, wantTestCfg) {
		t.Errorf("Decoded config = %v, want %v", gotCfg, wantTestCfg)
	}

	// verify get
	got, err := cfg.GetCredential(server1)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred1; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server1, got, want)
	}

	got, err = cfg.GetCredential(server2)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred2; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server2, got, want)
	}
}

func TestConfig_PutCredential_updateOld(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server := "registry.example.com"
	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				SomeAuthField: "whatever",
				Username:      "foo",
				Password:      "bar",
				IdentityToken: "refresh_token",
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// test put
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	cred := auth.Credential{
		Username:    "username",
		Password:    "password",
		AccessToken: "access_token",
	}
	if err := cfg.PutCredential(server, cred); err != nil {
		t.Fatalf("Config.PutCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()
	var gotCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	wantCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				RegistryToken: "access_token",
			},
		},
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotCfg, wantCfg) {
		t.Errorf("Decoded config = %v, want %v", gotCfg, wantCfg)
	}

	// verify get
	got, err := cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server, got, want)
	}
}

func TestConfig_DeleteCredential(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server1 := "registry1.example.com"
	cred1 := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
	}
	server2 := "registry2.example.com"
	cred2 := auth.Credential{
		Username:     "username_2",
		Password:     "password_2",
		RefreshToken: "refresh_token_2",
		AccessToken:  "access_token_2",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server1: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred1.RefreshToken,
				RegistryToken: cred1.AccessToken,
			},
			server2: {
				Auth:          "dXNlcm5hbWVfMjpwYXNzd29yZF8y",
				IdentityToken: "refresh_token_2",
				RegistryToken: "access_token_2",
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	// test get
	got, err := cfg.GetCredential(server1)
	if err != nil {
		t.Fatalf("FileStore.GetCredential() error = %v", err)
	}
	if want := cred1; !reflect.DeepEqual(got, want) {
		t.Errorf("FileStore.GetCredential(%s) = %v, want %v", server1, got, want)
	}
	got, err = cfg.GetCredential(server2)
	if err != nil {
		t.Fatalf("FileStore.GetCredential() error = %v", err)
	}
	if want := cred2; !reflect.DeepEqual(got, want) {
		t.Errorf("FileStore.Get(%s) = %v, want %v", server2, got, want)
	}

	// test delete
	if err := cfg.DeleteCredential(server1); err != nil {
		t.Fatalf("Config.DeleteCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()
	var gotTestCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotTestCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	wantTestCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server2: testCfg.AuthConfigs[server2],
		},
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotTestCfg, wantTestCfg) {
		t.Errorf("Decoded config = %v, want %v", gotTestCfg, wantTestCfg)
	}

	// test get again
	got, err = cfg.GetCredential(server1)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := auth.EmptyCredential; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server1, got, want)
	}
	got, err = cfg.GetCredential(server2)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred2; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server2, got, want)
	}
}

func TestConfig_DeleteCredential_lastConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server := "registry1.example.com"
	cred := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred.RefreshToken,
				RegistryToken: cred.AccessToken,
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	// test get
	got, err := cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server, got, want)
	}

	// test delete
	if err := cfg.DeleteCredential(server); err != nil {
		t.Fatalf("Config.DeleteCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()
	var gotTestCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotTestCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	wantTestCfg := configtest.Config{
		AuthConfigs:     map[string]configtest.AuthConfig{},
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotTestCfg, wantTestCfg) {
		t.Errorf("Decoded config = %v, want %v", gotTestCfg, wantTestCfg)
	}

	// test get again
	got, err = cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := auth.EmptyCredential; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server, got, want)
	}
}

func TestConfig_DeleteCredential_notExistRecord(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server := "registry1.example.com"
	cred := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
	}
	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred.RefreshToken,
				RegistryToken: cred.AccessToken,
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	// test get
	got, err := cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server, got, want)
	}

	// test delete
	if err := cfg.DeleteCredential("test.example.com"); err != nil {
		t.Fatalf("Config.DeleteCredential() error = %v", err)
	}

	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	defer configFile.Close()
	var gotTestCfg configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotTestCfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	wantTestCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: testCfg.AuthConfigs[server],
		},
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotTestCfg, wantTestCfg) {
		t.Errorf("Decoded config = %v, want %v", gotTestCfg, wantTestCfg)
	}

	// test get again
	got, err = cfg.GetCredential(server)
	if err != nil {
		t.Fatalf("Config.GetCredential() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetCredential(%s) = %v, want %v", server, got, want)
	}
}

func TestConfig_DeleteCredential_notExistConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	server := "test.example.com"
	// test delete
	if err := cfg.DeleteCredential(server); err != nil {
		t.Fatalf("Config.DeleteCredential() error = %v", err)
	}

	// verify config file is not created
	_, err = os.Stat(configPath)
	if wantErr := os.ErrNotExist; !errors.Is(err, wantErr) {
		t.Errorf("Stat(%s) error = %v, wantErr %v", configPath, err, wantErr)
	}
}

func TestConfig_GetCredentialHelper(t *testing.T) {
	cfg, err := Load("../../testdata/credHelpers_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          string
	}{
		{
			name:          "Get cred helper: registry_helper1",
			serverAddress: "registry1.example.com",
			want:          "registry1-helper",
		},
		{
			name:          "Get cred helper: registry_helper2",
			serverAddress: "registry2.example.com",
			want:          "registry2-helper",
		},
		{
			name:          "Empty cred helper configured",
			serverAddress: "registry3.example.com",
			want:          "",
		},
		{
			name:          "No cred helper configured",
			serverAddress: "whatever.example.com",
			want:          "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.GetCredentialHelper(tt.serverAddress); got != tt.want {
				t.Errorf("Config.GetCredentialHelper() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_CredentialsStore(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
		want       string
	}{
		{
			name:       "creds store configured",
			configPath: "../../testdata/credsStore_config.json",
			want:       "teststore",
		},
		{
			name:       "No creds store configured",
			configPath: "../../testdata/credsHelpers_config.json",
			want:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(tt.configPath)
			if err != nil {
				t.Fatal("Load() error =", err)
			}
			if got := cfg.CredentialsStore(); got != tt.want {
				t.Errorf("Config.CredentialsStore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_SetCredentialsStore(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	testCfg := configtest.Config{
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(testCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// test SetCredentialsStore
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	credsStore := "testStore"
	if err := cfg.SetCredentialsStore(credsStore); err != nil {
		t.Fatal("Config.SetCredentialsStore() error =", err)
	}

	// verify
	if got := cfg.credentialsStore; got != credsStore {
		t.Errorf("Config.credentialsStore = %v, want %v", got, credsStore)
	}
	// verify config file
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	var gotTestCfg1 configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotTestCfg1); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	if err := configFile.Close(); err != nil {
		t.Fatal("failed to close config file:", err)
	}

	wantTestCfg1 := configtest.Config{
		AuthConfigs:      make(map[string]configtest.AuthConfig),
		CredentialsStore: credsStore,
		SomeConfigField:  testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotTestCfg1, wantTestCfg1) {
		t.Errorf("Decoded config = %v, want %v", gotTestCfg1, wantTestCfg1)
	}

	// test SetCredentialsStore: set as empty
	if err := cfg.SetCredentialsStore(""); err != nil {
		t.Fatal("Config.SetCredentialsStore() error =", err)
	}
	// verify
	if got := cfg.credentialsStore; got != "" {
		t.Errorf("Config.credentialsStore = %v, want empty", got)
	}
	// verify config file
	configFile, err = os.Open(configPath)
	if err != nil {
		t.Fatalf("failed to open config file: %v", err)
	}
	var gotTestCfg2 configtest.Config
	if err := json.NewDecoder(configFile).Decode(&gotTestCfg2); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	if err := configFile.Close(); err != nil {
		t.Fatal("failed to close config file:", err)
	}

	wantTestCfg2 := configtest.Config{
		AuthConfigs:     make(map[string]configtest.AuthConfig),
		SomeConfigField: testCfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotTestCfg2, wantTestCfg2) {
		t.Errorf("Decoded config = %v, want %v", gotTestCfg2, wantTestCfg2)
	}
}

func TestConfig_IsAuthConfigured(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name             string
		fileName         string
		shouldCreateFile bool
		cfg              configtest.Config
		want             bool
	}{
		{
			name:             "not existing file",
			fileName:         "config.json",
			shouldCreateFile: false,
			cfg:              configtest.Config{},
			want:             false,
		},
		{
			name:             "no auth",
			fileName:         "config.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				SomeConfigField: 123,
			},
			want: false,
		},
		{
			name:             "empty auths exist",
			fileName:         "empty_auths.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				AuthConfigs: map[string]configtest.AuthConfig{},
			},
			want: false,
		},
		{
			name:             "auths exist, but no credential",
			fileName:         "no_cred_auths.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				AuthConfigs: map[string]configtest.AuthConfig{
					"test.example.com": {},
				},
			},
			want: true,
		},
		{
			name:             "auths exist",
			fileName:         "auths.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				AuthConfigs: map[string]configtest.AuthConfig{
					"test.example.com": {
						Auth: "dXNlcm5hbWU6cGFzc3dvcmQ=",
					},
				},
			},
			want: true,
		},
		{
			name:             "credsStore exists",
			fileName:         "credsStore.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				CredentialsStore: "teststore",
			},
			want: true,
		},
		{
			name:             "empty credHelpers exist",
			fileName:         "empty_credsStore.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				CredentialHelpers: map[string]string{},
			},
			want: false,
		},
		{
			name:             "credHelpers exist",
			fileName:         "credsStore.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				CredentialHelpers: map[string]string{
					"test.example.com": "testhelper",
				},
			},
			want: true,
		},
		{
			name:             "all exist",
			fileName:         "credsStore.json",
			shouldCreateFile: true,
			cfg: configtest.Config{
				SomeConfigField: 123,
				AuthConfigs: map[string]configtest.AuthConfig{
					"test.example.com": {},
				},
				CredentialsStore: "teststore",
				CredentialHelpers: map[string]string{
					"test.example.com": "testhelper",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare test content
			configPath := filepath.Join(tempDir, tt.fileName)
			if tt.shouldCreateFile {
				jsonStr, err := json.Marshal(tt.cfg)
				if err != nil {
					t.Fatalf("failed to marshal config: %v", err)
				}
				if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
					t.Fatalf("failed to write config file: %v", err)
				}
			}

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal("Load() error =", err)
			}
			if got := cfg.IsAuthConfigured(); got != tt.want {
				t.Errorf("IsAuthConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_saveFile(t *testing.T) {
	tempDir := t.TempDir()
	tests := []struct {
		name             string
		fileName         string
		shouldCreateFile bool
		oldCfg           configtest.Config
		newCfg           configtest.Config
		wantCfg          configtest.Config
	}{
		{
			name:     "set credsStore in a non-existing file",
			fileName: "config.json",
			oldCfg:   configtest.Config{},
			newCfg: configtest.Config{
				CredentialsStore: "teststore",
			},
			wantCfg: configtest.Config{
				AuthConfigs:      make(map[string]configtest.AuthConfig),
				CredentialsStore: "teststore",
			},
			shouldCreateFile: false,
		},
		{
			name:     "set credsStore in empty file",
			fileName: "empty.json",
			oldCfg:   configtest.Config{},
			newCfg: configtest.Config{
				CredentialsStore: "teststore",
			},
			wantCfg: configtest.Config{
				AuthConfigs:      make(map[string]configtest.AuthConfig),
				CredentialsStore: "teststore",
			},
			shouldCreateFile: true,
		},
		{
			name:     "set credsStore in a no-auth-configured file",
			fileName: "empty.json",
			oldCfg: configtest.Config{
				SomeConfigField: 123,
			},
			newCfg: configtest.Config{
				CredentialsStore: "teststore",
			},
			wantCfg: configtest.Config{
				SomeConfigField:  123,
				AuthConfigs:      make(map[string]configtest.AuthConfig),
				CredentialsStore: "teststore",
			},
			shouldCreateFile: true,
		},
		{
			name:     "Set credsStore and credHelpers in an auth-configured file",
			fileName: "auth_configured.json",
			oldCfg: configtest.Config{
				SomeConfigField: 123,
				AuthConfigs: map[string]configtest.AuthConfig{
					"registry1.example.com": {
						SomeAuthField: "something",
						Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
					},
				},
				CredentialsStore: "oldstore",
				CredentialHelpers: map[string]string{
					"registry2.example.com": "testhelper",
				},
			},
			newCfg: configtest.Config{
				AuthConfigs:      make(map[string]configtest.AuthConfig),
				SomeConfigField:  123,
				CredentialsStore: "newstore",
				CredentialHelpers: map[string]string{
					"xxx": "yyy",
				},
			},
			wantCfg: configtest.Config{
				SomeConfigField: 123,
				AuthConfigs: map[string]configtest.AuthConfig{
					"registry1.example.com": {
						SomeAuthField: "something",
						Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
					},
				},
				CredentialsStore: "newstore",
				CredentialHelpers: map[string]string{
					"registry2.example.com": "testhelper", // cred helpers will not be updated
				},
			},
			shouldCreateFile: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare test content
			configPath := filepath.Join(tempDir, tt.fileName)
			if tt.shouldCreateFile {
				jsonStr, err := json.Marshal(tt.oldCfg)
				if err != nil {
					t.Fatalf("failed to marshal config: %v", err)
				}
				if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
					t.Fatalf("failed to write config file: %v", err)
				}
			}

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal("Load() error =", err)
			}
			cfg.credentialsStore = tt.newCfg.CredentialsStore
			cfg.credentialHelpers = tt.newCfg.CredentialHelpers
			if err := cfg.saveFile(); err != nil {
				t.Fatal("saveFile() error =", err)
			}

			// verify config file
			configFile, err := os.Open(configPath)
			if err != nil {
				t.Fatalf("failed to open config file: %v", err)
			}
			defer configFile.Close()
			var gotCfg configtest.Config
			if err := json.NewDecoder(configFile).Decode(&gotCfg); err != nil {
				t.Fatalf("failed to decode config file: %v", err)
			}
			if !reflect.DeepEqual(gotCfg, tt.wantCfg) {
				t.Errorf("Decoded config = %v, want %v", gotCfg, tt.wantCfg)
			}
		})
	}
}

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

func Test_toHostname(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{
			addr: "http://test.example.com",
			want: "test.example.com",
		},
		{
			addr: "http://test.example.com/",
			want: "test.example.com",
		},
		{
			addr: "http://test.example.com/foo/bar",
			want: "test.example.com",
		},
		{
			addr: "https://test.example.com",
			want: "test.example.com",
		},
		{
			addr: "https://test.example.com/",
			want: "test.example.com",
		},
		{
			addr: "http://test.example.com/foo/bar",
			want: "test.example.com",
		},
		{
			addr: "test.example.com",
			want: "test.example.com",
		},
		{
			addr: "test.example.com/foo/bar/",
			want: "test.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToHostname(tt.addr); got != tt.want {
				t.Errorf("toHostname() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_Path(t *testing.T) {
	mockedPath := "/path/to/config.json"
	config := Config{
		path: mockedPath,
	}
	if got := config.Path(); got != mockedPath {
		t.Errorf("Config.Path() = %v, want %v", got, mockedPath)
	}
}
