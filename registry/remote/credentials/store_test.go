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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/internal/config/configtest"
)

type badStore struct{}

var errBadStore = errors.New("bad store!")

// Get retrieves credentials from the store for the given server address.
func (s *badStore) Get(ctx context.Context, serverAddress string) (auth.Credential, error) {
	return auth.EmptyCredential, errBadStore
}

// Put saves credentials into the store for the given server address.
func (s *badStore) Put(ctx context.Context, serverAddress string, cred auth.Credential) error {
	return errBadStore
}

// Delete removes credentials from the store for the given server address.
func (s *badStore) Delete(ctx context.Context, serverAddress string) error {
	return errBadStore
}

func Test_DynamicStore_IsAuthConfigured(t *testing.T) {
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

			ds, err := NewStore(configPath, StoreOptions{})
			if err != nil {
				t.Fatal("newStore() error =", err)
			}
			if got := ds.IsAuthConfigured(); got != tt.want {
				t.Errorf("DynamicStore.IsAuthConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_DynamicStore_authConfigured(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "auth_configured.json")
	config := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			"xxx": {},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	ds, err := NewStore(configPath, StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}

	// test IsAuthConfigured
	authConfigured := ds.IsAuthConfigured()
	if want := true; authConfigured != want {
		t.Errorf("DynamicStore.IsAuthConfigured() = %v, want %v", authConfigured, want)
	}

	serverAddr := "test.example.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	ctx := context.Background()

	// test put
	if err := ds.Put(ctx, serverAddr, cred); err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	// Put() should not set detected store back to config
	if got := ds.detectedCredsStore; got != "" {
		t.Errorf("ds.detectedCredsStore = %v, want empty", got)
	}
	if got := ds.config.CredentialsStore(); got != "" {
		t.Errorf("ds.config.CredentialsStore() = %v, want empty", got)
	}

	// test get
	got, err := ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test delete
	err = ds.Delete(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Delete() error =", err)
	}

	// verify delete
	got, err = ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}
}

func Test_DynamicStore_authConfigured_DetectDefaultNativeStore(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "auth_configured.json")
	config := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			"xxx": {},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	opts := StoreOptions{
		AllowPlaintextPut:        true,
		DetectDefaultNativeStore: true,
	}
	ds, err := NewStore(configPath, opts)
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}

	// test IsAuthConfigured
	authConfigured := ds.IsAuthConfigured()
	if want := true; authConfigured != want {
		t.Errorf("DynamicStore.IsAuthConfigured() = %v, want %v", authConfigured, want)
	}

	serverAddr := "test.example.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	ctx := context.Background()

	// test put
	if err := ds.Put(ctx, serverAddr, cred); err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	// Put() should not set detected store back to config
	if got := ds.detectedCredsStore; got != "" {
		t.Errorf("ds.detectedCredsStore = %v, want empty", got)
	}
	if got := ds.config.CredentialsStore(); got != "" {
		t.Errorf("ds.config.CredentialsStore() = %v, want empty", got)
	}

	// test get
	got, err := ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test delete
	err = ds.Delete(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Delete() error =", err)
	}

	// verify delete
	got, err = ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}
}

func Test_DynamicStore_noAuthConfigured(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "no_auth_configured.json")
	cfg := configtest.Config{
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	ds, err := NewStore(configPath, StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}

	// test IsAuthConfigured
	authConfigured := ds.IsAuthConfigured()
	if want := false; authConfigured != want {
		t.Errorf("DynamicStore.IsAuthConfigured() = %v, want %v", authConfigured, want)
	}

	serverAddr := "test.example.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	ctx := context.Background()

	// Get() should not set detected store back to config
	if _, err := ds.Get(ctx, serverAddr); err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}

	// test put
	if err := ds.Put(ctx, serverAddr, cred); err != nil {
		t.Fatal("DynamicStore.Put() error =", err)
	}
	// Put() should not set detected store back to config
	if got := ds.detectedCredsStore; got != "" {
		t.Errorf("ds.detectedCredsStore = %v, want empty", got)
	}
	if got := ds.config.CredentialsStore(); got != "" {
		t.Errorf("ds.config.CredentialsStore() = %v, want empty", got)
	}

	// test get
	got, err := ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test delete
	err = ds.Delete(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Delete() error =", err)
	}

	// verify delete
	got, err = ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}
}

func Test_DynamicStore_noAuthConfigured_DetectDefaultNativeStore(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "no_auth_configured.json")
	cfg := configtest.Config{
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	opts := StoreOptions{
		AllowPlaintextPut:        true,
		DetectDefaultNativeStore: true,
	}
	ds, err := NewStore(configPath, opts)
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}

	// test IsAuthConfigured
	authConfigured := ds.IsAuthConfigured()
	if want := false; authConfigured != want {
		t.Errorf("DynamicStore.IsAuthConfigured() = %v, want %v", authConfigured, want)
	}

	serverAddr := "test.example.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	ctx := context.Background()

	// Get() should set detectedCredsStore only, but should not save it back to config
	if _, err := ds.Get(ctx, serverAddr); err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if defaultStore := getDefaultHelperSuffix(); defaultStore != "" {
		if got := ds.detectedCredsStore; got != defaultStore {
			t.Errorf("ds.detectedCredsStore = %v, want %v", got, defaultStore)
		}
	}
	if got := ds.config.CredentialsStore(); got != "" {
		t.Errorf("ds.config.CredentialsStore() = %v, want empty", got)
	}

	// test put
	if err := ds.Put(ctx, serverAddr, cred); err != nil {
		t.Fatal("DynamicStore.Put() error =", err)
	}

	// Put() should set the detected store back to config
	if got := ds.config.CredentialsStore(); got != ds.detectedCredsStore {
		t.Errorf("ds.config.CredentialsStore() = %v, want %v", got, ds.detectedCredsStore)
	}

	// test get
	got, err := ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test delete
	err = ds.Delete(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Delete() error =", err)
	}

	// verify delete
	got, err = ds.Get(ctx, serverAddr)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}
}

func Test_DynamicStore_fileStore_AllowPlainTextPut(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	serverAddr := "newtest.example.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	ctx := context.Background()

	cfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			"test.example.com": {},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// test default option
	ds, err := NewStore(configPath, StoreOptions{})
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}
	err = ds.Put(ctx, serverAddr, cred)
	if wantErr := ErrPlaintextPutDisabled; !errors.Is(err, wantErr) {
		t.Errorf("DynamicStore.Put() error = %v, wantErr %v", err, wantErr)
	}

	// test AllowPlainTextPut = true
	ds, err = NewStore(configPath, StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatal("NewStore() error =", err)
	}
	if err := ds.Put(ctx, serverAddr, cred); err != nil {
		t.Error("DynamicStore.Put() error =", err)
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
			"test.example.com": {},
			serverAddr: {
				Auth: "dXNlcm5hbWU6cGFzc3dvcmQ=",
			},
		},
		SomeConfigField: cfg.SomeConfigField,
	}
	if !reflect.DeepEqual(gotCfg, wantCfg) {
		t.Errorf("Decoded config = %v, want %v", gotCfg, wantCfg)
	}
}

func Test_DynamicStore_getHelperSuffix(t *testing.T) {
	tests := []struct {
		name          string
		configPath    string
		serverAddress string
		want          string
	}{
		{
			name:          "Get cred helper: registry_helper1",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry1.example.com",
			want:          "registry1-helper",
		},
		{
			name:          "Get cred helper: registry_helper2",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry2.example.com",
			want:          "registry2-helper",
		},
		{
			name:          "Empty cred helper configured",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry3.example.com",
			want:          "",
		},
		{
			name:          "No cred helper and creds store configured",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "whatever.example.com",
			want:          "",
		},
		{
			name:          "Choose cred helper over creds store",
			configPath:    "testdata/credsStore_config.json",
			serverAddress: "test.example.com",
			want:          "test-helper",
		},
		{
			name:          "No cred helper configured, choose cred store",
			configPath:    "testdata/credsStore_config.json",
			serverAddress: "whatever.example.com",
			want:          "teststore",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds, err := NewStore(tt.configPath, StoreOptions{})
			if err != nil {
				t.Fatal("NewStore() error =", err)
			}
			if got := ds.getHelperSuffix(tt.serverAddress); got != tt.want {
				t.Errorf("DynamicStore.getHelperSuffix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_DynamicStore_ConfigPath(t *testing.T) {
	path := "../../testdata/credsStore_config.json"
	var err error
	store, err := NewStore(path, StoreOptions{})
	if err != nil {
		t.Fatal("NewFileStore() error =", err)
	}
	got := store.ConfigPath()
	if got != path {
		t.Errorf("Config.GetPath() = %v, want %v", got, path)
	}
}

func Test_DynamicStore_getStore_nativeStore(t *testing.T) {
	tests := []struct {
		name          string
		configPath    string
		serverAddress string
	}{
		{
			name:          "Cred helper configured for registry1.example.com",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry1.example.com",
		},
		{
			name:          "Cred helper configured for registry2.example.com",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry2.example.com",
		},
		{
			name:          "Cred helper configured for test.example.com",
			configPath:    "testdata/credsStore_config.json",
			serverAddress: "test.example.com",
		},
		{
			name:          "No cred helper configured, use creds store",
			configPath:    "testdata/credsStore_config.json",
			serverAddress: "whaterver.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds, err := NewStore(tt.configPath, StoreOptions{})
			if err != nil {
				t.Fatal("NewStore() error =", err)
			}
			gotStore := ds.getStore(tt.serverAddress)
			if _, ok := gotStore.(*nativeStore); !ok {
				t.Errorf("gotStore is not a native store")
			}
		})
	}
}

func Test_DynamicStore_getStore_fileStore(t *testing.T) {
	tests := []struct {
		name          string
		configPath    string
		serverAddress string
	}{
		{
			name:          "Empty cred helper configured for registry3.example.com",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "registry3.example.com",
		},
		{
			name:          "No cred helper configured",
			configPath:    "testdata/credHelpers_config.json",
			serverAddress: "whatever.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds, err := NewStore(tt.configPath, StoreOptions{})
			if err != nil {
				t.Fatal("NewStore() error =", err)
			}
			gotStore := ds.getStore(tt.serverAddress)
			gotFS1, ok := gotStore.(*FileStore)
			if !ok {
				t.Errorf("gotStore is not a file store")
			}

			// get again, the two file stores should be based on the same config instance
			gotStore = ds.getStore(tt.serverAddress)
			gotFS2, ok := gotStore.(*FileStore)
			if !ok {
				t.Errorf("gotStore is not a file store")
			}
			if gotFS1.config != gotFS2.config {
				t.Errorf("gotFS1 and gotFS2 are not based on the same config")
			}
		})
	}
}

func Test_storeWithFallbacks_Get(t *testing.T) {
	// prepare test content
	server1 := "foo.registry.com"
	cred1 := auth.Credential{
		Username: "username",
		Password: "password",
	}
	server2 := "bar.registry.com"
	cred2 := auth.Credential{
		RefreshToken: "identity_token",
	}

	primaryStore := &testStore{}
	fallbackStore1 := &testStore{
		storage: map[string]auth.Credential{
			server1: cred1,
		},
	}
	fallbackStore2 := &testStore{
		storage: map[string]auth.Credential{
			server2: cred2,
		},
	}
	sf := NewStoreWithFallbacks(primaryStore, fallbackStore1, fallbackStore2)
	ctx := context.Background()

	// test Get()
	got1, err := sf.Get(ctx, server1)
	if err != nil {
		t.Fatalf("storeWithFallbacks.Get(%s) error = %v", server1, err)
	}
	if want := cred1; got1 != cred1 {
		t.Errorf("storeWithFallbacks.Get(%s) = %v, want %v", server1, got1, want)
	}
	got2, err := sf.Get(ctx, server2)
	if err != nil {
		t.Fatalf("storeWithFallbacks.Get(%s) error = %v", server2, err)
	}
	if want := cred2; got2 != cred2 {
		t.Errorf("storeWithFallbacks.Get(%s) = %v, want %v", server2, got2, want)
	}

	// test Get(): no credential found
	got, err := sf.Get(ctx, "whaterver")
	if err != nil {
		t.Fatal("storeWithFallbacks.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("storeWithFallbacks.Get() = %v, want %v", got, want)
	}
}

func Test_storeWithFallbacks_Get_throwError(t *testing.T) {
	badStore := &badStore{}
	goodStore := &testStore{}
	sf := NewStoreWithFallbacks(badStore, goodStore)
	ctx := context.Background()

	// test Get(): should throw error
	_, err := sf.Get(ctx, "whatever")
	if wantErr := errBadStore; !errors.Is(err, wantErr) {
		t.Errorf("storeWithFallback.Get() error = %v, wantErr %v", err, wantErr)
	}
}

func Test_storeWithFallbacks_Put(t *testing.T) {
	// prepare test content
	cfg := configtest.Config{
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "no_auth_configured.json")
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	opts := StoreOptions{
		AllowPlaintextPut: true,
	}
	primaryStore, err := NewStore(configPath, opts) // plaintext enabled
	if err != nil {
		t.Fatalf("NewStore(%s) error = %v", configPath, err)
	}
	badStore := &badStore{} // bad store
	sf := NewStoreWithFallbacks(primaryStore, badStore)
	ctx := context.Background()

	server := "example.registry.com"
	cred := auth.Credential{
		Username: "username",
		Password: "password",
	}
	// test Put()
	if err := sf.Put(ctx, server, cred); err != nil {
		t.Fatal("storeWithFallbacks.Put() error =", err)
	}
	// verify Get()
	got, err := sf.Get(ctx, server)
	if err != nil {
		t.Fatal("storeWithFallbacks.Get() error =", err)
	}
	if want := cred; got != want {
		t.Errorf("storeWithFallbacks.Get() = %v, want %v", got, want)
	}
}

func Test_storeWithFallbacks_Put_throwError(t *testing.T) {
	badStore := &badStore{}
	goodStore := &testStore{}
	sf := NewStoreWithFallbacks(badStore, goodStore)
	ctx := context.Background()

	// test Put(): should thrown error
	err := sf.Put(ctx, "whatever", auth.Credential{})
	if wantErr := errBadStore; !errors.Is(err, wantErr) {
		t.Errorf("storeWithFallback.Put() error = %v, wantErr %v", err, wantErr)
	}
}

func Test_storeWithFallbacks_Delete(t *testing.T) {
	// prepare test content
	server1 := "foo.registry.com"
	cred1 := auth.Credential{
		Username: "username",
		Password: "password",
	}
	server2 := "bar.registry.com"
	cred2 := auth.Credential{
		RefreshToken: "identity_token",
	}

	primaryStore := &testStore{
		storage: map[string]auth.Credential{
			server1: cred1,
			server2: cred2,
		},
	}
	badStore := &badStore{}
	sf := NewStoreWithFallbacks(primaryStore, badStore)
	ctx := context.Background()

	// test Delete(): server1
	if err := sf.Delete(ctx, server1); err != nil {
		t.Fatal("storeWithFallback.Delete()")
	}
	// verify primary store
	if want := map[string]auth.Credential{server2: cred2}; !reflect.DeepEqual(primaryStore.storage, want) {
		t.Errorf("primaryStore.storage = %v, want %v", primaryStore.storage, want)
	}

	// test Delete(): server2
	if err := sf.Delete(ctx, server2); err != nil {
		t.Fatal("storeWithFallback.Delete()")
	}
	// verify primary store
	if want := map[string]auth.Credential{}; !reflect.DeepEqual(primaryStore.storage, want) {
		t.Errorf("primaryStore.storage = %v, want %v", primaryStore.storage, want)
	}
}

func Test_storeWithFallbacks_Delete_throwError(t *testing.T) {
	badStore := &badStore{}
	goodStore := &testStore{}
	sf := NewStoreWithFallbacks(badStore, goodStore)
	ctx := context.Background()

	// test Delete(): should throw error
	err := sf.Delete(ctx, "whatever")
	if wantErr := errBadStore; !errors.Is(err, wantErr) {
		t.Errorf("storeWithFallback.Delete() error = %v, wantErr %v", err, wantErr)
	}
}

func Test_getDockerConfigPath_env(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal("os.Getwd() error =", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)

	got, err := getDockerConfigPath()
	if err != nil {
		t.Fatal("getDockerConfigPath() error =", err)
	}
	if want := filepath.Join(dir, "config.json"); got != want {
		t.Errorf("getDockerConfigPath() = %v, want %v", got, want)
	}
}

func Test_getDockerConfigPath_homeDir(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "")

	got, err := getDockerConfigPath()
	if err != nil {
		t.Fatal("getDockerConfigPath() error =", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("os.UserHomeDir()")
	}
	if want := filepath.Join(homeDir, ".docker", "config.json"); got != want {
		t.Errorf("getDockerConfigPath() = %v, want %v", got, want)
	}
}

func TestNewStoreFromDocker(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	t.Setenv("DOCKER_CONFIG", tempDir)

	serverAddr1 := "test.example.com"
	cred1 := auth.Credential{
		Username: "foo",
		Password: "bar",
	}
	config := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			serverAddr1: {
				Auth: "Zm9vOmJhcg==",
			},
		},
		SomeConfigField: 123,
	}
	jsonStr, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	ctx := context.Background()

	ds, err := NewStoreFromDocker(StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatal("NewStoreFromDocker() error =", err)
	}

	// test getting an existing credential
	got, err := ds.Get(ctx, serverAddr1)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred1; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test putting a new credential
	serverAddr2 := "newtest.example.com"
	cred2 := auth.Credential{
		Username: "username",
		Password: "password",
	}
	if err := ds.Put(ctx, serverAddr2, cred2); err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}

	// test getting the new credential
	got, err = ds.Get(ctx, serverAddr2)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := cred2; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}

	// test deleting the old credential
	err = ds.Delete(ctx, serverAddr1)
	if err != nil {
		t.Fatal("DynamicStore.Delete() error =", err)
	}

	// verify delete
	got, err = ds.Get(ctx, serverAddr1)
	if err != nil {
		t.Fatal("DynamicStore.Get() error =", err)
	}
	if want := auth.EmptyCredential; got != want {
		t.Errorf("DynamicStore.Get() = %v, want %v", got, want)
	}
}
