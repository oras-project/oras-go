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
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRegistriesConf(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	if conf.Path() != path {
		t.Errorf("NewRegistriesConf().Path() = %v, want %v", conf.Path(), path)
	}

	if conf.IsAuthConfigured() {
		t.Error("NewRegistriesConf().IsAuthConfigured() = true, want false")
	}

	if conf.CredentialsStore() != "" {
		t.Errorf("NewRegistriesConf().CredentialsStore() = %v, want empty", conf.CredentialsStore())
	}
}

func TestRegistriesConf_Config_Interface(t *testing.T) {
	// Verify that registriesConf implements Config interface
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")
	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}
	var _ Config = conf
}

func TestRegistriesConf_CredentialOperations(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}
	serverAddress := "registry.example.com"
	cred := Credential{
		Username: "testuser",
		Password: "testpass",
	}

	// Test initial state - no credentials
	gotCred, err := conf.GetCredential(serverAddress)
	if err != nil {
		t.Errorf("GetCredential() error = %v", err)
	}
	if gotCred != EmptyCredential {
		t.Errorf("GetCredential() = %v, want %v", gotCred, EmptyCredential)
	}

	// Test putting credential
	if err := conf.PutCredential(serverAddress, cred); err != nil {
		t.Errorf("PutCredential() error = %v", err)
	}

	// Test getting credential
	gotCred, err = conf.GetCredential(serverAddress)
	if err != nil {
		t.Errorf("GetCredential() error = %v", err)
	}
	if gotCred != cred {
		t.Errorf("GetCredential() = %v, want %v", gotCred, cred)
	}

	// Test IsAuthConfigured after adding credential
	if !conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = false, want true after adding credential")
	}

	// Test deleting credential
	if err := conf.DeleteCredential(serverAddress); err != nil {
		t.Errorf("DeleteCredential() error = %v", err)
	}

	// Test getting credential after deletion
	gotCred, err = conf.GetCredential(serverAddress)
	if err != nil {
		t.Errorf("GetCredential() error = %v", err)
	}
	if gotCred != EmptyCredential {
		t.Errorf("GetCredential() after delete = %v, want %v", gotCred, EmptyCredential)
	}
}

func TestRegistriesConf_CredentialsStore(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}
	credsStore := "osxkeychain"

	// Test initial state
	if got := conf.CredentialsStore(); got != "" {
		t.Errorf("CredentialsStore() = %v, want empty", got)
	}

	// Test setting credentials store
	if err := conf.SetCredentialsStore(credsStore); err != nil {
		t.Errorf("SetCredentialsStore() error = %v", err)
	}

	// Test getting credentials store
	if got := conf.CredentialsStore(); got != credsStore {
		t.Errorf("CredentialsStore() = %v, want %v", got, credsStore)
	}

	// Test IsAuthConfigured with credentials store set
	if !conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = false, want true with credentials store set")
	}
}

func TestRegistriesConf_GetCredentialHelper(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}
	serverAddress := "registry.example.com"

	// Test getting non-existent helper
	if got := conf.GetCredentialHelper(serverAddress); got != "" {
		t.Errorf("GetCredentialHelper() = %v, want empty", got)
	}
}

func TestRegistriesConf_TOMLOperations(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test setting unqualified search registries
	registries := []string{"docker.io", "registry.fedoraproject.org"}
	if err := conf.SetUnqualifiedSearchRegistries(registries); err != nil {
		t.Errorf("SetUnqualifiedSearchRegistries() error = %v", err)
	}

	// Test getting unqualified search registries
	got := conf.GetUnqualifiedSearchRegistries()
	if len(got) != len(registries) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", got, registries)
	}
	for i, reg := range registries {
		if got[i] != reg {
			t.Errorf("GetUnqualifiedSearchRegistries()[%d] = %v, want %v", i, got[i], reg)
		}
	}

	// Test adding registry
	if err := conf.AddRegistry("example.com", "internal.example.com", false, false); err != nil {
		t.Errorf("AddRegistry() error = %v", err)
	}

	// Test adding alias
	if err := conf.AddAlias("myapp", "registry.example.com/namespace/myapp"); err != nil {
		t.Errorf("AddAlias() error = %v", err)
	}

	// Verify file was created and contains expected content
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("TOML file was not created")
	}
}

func TestRegistriesConf_LoadExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	// Create a TOML file with some content
	tomlContent := `unqualified-search-registries = ["docker.io", "registry.fedoraproject.org"]
credential-helpers = ["containers-auth.json"]

[[registry]]
prefix = "example.com"
location = "internal.example.com"
insecure = false

[aliases]
"myapp" = "registry.example.com/namespace/myapp"
`

	if err := os.WriteFile(path, []byte(tomlContent), 0644); err != nil {
		t.Fatalf("Failed to write test TOML file: %v", err)
	}

	// Create RegistriesConf the existing file
	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Verify loaded content
	registries := conf.GetUnqualifiedSearchRegistries()
	expected := []string{"docker.io", "registry.fedoraproject.org"}
	if len(registries) != len(expected) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", registries, expected)
	}

	credStore := conf.CredentialsStore()
	if credStore != "containers-auth.json" {
		t.Errorf("CredentialsStore() = %v, want containers-auth.json", credStore)
	}

	if !conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = false, want true")
	}
}

func TestRegistriesConf_LoadBasicConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "basic_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test unqualified search registries
	searchRegs := conf.GetUnqualifiedSearchRegistries()
	expected := []string{"docker.io", "registry.fedoraproject.org", "registry.access.redhat.com"}
	if len(searchRegs) != len(expected) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", searchRegs, expected)
	}
	for i, reg := range expected {
		if searchRegs[i] != reg {
			t.Errorf("GetUnqualifiedSearchRegistries()[%d] = %v, want %v", i, searchRegs[i], reg)
		}
	}

	// Test credential helpers
	if !conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = false, want true")
	}

	credStore := conf.CredentialsStore()
	if credStore != "containers-auth.json" {
		t.Errorf("CredentialsStore() = %v, want containers-auth.json", credStore)
	}

	credHelper := conf.GetCredentialHelper("any.server.com")
	if credHelper != "containers-auth.json" {
		t.Errorf("GetCredentialHelper() = %v, want containers-auth.json", credHelper)
	}

	// Test path
	if conf.Path() != testdataPath {
		t.Errorf("Path() = %v, want %v", conf.Path(), testdataPath)
	}
}

func TestRegistriesConf_LoadMirrorConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "mirror_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test unqualified search registries
	searchRegs := conf.GetUnqualifiedSearchRegistries()
	expected := []string{"docker.io"}
	if len(searchRegs) != len(expected) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", searchRegs, expected)
	}

	// Test that configuration is not considered to have auth configured
	// (no credential helpers in this file)
	if conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = true, want false")
	}

	if conf.CredentialsStore() != "" {
		t.Errorf("CredentialsStore() = %v, want empty", conf.CredentialsStore())
	}
}

func TestRegistriesConf_LoadEnterpriseConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "enterprise_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test unqualified search registries
	searchRegs := conf.GetUnqualifiedSearchRegistries()
	expected := []string{"docker.io", "internal.company.com"}
	if len(searchRegs) != len(expected) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", searchRegs, expected)
	}

	// Test multiple credential helpers
	if !conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = false, want true")
	}

	credStore := conf.CredentialsStore()
	if credStore != "pass" {
		t.Errorf("CredentialsStore() = %v, want pass", credStore)
	}

	// Test credential operations with in-memory storage
	serverAddr := "internal.company.com"
	cred := Credential{
		Username: "enterprise-user",
		Password: "enterprise-pass",
	}

	// Test putting credential
	if err := conf.PutCredential(serverAddr, cred); err != nil {
		t.Errorf("PutCredential() error = %v", err)
	}

	// Test getting credential
	gotCred, err := conf.GetCredential(serverAddr)
	if err != nil {
		t.Errorf("GetCredential() error = %v", err)
	}
	if gotCred != cred {
		t.Errorf("GetCredential() = %v, want %v", gotCred, cred)
	}

	// Test deleting credential
	if err := conf.DeleteCredential(serverAddr); err != nil {
		t.Errorf("DeleteCredential() error = %v", err)
	}

	// Verify credential was deleted
	gotCred, err = conf.GetCredential(serverAddr)
	if err != nil {
		t.Errorf("GetCredential() error = %v", err)
	}
	if gotCred != EmptyCredential {
		t.Errorf("GetCredential() after delete = %v, want %v", gotCred, EmptyCredential)
	}
}

func TestRegistriesConf_LoadEmptyConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "empty_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test empty configuration
	searchRegs := conf.GetUnqualifiedSearchRegistries()
	if len(searchRegs) != 0 {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want empty", searchRegs)
	}

	if conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = true, want false")
	}

	if conf.CredentialsStore() != "" {
		t.Errorf("CredentialsStore() = %v, want empty", conf.CredentialsStore())
	}
}

func TestRegistriesConf_LoadInvalidConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "invalid_registries.conf")

	_, err := NewRegistriesConf(testdataPath)
	if err == nil {
		t.Error("NewRegistriesConf() with invalid TOML should return error")
	}

	// Verify error contains TOML parse information
	errStr := err.Error()
	if !containsAny(errStr, []string{"TOML", "toml", "parse", "unmarshal"}) {
		t.Errorf("Error message should indicate TOML parse error, got: %v", errStr)
	}
}

func TestRegistriesConf_ModifyAndPersist(t *testing.T) {
	// Use a temporary copy of basic config
	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "registries.conf")

	// Note: We don't need to copy existing content, just test with empty config

	// Create new config with temp path
	conf, err := NewRegistriesConf(tempPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test adding unqualified search registries
	newRegistries := []string{"docker.io", "quay.io", "gcr.io"}
	if err := conf.SetUnqualifiedSearchRegistries(newRegistries); err != nil {
		t.Errorf("SetUnqualifiedSearchRegistries() error = %v", err)
	}

	// Test adding registry
	if err := conf.AddRegistry("test.example.com", "internal.test.example.com", false, false); err != nil {
		t.Errorf("AddRegistry() error = %v", err)
	}

	// Test adding alias
	if err := conf.AddAlias("test", "test.example.com/namespace/image"); err != nil {
		t.Errorf("AddAlias() error = %v", err)
	}

	// Test setting credential store
	if err := conf.SetCredentialsStore("pass"); err != nil {
		t.Errorf("SetCredentialsStore() error = %v", err)
	}

	// Reload configuration from file to verify persistence
	reloadedConf, err := NewRegistriesConf(tempPath)
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Verify unqualified search registries persisted
	gotRegistries := reloadedConf.GetUnqualifiedSearchRegistries()
	if len(gotRegistries) != len(newRegistries) {
		t.Errorf("Reloaded GetUnqualifiedSearchRegistries() = %v, want %v", gotRegistries, newRegistries)
	}

	// Verify credential store persisted
	if gotStore := reloadedConf.CredentialsStore(); gotStore != "pass" {
		t.Errorf("Reloaded CredentialsStore() = %v, want pass", gotStore)
	}

	// Verify auth is configured
	if !reloadedConf.IsAuthConfigured() {
		t.Error("Reloaded IsAuthConfigured() = false, want true")
	}
}

func TestRegistriesConf_HostnameMatchingFunctional(t *testing.T) {
	testdataPath := filepath.Join("testdata", "basic_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test credential hostname matching with different formats
	testCases := []struct {
		storeAddr   string
		queryAddr   string
		shouldMatch bool
	}{
		{"registry.example.com", "registry.example.com", true},
		{"https://registry.example.com/", "registry.example.com", true},
		{"http://registry.example.com", "registry.example.com", true},
		{"registry.example.com:5000", "registry.example.com:5000", true},
		{"registry.example.com", "different.example.com", false},
	}

	for _, tc := range testCases {
		t.Run(tc.storeAddr+"_"+tc.queryAddr, func(t *testing.T) {
			cred := Credential{
				Username: "testuser",
				Password: "testpass",
			}

			// Store credential with storeAddr
			if err := conf.PutCredential(tc.storeAddr, cred); err != nil {
				t.Errorf("PutCredential() error = %v", err)
			}

			// Query with queryAddr
			gotCred, err := conf.GetCredential(tc.queryAddr)
			if err != nil {
				t.Errorf("GetCredential() error = %v", err)
			}

			if tc.shouldMatch {
				if gotCred != cred {
					t.Errorf("GetCredential() = %v, want %v", gotCred, cred)
				}
			} else {
				if gotCred != EmptyCredential {
					t.Errorf("GetCredential() = %v, want %v", gotCred, EmptyCredential)
				}
			}

			// Clean up
			conf.DeleteCredential(tc.storeAddr)
		})
	}
}

func TestRegistriesConf_LoadPartialConfiguration(t *testing.T) {
	testdataPath := filepath.Join("testdata", "partial_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test unqualified search registries
	searchRegs := conf.GetUnqualifiedSearchRegistries()
	expected := []string{"docker.io"}
	if len(searchRegs) != len(expected) {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want %v", searchRegs, expected)
	}

	// Test that no auth is configured
	if conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = true, want false")
	}

	if conf.CredentialsStore() != "" {
		t.Errorf("CredentialsStore() = %v, want empty", conf.CredentialsStore())
	}

	if conf.GetCredentialHelper("any.server.com") != "" {
		t.Errorf("GetCredentialHelper() = %v, want empty", conf.GetCredentialHelper("any.server.com"))
	}
}

func TestRegistriesConf_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "definitely_does_not_exist.conf")

	conf, err := NewRegistriesConf(nonExistentPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() with non-existent file should not error, got: %v", err)
	}

	// Should create empty configuration initially
	if conf.IsAuthConfigured() {
		t.Error("IsAuthConfigured() = true, want false for non-existent file")
	}

	initialRegs := conf.GetUnqualifiedSearchRegistries()
	if len(initialRegs) != 0 {
		t.Errorf("GetUnqualifiedSearchRegistries() = %v, want empty for non-existent file", initialRegs)
	}

	// Should be able to add configuration and save
	if err := conf.SetUnqualifiedSearchRegistries([]string{"docker.io"}); err != nil {
		t.Errorf("SetUnqualifiedSearchRegistries() error = %v", err)
	}

	// Verify the change was applied
	updatedRegs := conf.GetUnqualifiedSearchRegistries()
	if len(updatedRegs) != 1 || updatedRegs[0] != "docker.io" {
		t.Errorf("After SetUnqualifiedSearchRegistries(), got %v, want [docker.io]", updatedRegs)
	}
}

func TestRegistriesConf_ConcurrentAccess(t *testing.T) {
	testdataPath := filepath.Join("testdata", "basic_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test concurrent read/write operations to ensure thread safety
	done := make(chan bool, 4)

	// Concurrent readers
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				conf.GetUnqualifiedSearchRegistries()
				conf.CredentialsStore()
				conf.IsAuthConfigured()
				_, _ = conf.GetCredential("test.example.com")
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 2; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				serverAddr := fmt.Sprintf("server%d-%d.example.com", id, j)
				cred := Credential{
					Username: fmt.Sprintf("user%d", id),
					Password: fmt.Sprintf("pass%d-%d", id, j),
				}
				conf.PutCredential(serverAddr, cred)
				conf.DeleteCredential(serverAddr)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestRegistriesConf_EmptyCredentialOperations(t *testing.T) {
	testdataPath := filepath.Join("testdata", "empty_registries.conf")

	conf, err := NewRegistriesConf(testdataPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Test getting non-existent credential
	cred, err := conf.GetCredential("nonexistent.example.com")
	if err != nil {
		t.Errorf("GetCredential() for non-existent server error = %v", err)
	}
	if cred != EmptyCredential {
		t.Errorf("GetCredential() for non-existent server = %v, want %v", cred, EmptyCredential)
	}

	// Test deleting non-existent credential (should not error)
	if err := conf.DeleteCredential("nonexistent.example.com"); err != nil {
		t.Errorf("DeleteCredential() for non-existent server error = %v", err)
	}

	// Test empty/invalid inputs
	if err := conf.PutCredential("", EmptyCredential); err != nil {
		t.Errorf("PutCredential() with empty server address error = %v", err)
	}

	if err := conf.DeleteCredential(""); err != nil {
		t.Errorf("DeleteCredential() with empty server address error = %v", err)
	}
}

func TestRegistriesConf_LargeConfiguration(t *testing.T) {
	// Test with many registries to ensure scalability
	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "large_registries.conf")

	conf, err := NewRegistriesConf(tempPath)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Add many unqualified search registries
	var largeRegList []string
	for i := 0; i < 100; i++ {
		largeRegList = append(largeRegList, fmt.Sprintf("registry%d.example.com", i))
	}

	if err := conf.SetUnqualifiedSearchRegistries(largeRegList); err != nil {
		t.Errorf("SetUnqualifiedSearchRegistries() with large list error = %v", err)
	}

	// Verify large list can be retrieved
	gotRegs := conf.GetUnqualifiedSearchRegistries()
	if len(gotRegs) != len(largeRegList) {
		t.Errorf("GetUnqualifiedSearchRegistries() length = %d, want %d", len(gotRegs), len(largeRegList))
	}

	// Add many aliases
	for i := 0; i < 50; i++ {
		alias := fmt.Sprintf("alias%d", i)
		target := fmt.Sprintf("registry%d.example.com/namespace/image", i)
		if err := conf.AddAlias(alias, target); err != nil {
			t.Errorf("AddAlias() error = %v", err)
		}
	}

	// Add many registries
	for i := 0; i < 50; i++ {
		prefix := fmt.Sprintf("registry%d.example.com", i)
		location := fmt.Sprintf("internal%d.example.com", i)
		if err := conf.AddRegistry(prefix, location, i%2 == 0, false); err != nil {
			t.Errorf("AddRegistry() error = %v", err)
		}
	}

	// Test that large configuration can be reloaded
	reloadedConf, err := NewRegistriesConf(tempPath)
	if err != nil {
		t.Fatalf("Failed to reload large config: %v", err)
	}

	reloadedRegs := reloadedConf.GetUnqualifiedSearchRegistries()
	if len(reloadedRegs) != len(largeRegList) {
		t.Errorf("Reloaded config registry count = %d, want %d", len(reloadedRegs), len(largeRegList))
	}
}

// containsAny checks if the string contains any of the given substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

func TestRegistriesConf_GetCredential_namespaceMatching(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "registries.conf")

	conf, err := NewRegistriesConf(path)
	if err != nil {
		t.Fatalf("NewRegistriesConf() error = %v", err)
	}

	// Set up namespace-specific credentials
	dockerDefault := Credential{
		Username: "default_user",
		Password: "default_pass",
	}
	terryloweNs := Credential{
		Username: "terrylhowe_user",
		Password: "terrylhowe_pass",
	}
	chainguardNs := Credential{
		Username: "chainguard_user",
		Password: "chainguard_pass",
	}
	teamNs := Credential{
		Username: "team_user",
		Password: "team_pass",
	}

	// Store credentials at different namespace levels
	if err := conf.PutCredential("docker.io", dockerDefault); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	if err := conf.PutCredential("docker.io/terrylhowe", terryloweNs); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	if err := conf.PutCredential("docker.io/chainguard", chainguardNs); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	if err := conf.PutCredential("registry.example.com/org/team", teamNs); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}

	tests := []struct {
		name          string
		serverAddress string
		want          Credential
		wantErr       bool
	}{
		{
			name:          "Match exact namespace: terrylhowe",
			serverAddress: "docker.io/terrylhowe",
			want:          terryloweNs,
		},
		{
			name:          "Match parent namespace: terrylhowe for helm image",
			serverAddress: "docker.io/terrylhowe/helm",
			want:          terryloweNs,
		},
		{
			name:          "Match different namespace: chainguard",
			serverAddress: "docker.io/chainguard/nginx",
			want:          chainguardNs,
		},
		{
			name:          "Fall back to registry-level credentials",
			serverAddress: "docker.io/unknown/image",
			want:          dockerDefault,
		},
		{
			name:          "Match deep namespace path",
			serverAddress: "registry.example.com/org/team/repo/image",
			want:          teamNs,
		},
		{
			name:          "No match returns empty credential",
			serverAddress: "unknown.registry.com/foo/bar",
			want:          EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := conf.GetCredential(tt.serverAddress)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetCredential() = %v, want %v", got, tt.want)
			}
		})
	}
}
