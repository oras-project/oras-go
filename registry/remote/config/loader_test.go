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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

func TestLoadConfigs_BothPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create Docker config.
	dockerConfig := `{"auths":{}}`
	dockerPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(dockerPath, []byte(dockerConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create registries.conf.
	regConf := `
[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
`
	regPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(regPath, []byte(regConf), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     dockerPath,
		RegistriesConfigPath: regPath,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.DockerConfig == nil {
		t.Error("DockerConfig should not be nil")
	}
	if configs.RegistriesConfig == nil {
		t.Error("RegistriesConfig should not be nil")
	}
}

func TestLoadConfigs_OnlyDockerConfig(t *testing.T) {
	tmpDir := t.TempDir()

	dockerConfig := `{"auths":{}}`
	dockerPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(dockerPath, []byte(dockerConfig), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     dockerPath,
		RegistriesConfigPath: filepath.Join(tmpDir, "nonexistent.conf"),
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.DockerConfig == nil {
		t.Error("DockerConfig should not be nil")
	}
	if configs.RegistriesConfig != nil {
		t.Error("RegistriesConfig should be nil when file does not exist")
	}
}

func TestLoadConfigs_OnlyRegistriesConfig(t *testing.T) {
	tmpDir := t.TempDir()

	regConf := `
[[registry]]
prefix = "docker.io"
`
	regPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(regPath, []byte(regConf), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     filepath.Join(tmpDir, "nonexistent.json"),
		RegistriesConfigPath: regPath,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.DockerConfig != nil {
		t.Error("DockerConfig should be nil when file does not exist")
	}
	if configs.RegistriesConfig == nil {
		t.Error("RegistriesConfig should not be nil")
	}
}

func TestLoadConfigs_NeitherPresent(t *testing.T) {
	tmpDir := t.TempDir()

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     filepath.Join(tmpDir, "nonexistent.json"),
		RegistriesConfigPath: filepath.Join(tmpDir, "nonexistent.conf"),
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.DockerConfig != nil {
		t.Error("DockerConfig should be nil")
	}
	if configs.RegistriesConfig != nil {
		t.Error("RegistriesConfig should be nil")
	}
}

func TestLoadConfigs_InvalidDockerConfig(t *testing.T) {
	tmpDir := t.TempDir()

	dockerPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(dockerPath, []byte("not json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     dockerPath,
		RegistriesConfigPath: filepath.Join(tmpDir, "nonexistent.conf"),
	})
	if err == nil {
		t.Fatal("LoadConfigsWithOptions() should return error for invalid Docker config")
	}
}

func TestLoadConfigs_InvalidRegistriesConfig(t *testing.T) {
	tmpDir := t.TempDir()

	regPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(regPath, []byte("not valid {{toml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:     filepath.Join(tmpDir, "nonexistent.json"),
		RegistriesConfigPath: regPath,
	})
	if err == nil {
		t.Fatal("LoadConfigsWithOptions() should return error for invalid registries config")
	}
}

func TestLoadConfigs_ContainersAuth_ExplicitPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create containers auth.json with hierarchical keys.
	authJSON := `{
		"auths": {
			"registry.example.com": {"auth": "cmVnaXN0cnk6cGFzcw=="},
			"registry.example.com/namespace": {"auth": "bmFtZXNwYWNlOnBhc3M="}
		}
	}`
	authPath := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(authJSON), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:   filepath.Join(tmpDir, "nonexistent.json"),
		ContainersAuthPath: authPath,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.ContainersAuthConfig == nil {
		t.Fatal("ContainersAuthConfig should not be nil")
	}

	// Verify hierarchical matching works.
	got, err := configs.ContainersAuthConfig.GetAuthConfigHierarchical("registry.example.com/namespace/repo")
	if err != nil {
		t.Fatalf("GetAuthConfigHierarchical() error: %v", err)
	}
	if got.Auth != "bmFtZXNwYWNlOnBhc3M=" {
		t.Errorf("GetAuthConfigHierarchical() auth = %v, want bmFtZXNwYWNlOnBhc3M=", got.Auth)
	}
}

func TestLoadConfigs_ContainersAuth_MissingPath(t *testing.T) {
	tmpDir := t.TempDir()

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:   filepath.Join(tmpDir, "nonexistent.json"),
		ContainersAuthPath: filepath.Join(tmpDir, "nonexistent-auth.json"),
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.ContainersAuthConfig != nil {
		t.Error("ContainersAuthConfig should be nil when file does not exist")
	}
}

func TestLoadConfigs_ContainersAuth_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	authPath := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(authPath, []byte("not json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath:   filepath.Join(tmpDir, "nonexistent.json"),
		ContainersAuthPath: authPath,
	})
	if err == nil {
		t.Fatal("LoadConfigsWithOptions() should return error for invalid containers auth")
	}
}

func TestLoadConfigs_ContainersAuth_DefaultXDGPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up XDG_RUNTIME_DIR with an auth.json.
	xdgDir := filepath.Join(tmpDir, "xdg-runtime")
	containersDir := filepath.Join(xdgDir, "containers")
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"auths": {"xdg.example.com": {"auth": "eGRnOnBhc3M="}}}`
	if err := os.WriteFile(filepath.Join(containersDir, "auth.json"), []byte(authJSON), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_RUNTIME_DIR", xdgDir)
	t.Setenv("DOCKER_CONFIG", tmpDir)
	t.Setenv("HOME", tmpDir)

	// Override system paths to avoid reading real system config.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	systemRegistriesConfPath = filepath.Join(tmpDir, "registries.conf")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "registries.conf.d")
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	configs, err := LoadConfigs()
	if err != nil {
		t.Fatalf("LoadConfigs() error: %v", err)
	}
	if configs.ContainersAuthConfig == nil {
		t.Fatal("ContainersAuthConfig should not be nil when XDG_RUNTIME_DIR auth.json exists")
	}

	got, err := configs.ContainersAuthConfig.GetAuthConfig("xdg.example.com")
	if err != nil {
		t.Fatalf("GetAuthConfig() error: %v", err)
	}
	if got.Auth != "eGRnOnBhc3M=" {
		t.Errorf("GetAuthConfig() auth = %v, want eGRnOnBhc3M=", got.Auth)
	}
}

func TestLoadConfigs_ContainersAuth_FallbackToHomeConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up $HOME/.config/containers/auth.json (no XDG_RUNTIME_DIR).
	containersDir := filepath.Join(tmpDir, ".config", "containers")
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"auths": {"home.example.com": {"auth": "aG9tZTpwYXNz"}}}`
	if err := os.WriteFile(filepath.Join(containersDir, "auth.json"), []byte(authJSON), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DOCKER_CONFIG", tmpDir)
	t.Setenv("HOME", tmpDir)

	// Override system paths.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	systemRegistriesConfPath = filepath.Join(tmpDir, "registries.conf")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "registries.conf.d")
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	configs, err := LoadConfigs()
	if err != nil {
		t.Fatalf("LoadConfigs() error: %v", err)
	}
	if configs.ContainersAuthConfig == nil {
		t.Fatal("ContainersAuthConfig should not be nil when $HOME/.config/containers/auth.json exists")
	}

	got, err := configs.ContainersAuthConfig.GetAuthConfig("home.example.com")
	if err != nil {
		t.Fatalf("GetAuthConfig() error: %v", err)
	}
	if got.Auth != "aG9tZTpwYXNz" {
		t.Errorf("GetAuthConfig() auth = %v, want aG9tZTpwYXNz", got.Auth)
	}
}

func TestLoadConfigs_DefaultPaths(t *testing.T) {
	// Set DOCKER_CONFIG and HOME to a temp dir to avoid reading real configs.
	tmpDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", tmpDir)
	t.Setenv("HOME", tmpDir)

	// Override system paths to avoid reading real system config.
	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	systemRegistriesConfPath = filepath.Join(tmpDir, "registries.conf")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "registries.conf.d")
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	// LoadConfigs should succeed even with no files.
	configs, err := LoadConfigs()
	if err != nil {
		t.Fatalf("LoadConfigs() error: %v", err)
	}
	if configs.DockerConfig != nil {
		t.Error("DockerConfig should be nil when no file exists")
	}
	if configs.RegistriesConfig != nil {
		t.Error("RegistriesConfig should be nil when no file exists")
	}
}

func TestConfigs_RegistryProperties(t *testing.T) {
	// Set up certs.d with a CA cert for the registry host.
	certsDir := t.TempDir()
	hostDir := filepath.Join(certsDir, "registry-1.docker.io")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}
	caCertPath := filepath.Join(hostDir, "ca.crt")
	if err := os.WriteFile(caCertPath, []byte("ca-cert-data"), 0644); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		RegistriesConfig: &RegistriesConfig{
			Registries: []Registry{
				{Prefix: "docker.io", Location: "registry-1.docker.io", Insecure: true},
			},
			Aliases: map[string]string{},
		},
		CertsDirPaths: []string{certsDir},
	}

	props, err := configs.RegistryProperties("docker.io/library/alpine:latest")
	if err != nil {
		t.Fatalf("RegistryProperties() error: %v", err)
	}
	if props.Reference.Registry != "registry-1.docker.io" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "registry-1.docker.io")
	}
	if props.Reference.Repository != "library/alpine" {
		t.Errorf("Reference.Repository = %q, want %q", props.Reference.Repository, "library/alpine")
	}
	if !props.Transport.Insecure {
		t.Error("Transport.Insecure should be true")
	}
	if len(props.Transport.CACerts) != 1 || props.Transport.CACerts[0] != caCertPath {
		t.Errorf("Transport.CACerts = %v, want [%s]", props.Transport.CACerts, caCertPath)
	}
}

func TestConfigs_RegistryProperties_NilRegistriesConfig(t *testing.T) {
	configs := &Configs{}

	props, err := configs.RegistryProperties("ghcr.io/user/repo:v1")
	if err != nil {
		t.Fatalf("RegistryProperties() error: %v", err)
	}
	if props.Reference.Registry != "ghcr.io" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "ghcr.io")
	}
	if props.Reference.Repository != "user/repo" {
		t.Errorf("Reference.Repository = %q, want %q", props.Reference.Repository, "user/repo")
	}
	if props.Transport.Insecure {
		t.Error("Transport.Insecure should be false")
	}
}

func TestConfigs_RegistryProperties_NoCerts(t *testing.T) {
	configs := &Configs{
		RegistriesConfig: &RegistriesConfig{
			Registries: []Registry{
				{Prefix: "example.com", Insecure: true},
			},
			Aliases: map[string]string{},
		},
	}

	props, err := configs.RegistryProperties("example.com/myimage:v1")
	if err != nil {
		t.Fatalf("RegistryProperties() error: %v", err)
	}
	if props.Reference.Registry != "example.com" {
		t.Errorf("Reference.Registry = %q, want %q", props.Reference.Registry, "example.com")
	}
	if !props.Transport.Insecure {
		t.Error("Transport.Insecure should be true")
	}
	if len(props.Transport.CACerts) != 0 {
		t.Errorf("Transport.CACerts = %v, want empty", props.Transport.CACerts)
	}
}

func TestConfigs_CredentialStore(t *testing.T) {
	dockerCfg := NewConfig()
	if err := dockerCfg.SetAuthConfig("docker.io", AuthConfig{Auth: "ZG9ja2VyOnBhc3M="}); err != nil {
		t.Fatal(err)
	}

	containersCfg := NewConfig()
	if err := containersCfg.SetAuthConfig("quay.io", AuthConfig{Auth: "cXVheTpwYXNz"}); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		DockerConfig:         dockerCfg,
		ContainersAuthConfig: containersCfg,
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore() error: %v", err)
	}

	ctx := context.Background()

	// Docker config credential should be found.
	cred, err := store.Get(ctx, "docker.io")
	if err != nil {
		t.Fatalf("Get(docker.io) error: %v", err)
	}
	if cred.Username != "docker" || cred.Password != "pass" {
		t.Errorf("Get(docker.io) = %v, want docker:pass", cred)
	}

	// Containers auth credential should be found (fallback).
	cred, err = store.Get(ctx, "quay.io")
	if err != nil {
		t.Fatalf("Get(quay.io) error: %v", err)
	}
	if cred.Username != "quay" || cred.Password != "pass" {
		t.Errorf("Get(quay.io) = %v, want quay:pass", cred)
	}

	// Unknown registry should return empty credential.
	cred, err = store.Get(ctx, "unknown.io")
	if err != nil {
		t.Fatalf("Get(unknown.io) error: %v", err)
	}
	if cred != credentials.EmptyCredential {
		t.Errorf("Get(unknown.io) = %v, want empty", cred)
	}
}

func TestConfigs_CredentialStore_DockerOnly(t *testing.T) {
	dockerCfg := NewConfig()
	if err := dockerCfg.SetAuthConfig("docker.io", AuthConfig{Auth: "ZG9ja2VyOnBhc3M="}); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		DockerConfig: dockerCfg,
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore() error: %v", err)
	}

	ctx := context.Background()
	cred, err := store.Get(ctx, "docker.io")
	if err != nil {
		t.Fatalf("Get(docker.io) error: %v", err)
	}
	if cred.Username != "docker" || cred.Password != "pass" {
		t.Errorf("Get(docker.io) = %v, want docker:pass", cred)
	}
}

func TestConfigs_CredentialStore_NoneLoaded(t *testing.T) {
	configs := &Configs{}

	_, err := configs.CredentialStore(credentials.StoreOptions{})
	if err == nil {
		t.Fatal("CredentialStore() should return error when no auth configs are loaded")
	}
}

