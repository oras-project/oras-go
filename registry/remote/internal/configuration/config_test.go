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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote/internal/configuration/configtest"
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
			configPath: "./testdata/bad_config",
			wantErr:    true,
		},
		{
			name:       "Invalid auths format",
			configPath: "./testdata/invalid_auths_config.json",
			wantErr:    true,
		},
		{
			name:       "No auths field",
			configPath: "./testdata/no_auths_config.json",
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

func TestConfig_GetAuthConfig_validConfig(t *testing.T) {
	cfg, err := Load("./testdata/valid_auths_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       bool
	}{
		{
			name:          "Username and password",
			serverAddress: "registry1.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWU6cGFzc3dvcmQ=",
			},
		},
		{
			name:          "Identity token",
			serverAddress: "registry2.example.com",
			want: AuthConfig{
				IdentityToken: "identity_token",
			},
		},
		{
			name:          "Registry token",
			serverAddress: "registry3.example.com",
			want: AuthConfig{
				RegistryToken: "registry_token",
			},
		},
		{
			name:          "Username and password, identity token and registry token",
			serverAddress: "registry4.example.com",
			want: AuthConfig{
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: "identity_token",
				RegistryToken: "registry_token",
			},
		},
		{
			name:          "Empty credential",
			serverAddress: "registry5.example.com",
			want:          AuthConfig{},
		},
		{
			name:          "Username and password, no auth",
			serverAddress: "registry6.example.com",
			want: AuthConfig{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:          "Auth overriding Username and password",
			serverAddress: "registry7.example.com",
			want: AuthConfig{
				Auth:     "dXNlcm5hbWU6cGFzc3dvcmQ=",
				Username: "foo",
				Password: "bar",
			},
		},
		{
			name:          "Not in auths",
			serverAddress: "foo.example.com",
			want:          AuthConfig{},
		},
		{
			name:          "No record",
			serverAddress: "registry999.example.com",
			want:          AuthConfig{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetAuthConfig_legacyConfig(t *testing.T) {
	cfg, err := Load("./testdata/legacy_auths_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       bool
	}{
		{
			name:          "Regular address matched",
			serverAddress: "registry1.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWUxOnBhc3N3b3JkMQ==",
			},
		},
		{
			name:          "Another entry for the same address matched",
			serverAddress: "https://registry1.example.com/",
			want: AuthConfig{
				Auth: "Zm9vOmJhcg==",
			},
		},
		{
			name:          "Address with different scheme unmached",
			serverAddress: "http://registry1.example.com/",
			want:          AuthConfig{},
		},
		{
			name:          "Address with http prefix matched",
			serverAddress: "registry2.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWUyOnBhc3N3b3JkMg==",
			},
		},
		{
			name:          "Address with https prefix matched",
			serverAddress: "registry3.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWUzOnBhc3N3b3JkMw==",
			},
		},
		{
			name:          "Address with http prefix and / suffix matched",
			serverAddress: "registry4.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWU0OnBhc3N3b3JkNA==",
			},
		},
		{
			name:          "Address with https prefix and / suffix matched",
			serverAddress: "registry5.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWU1OnBhc3N3b3JkNQ==",
			},
		},
		{
			name:          "Address with https prefix and path suffix matched",
			serverAddress: "registry6.example.com",
			want: AuthConfig{
				Auth: "dXNlcm5hbWU2OnBhc3N3b3JkNg==",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetAuthConfig_invalidConfig(t *testing.T) {
	cfg, err := Load("./testdata/invalid_auths_entry_config.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       bool
	}{
		{
			name:          "Invalid auth encode",
			serverAddress: "registry1.example.com",
			want: AuthConfig{
				Auth: "username:password",
			},
			wantErr: false,
		},
		{
			name:          "Invalid auths format",
			serverAddress: "registry2.example.com",
			want:          AuthConfig{},
			wantErr:       true,
		},
		{
			name:          "Invalid type",
			serverAddress: "registry3.example.com",
			want:          AuthConfig{},
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetAuthConfig_empty(t *testing.T) {
	cfg, err := Load("./testdata/empty.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       error
	}{
		{
			name:          "Not found",
			serverAddress: "registry.example.com",
			want:          AuthConfig{},
			wantErr:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetAuthConfig_whiteSpace(t *testing.T) {
	cfg, err := Load("./testdata/whitespace.json")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       error
	}{
		{
			name:          "Not found",
			serverAddress: "registry.example.com",
			want:          AuthConfig{},
			wantErr:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_GetAuthConfig_notExistConfig(t *testing.T) {
	cfg, err := Load("whatever")
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          AuthConfig
		wantErr       error
	}{
		{
			name:          "Not found",
			serverAddress: "registry.example.com",
			want:          AuthConfig{},
			wantErr:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetAuthConfig(tt.serverAddress)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Config.GetAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Config.GetAuthConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_PutAuthConfig_notExistConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	server := "test.example.com"
	authCfg := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		IdentityToken: "refresh_token",
		RegistryToken: "access_token",
	}

	// test put
	if err := cfg.PutAuthConfig(server, authCfg); err != nil {
		t.Fatalf("Config.PutAuthConfig() error = %v", err)
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
	got, err := cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got, authCfg) {
		t.Errorf("Config.GetAuthConfig() = %v, want %v", got, authCfg)
	}
}

func TestConfig_PutAuthConfig_addNew(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	// prepare test content
	server1 := "registry1.example.com"
	authCfg1 := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		IdentityToken: "refresh_token",
		RegistryToken: "access_token",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server1: {
				SomeAuthField: "whatever",
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: "refresh_token",
				RegistryToken: "access_token",
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
	authCfg2 := AuthConfig{
		Auth:          "dXNlcm5hbWVfMjpwYXNzd29yZF8y",
		IdentityToken: "refresh_token_2",
		RegistryToken: "access_token_2",
	}
	if err := cfg.PutAuthConfig(server2, authCfg2); err != nil {
		t.Fatalf("Config.PutAuthConfig() error = %v", err)
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
				IdentityToken: "refresh_token",
				RegistryToken: "access_token",
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
	got, err := cfg.GetAuthConfig(server1)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got, authCfg1) {
		t.Errorf("Config.GetAuthConfig(%s) = %v, want %v", server1, got, authCfg1)
	}

	got, err = cfg.GetAuthConfig(server2)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got, authCfg2) {
		t.Errorf("Config.GetAuthConfig(%s) = %v, want %v", server2, got, authCfg2)
	}
}

func TestConfig_PutAuthConfig_updateOld(t *testing.T) {
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
	authCfg := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		RegistryToken: "access_token",
	}
	if err := cfg.PutAuthConfig(server, authCfg); err != nil {
		t.Fatalf("Config.PutAuthConfig() error = %v", err)
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
	got, err := cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got, authCfg) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server, got, authCfg)
	}
}

func TestConfig_DeleteAuthConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server1 := "registry1.example.com"
	cred1 := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		IdentityToken: "refresh_token",
		RegistryToken: "access_token",
	}
	server2 := "registry2.example.com"
	cred2 := AuthConfig{
		Auth:          "dXNlcm5hbWVfMjpwYXNzd29yZF8y",
		IdentityToken: "refresh_token_2",
		RegistryToken: "access_token_2",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server1: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred1.IdentityToken,
				RegistryToken: cred1.RegistryToken,
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
	got, err := cfg.GetAuthConfig(server1)
	if err != nil {
		t.Fatalf("FileStore.GetAuthConfig() error = %v", err)
	}

	if want := cred1; !reflect.DeepEqual(got, want) {
		t.Errorf("FileStore.GetAuthConfig(%s).Credential() = %v, want %v", server1, got, want)
	}
	got, err = cfg.GetAuthConfig(server2)
	if err != nil {
		t.Fatalf("FileStore.GetAuthConfig() error = %v", err)
	}

	if want := cred2; !reflect.DeepEqual(got, want) {
		t.Errorf("FileStore.GetAuthConfig(%s).Credential() = %v, want %v", server2, got, want)
	}

	// test delete
	if err := cfg.DeleteAuthConfig(server1); err != nil {
		t.Fatalf("Config.DeleteAuthConfig() error = %v", err)
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
	got, err = cfg.GetAuthConfig(server1)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}

	want := AuthConfig{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server1, got, want)
	}
	got, err = cfg.GetAuthConfig(server2)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}

	if want := cred2; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server2, got, want)
	}
}

func TestConfig_DeleteAuthConfig_lastConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server := "registry1.example.com"
	cred := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		IdentityToken: "refresh_token",
		RegistryToken: "access_token",
	}

	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred.IdentityToken,
				RegistryToken: cred.RegistryToken,
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
	got, err := cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server, got, want)
	}

	// test delete
	if err := cfg.DeleteAuthConfig(server); err != nil {
		t.Fatalf("Config.DeleteAuthConfig() error = %v", err)
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
	got, err = cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	want := AuthConfig{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server, got, want)
	}
}

func TestConfig_DeleteAuthConfig_notExistRecord(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// prepare test content
	server := "registry1.example.com"
	cred := AuthConfig{
		Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
		IdentityToken: "refresh_token",
		RegistryToken: "access_token",
	}
	testCfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			server: {
				Auth:          "dXNlcm5hbWU6cGFzc3dvcmQ=",
				IdentityToken: cred.IdentityToken,
				RegistryToken: cred.RegistryToken,
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
	got, err := cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server, got, want)
	}

	// test delete
	if err := cfg.DeleteAuthConfig("test.example.com"); err != nil {
		t.Fatalf("Config.DeleteAuthConfig() error = %v", err)
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
	got, err = cfg.GetAuthConfig(server)
	if err != nil {
		t.Fatalf("Config.GetAuthConfig() error = %v", err)
	}
	if want := cred; !reflect.DeepEqual(got, want) {
		t.Errorf("Config.GetAuthConfig(%s).Credential() = %v, want %v", server, got, want)
	}
}

func TestConfig_DeleteAuthConfig_notExistConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}

	server := "test.example.com"
	// test delete
	if err := cfg.DeleteAuthConfig(server); err != nil {
		t.Fatalf("Config.DeleteAuthConfig() error = %v", err)
	}

	// verify config file is not created
	_, err = os.Stat(configPath)
	if wantErr := os.ErrNotExist; !errors.Is(err, wantErr) {
		t.Errorf("Stat(%s) error = %v, wantErr %v", configPath, err, wantErr)
	}
}

func TestConfig_GetCredentialHelper(t *testing.T) {
	cfg, err := Load("./testdata/credHelpers_config.json")
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
			configPath: "./testdata/credsStore_config.json",
			want:       "teststore",
		},
		{
			name:       "No creds store configured",
			configPath: "./testdata/credsHelpers_config.json",
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
	if got := cfg.CredentialsStore(); got != credsStore {
		t.Errorf("Config.CredentialsStore() = %v, want %v", got, credsStore)
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
	if got := cfg.CredentialsStore(); got != "" {
		t.Errorf("Config.CredentialsStore() = %v, want empty", got)
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
	mockedPath := "testdata/valid_auths_config.json"
	config, err := Load(mockedPath)
	if err != nil {
		t.Fatal("Load() error =", err)
	}
	if got := config.Path(); got != mockedPath {
		t.Errorf("Config.Path() = %v, want %v", got, mockedPath)
	}
}
