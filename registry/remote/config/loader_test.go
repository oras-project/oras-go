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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configpaths"
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
	isolateLoadEnv(t, tmpDir)

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
	isolateLoadEnv(t, tmpDir)

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
	isolateLoadEnv(t, tmpDir)

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
	if len(configs.ContainersAuthConfigs) != 1 {
		t.Fatalf("ContainersAuthConfigs length = %d, want 1", len(configs.ContainersAuthConfigs))
	}
	if configs.ContainersAuthConfigs[0] != configs.ContainersAuthConfig {
		t.Error("ContainersAuthConfig should be ContainersAuthConfigs[0]")
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
	if len(configs.ContainersAuthConfigs) != 0 {
		t.Errorf("ContainersAuthConfigs = %v, want empty", configs.ContainersAuthConfigs)
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
	if runtime.GOOS != "linux" {
		t.Skip("XDG_RUNTIME_DIR is only used on Linux")
	}
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
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

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
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

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
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

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

	if err := containersCfg.SetAuthConfig("reg.example.com", AuthConfig{Auth: "YXV0aGpzb246cGFzcw=="}); err != nil {
		t.Fatal(err)
	}
	if err := dockerCfg.SetAuthConfig("reg.example.com", AuthConfig{Auth: "ZG9ja2VyOnBhc3M="}); err != nil {
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

	cred, err := store.Get(ctx, "docker.io")
	if err != nil {
		t.Fatalf("Get(docker.io) error: %v", err)
	}
	if cred.Username != "docker" || cred.Password != "pass" {
		t.Errorf("Get(docker.io) = %v, want docker:pass", cred)
	}

	cred, err = store.Get(ctx, "quay.io")
	if err != nil {
		t.Fatalf("Get(quay.io) error: %v", err)
	}
	if cred.Username != "quay" || cred.Password != "pass" {
		t.Errorf("Get(quay.io) = %v, want quay:pass", cred)
	}

	cred, err = store.Get(ctx, "reg.example.com")
	if err != nil {
		t.Fatalf("Get(reg.example.com) error: %v", err)
	}
	if cred.Username != "docker" || cred.Password != "pass" {
		t.Errorf("Get(reg.example.com) = %v, want docker:pass (Docker before auth.json)", cred)
	}

	configs.PreferContainersAuth = true
	store, err = configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore(PreferContainersAuth) error: %v", err)
	}
	cred, err = store.Get(ctx, "reg.example.com")
	if err != nil {
		t.Fatalf("Get(reg.example.com) prefer error: %v", err)
	}
	if cred.Username != "authjson" || cred.Password != "pass" {
		t.Errorf("Get(reg.example.com) = %v, want authjson:pass (auth.json before Docker)", cred)
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

func TestConfigs_PolicyEvaluator_NilPolicy(t *testing.T) {
	configs := &Configs{}
	evaluator, err := configs.PolicyEvaluator()
	if err != nil {
		t.Fatalf("PolicyEvaluator() error: %v", err)
	}
	if evaluator != nil {
		t.Error("PolicyEvaluator() should return nil when no policy is loaded")
	}
}

func TestLoadConfigsWithOptions_PolicyConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	isolateLoadEnv(t, tmpDir)

	policyJSON := `{
  "default": [{"type": "insecureAcceptAnything"}]
}`
	policyPath := filepath.Join(tmpDir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(policyJSON), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		PolicyConfigPath: policyPath,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.PolicyConfig == nil {
		t.Fatal("PolicyConfig should not be nil when PolicyConfigPath is set")
	}

	evaluator, err := configs.PolicyEvaluator()
	if err != nil {
		t.Fatalf("PolicyEvaluator() error: %v", err)
	}
	if evaluator == nil {
		t.Error("PolicyEvaluator() should return non-nil when policy is loaded")
	}
}

func TestLoadConfigsWithOptions_CertsDirPathsOverride(t *testing.T) {
	tmpDir := t.TempDir()
	isolateLoadEnv(t, tmpDir)

	certsDirs := []string{filepath.Join(tmpDir, "certs.d"), filepath.Join(tmpDir, "certs2.d")}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath: filepath.Join(tmpDir, "nonexistent.json"),
		CertsDirPaths:    certsDirs,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if len(configs.CertsDirPaths) != 2 {
		t.Errorf("CertsDirPaths length = %d, want 2", len(configs.CertsDirPaths))
	}
	if configs.CertsDirPaths[0] != certsDirs[0] || configs.CertsDirPaths[1] != certsDirs[1] {
		t.Errorf("CertsDirPaths = %v, want %v", configs.CertsDirPaths, certsDirs)
	}
}

func TestLoadConfigsWithOptions_RegistriesDConfig_DefaultPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Override system paths so registries.d falls through to system default.
	origPath := systemRegistriesDPath
	systemRegistriesDPath = filepath.Join(tmpDir, "nonexistent")
	defer func() { systemRegistriesDPath = origPath }()

	origSysPath := systemRegistriesConfPath
	origSysDirPath := systemRegistriesConfDirPath
	systemRegistriesConfPath = filepath.Join(tmpDir, "registries.conf")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "registries.conf.d")
	defer func() {
		systemRegistriesConfPath = origSysPath
		systemRegistriesConfDirPath = origSysDirPath
	}()

	t.Setenv("DOCKER_CONFIG", tmpDir)
	t.Setenv("HOME", tmpDir)

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	// RegistriesDConfig should be nil when no files found.
	if configs.RegistriesDConfig != nil {
		t.Error("RegistriesDConfig should be nil when no registries.d files found")
	}
}

func TestLoadConfigsWithOptions_RegistriesConfig_UAPI_Strategy(t *testing.T) {
	// Skip if any system-level UAPI registries.conf exists; this test requires
	// that no system or vendor file is present to verify the not-found path.
	for _, p := range []string{
		"/etc/containers/registries.conf",
		"/usr/share/containers/registries.conf",
	} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("skipping: %s exists on this system", p)
		}
	}

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("DOCKER_CONFIG", tmpDir)

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		Strategy: StrategyUAPI,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions(UAPI) error: %v", err)
	}
	// No configs should be found, but should not error.
	if configs.RegistriesConfig != nil {
		t.Error("RegistriesConfig should be nil when no files found with UAPI strategy")
	}
}

func TestLoadConfigs_UAPI_DockerConfigNotInContainersAuth(t *testing.T) {
	tmpDir := t.TempDir()
	isolateLoadEnv(t, tmpDir)
	writeFile(t, filepath.Join(tmpDir, ".docker", "config.json"), `{"auths":{"docker.example.com":{"auth":"ZG9ja2VyOnBhc3M="}}}`)

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		Strategy: StrategyUAPI,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions(UAPI) error: %v", err)
	}
	if configs.ContainersAuthConfig != nil || len(configs.ContainersAuthConfigs) != 0 {
		t.Fatal("~/.docker/config.json must not appear in ContainersAuthConfigs with StrategyUAPI")
	}
}

func TestLoadConfigsWithOptions_InvalidPolicyConfig(t *testing.T) {
	tmpDir := t.TempDir()

	policyPath := filepath.Join(tmpDir, "policy.json")
	if err := os.WriteFile(policyPath, []byte("not valid json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath: filepath.Join(tmpDir, "nonexistent.json"),
		PolicyConfigPath: policyPath,
	})
	if err == nil {
		t.Fatal("LoadConfigsWithOptions() should return error for invalid policy config")
	}
}

func TestConfigs_CredentialStore_ContainersAuthOnly(t *testing.T) {
	containersCfg := NewConfig()
	if err := containersCfg.SetAuthConfig("quay.io", AuthConfig{Auth: "cXVheTpwYXNz"}); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		ContainersAuthConfig: containersCfg,
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore() error: %v", err)
	}

	cred, err := store.Get(context.Background(), "quay.io")
	if err != nil {
		t.Fatalf("Get(quay.io) error: %v", err)
	}
	if cred.Username != "quay" || cred.Password != "pass" {
		t.Errorf("Get(quay.io) = %v, want quay:pass", cred)
	}
}

func TestConfigs_CredentialStore_ContainersAuth_Hierarchical(t *testing.T) {
	// containers-auth.json store should resolve credentials by longest-prefix
	// namespace matching, so a lookup for a sub-path resolves to the namespace
	// entry. base64("namespace:pass") = bmFtZXNwYWNlOnBhc3M=
	containersCfg := NewConfig()
	if err := containersCfg.SetAuthConfig("registry.example.com/namespace", AuthConfig{Auth: "bmFtZXNwYWNlOnBhc3M="}); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		ContainersAuthConfig: containersCfg,
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore() error: %v", err)
	}

	cred, err := store.Get(context.Background(), "registry.example.com/namespace/repo")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if cred.Username != "namespace" || cred.Password != "pass" {
		t.Errorf("Get(registry.example.com/namespace/repo) = %v, want namespace:pass", cred)
	}
}

func TestConfigs_CredentialStore_DockerNotHierarchical(t *testing.T) {
	// The Docker config store uses exact hostname matching, so a lookup for a
	// namespaced sub-path must NOT resolve to the namespace entry.
	dockerCfg := NewConfig()
	if err := dockerCfg.SetAuthConfig("registry.example.com/namespace", AuthConfig{Auth: "bmFtZXNwYWNlOnBhc3M="}); err != nil {
		t.Fatal(err)
	}

	configs := &Configs{
		DockerConfig: dockerCfg,
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{})
	if err != nil {
		t.Fatalf("CredentialStore() error: %v", err)
	}

	cred, err := store.Get(context.Background(), "registry.example.com/namespace/repo")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if cred != credentials.EmptyCredential {
		t.Errorf("Get(registry.example.com/namespace/repo) = %v, want empty (no prefix matching for Docker config)", cred)
	}
}

func isolateLoadEnv(t *testing.T, tmpDir string) {
	t.Helper()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("DOCKER_CONFIG", filepath.Join(tmpDir, "docker"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdgconfig"))
	t.Setenv("XDG_RUNTIME_DIR", "")
	origSys, origDir := systemRegistriesConfPath, systemRegistriesConfDirPath
	systemRegistriesConfPath = filepath.Join(tmpDir, "registries.conf")
	systemRegistriesConfDirPath = filepath.Join(tmpDir, "registries.conf.d")
	t.Cleanup(func() {
		systemRegistriesConfPath, systemRegistriesConfDirPath = origSys, origDir
	})
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeAuthJSON(t *testing.T, dir, host, auth string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	writeFile(t, path, fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, auth))
	return path
}

func fileAuth(t *testing.T, path, host string) string {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.GetAuthConfig(host)
	if err != nil {
		t.Fatal(err)
	}
	return got.Auth
}

func TestLoadConfigs_ContainersAuthChain(t *testing.T) {
	const primaryB64, xdgB64, overrideB64, dockerB64 = "cHJpbWFyeTpwYXNz", "eGRnOnBhc3M=", "b3ZlcnJpZGU6cGFzcw==", "ZG9ja2VyOnBhc3M="
	putOpts := credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true}
	tests := []struct {
		name, dockerJSON string
		files            [][3]string // dir, host, auth
		opts             func(tmp, override string) LoadConfigsOptions
		prefer           bool
		wantErr          bool
		wantN            int
		wantUser         map[string]string
		noUser           []string
		put              bool
	}{
		{
			name:     "prefer: primary and XDG config, same registry: primary wins",
			files:    [][3]string{{".config/containers", "reg.example.com", primaryB64}, {"xdgconfig/containers", "reg.example.com", xdgB64}},
			prefer:   true,
			wantN:    2,
			wantUser: map[string]string{"reg.example.com": "primary"},
		},
		{
			name:   "prefer: primary and XDG config, different registries",
			files:  [][3]string{{".config/containers", "primary.example.com", primaryB64}, {"xdgconfig/containers", "xdg.example.com", xdgB64}},
			prefer: true,
			wantN:  2,
			wantUser: map[string]string{
				"primary.example.com": "primary",
				"xdg.example.com":     "xdg",
			},
		},
		{
			name:     "prefer: primary missing, XDG config used",
			files:    [][3]string{{"xdgconfig/containers", "xdg.example.com", xdgB64}},
			prefer:   true,
			wantN:    1,
			wantUser: map[string]string{"xdg.example.com": "xdg"},
		},
		{
			name:       "DOCKER_CONFIG is not containers auth; put does not create auth.json",
			dockerJSON: `{"auths":{"docker.example.com":{"auth":"` + dockerB64 + `"}}}`,
			wantN:      0,
			wantUser:   map[string]string{"docker.example.com": "docker"},
			put:        true,
		},
		{
			name:  "ContainersAuthPath ignores fallbacks",
			files: [][3]string{{"xdgconfig/containers", "fallback.example.com", xdgB64}, {"override", "override.example.com", overrideB64}},
			opts: func(tmp, override string) LoadConfigsOptions {
				return LoadConfigsOptions{DockerConfigPath: filepath.Join(tmp, "none.json"), ContainersAuthPath: override}
			},
			wantN:    1,
			wantUser: map[string]string{"override.example.com": "override"},
			noUser:   []string{"fallback.example.com"},
		},
		{
			name:       "DockerConfigPath override does not load default docker as containers",
			dockerJSON: `{"auths":{"docker.example.com":{"auth":"` + dockerB64 + `"}}}`,
			opts: func(tmp, override string) LoadConfigsOptions {
				p := filepath.Join(tmp, "override-docker.json")
				writeFile(t, p, `{"auths":{}}`)
				return LoadConfigsOptions{DockerConfigPath: p}
			},
			wantN: 0,
		},
		{
			name:    "prefer: invalid JSON in fallback is an error",
			prefer:  true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			isolateLoadEnv(t, tmp)
			var override string
			for _, f := range tt.files {
				p := writeAuthJSON(t, filepath.Join(tmp, f[0]), f[1], f[2])
				if f[0] == "override" {
					override = p
				}
			}
			if tt.dockerJSON != "" {
				writeFile(t, filepath.Join(tmp, "docker", "config.json"), tt.dockerJSON)
			}
			if tt.wantErr {
				writeFile(t, filepath.Join(tmp, "xdgconfig", "containers", "auth.json"), "not json{{{")
			}
			opts := LoadConfigsOptions{PreferContainersAuth: tt.prefer}
			if tt.opts != nil {
				opts = tt.opts(tmp, override)
				opts.PreferContainersAuth = tt.prefer
			}
			configs, err := LoadConfigsWithOptions(opts)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(configs.ContainersAuthConfigs) != tt.wantN {
				t.Fatalf("ContainersAuthConfigs length = %d, want %d", len(configs.ContainersAuthConfigs), tt.wantN)
			}
			if (tt.wantN == 0) != (configs.ContainersAuthConfig == nil) {
				t.Fatal("ContainersAuthConfig / ContainersAuthConfigs mismatch")
			}
			if tt.wantN > 0 && configs.ContainersAuthConfig != configs.ContainersAuthConfigs[0] {
				t.Fatal("ContainersAuthConfig should be ContainersAuthConfigs[0]")
			}
			store, err := configs.CredentialStore(putOpts)
			if err != nil {
				t.Fatal(err)
			}
			for host, user := range tt.wantUser {
				cred, err := store.Get(context.Background(), host)
				if err != nil {
					t.Fatal(err)
				}
				if cred.Username != user {
					t.Errorf("Get(%s) user = %q, want %q", host, cred.Username, user)
				}
			}
			for _, host := range tt.noUser {
				cred, err := store.Get(context.Background(), host)
				if err != nil {
					t.Fatal(err)
				}
				if cred.Username != "" {
					t.Errorf("Get(%s) user = %q, want empty", host, cred.Username)
				}
			}
			if !tt.put {
				return
			}
			if err := store.Put(context.Background(), "put.example.com", credentials.Credential{Username: "user", Password: "pass"}); err != nil {
				t.Fatal(err)
			}
			if fileAuth(t, filepath.Join(tmp, "docker", "config.json"), "put.example.com") == "" {
				t.Error("Put should write Docker when it is the only store")
			}
			for _, rel := range []string{filepath.Join(".config", "containers", "auth.json"), filepath.Join("xdgconfig", "containers", "auth.json")} {
				if _, err := os.Stat(filepath.Join(tmp, rel)); err == nil {
					t.Errorf("Put created %s", rel)
				}
			}
		})
	}
}

func TestLoadConfigs_Default_DockerFirst(t *testing.T) {
	tmp := t.TempDir()
	isolateLoadEnv(t, tmp)
	const host, dockerB64, authB64, xdgB64 = "reg.example.com", "ZG9ja2VyOnBhc3M=", "YXV0aGpzb246cGFzcw==", "eGRnOnBhc3M="
	writeAuthJSON(t, filepath.Join(tmp, ".config", "containers"), host, authB64)
	writeAuthJSON(t, filepath.Join(tmp, "xdgconfig", "containers"), host, xdgB64)
	writeFile(t, filepath.Join(tmp, "docker", "config.json"), fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, dockerB64))

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if configs.PreferContainersAuth {
		t.Fatal("PreferContainersAuth must default to false")
	}
	if len(configs.ContainersAuthConfigs) != 1 {
		t.Fatalf("ContainersAuthConfigs length = %d, want 1 (primary only)", len(configs.ContainersAuthConfigs))
	}
	storeOpts := credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true}
	store, err := configs.CredentialStore(storeOpts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.(*credentialStore); ok {
		t.Fatal("default CredentialStore must not return credentialStore wrapper")
	}
	cred, err := store.Get(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Username != "docker" {
		t.Fatalf("Get(%s) user = %q, want docker", host, cred.Username)
	}
	if err := store.Put(context.Background(), "put.example.com", credentials.Credential{Username: "user", Password: "pass"}); err != nil {
		t.Fatal(err)
	}
	if fileAuth(t, filepath.Join(tmp, "docker", "config.json"), "put.example.com") == "" {
		t.Error("default Put should write Docker")
	}
	if fileAuth(t, filepath.Join(tmp, ".config", "containers", "auth.json"), "put.example.com") != "" {
		t.Error("default Put should not write auth.json")
	}
	if err := store.Delete(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if fileAuth(t, filepath.Join(tmp, "docker", "config.json"), host) != "" {
		t.Error("default Delete should clear Docker")
	}
	if fileAuth(t, filepath.Join(tmp, ".config", "containers", "auth.json"), host) == "" {
		t.Error("default Delete should not clear auth.json")
	}
}

func TestLoadConfigs_DockerConfigPathAliasesPrimary_FlagOff(t *testing.T) {
	tmp := t.TempDir()
	isolateLoadEnv(t, tmp)
	primary, err := configpaths.NewResolver(configpaths.ContainersImage).AuthPrimaryPath()
	if err != nil {
		t.Fatal(err)
	}
	const nsAuth = "bmFtZXNwYWNlOnBhc3M=" // namespace:pass
	writeFile(t, primary, fmt.Sprintf(`{"auths":{"reg.example.com/ns":{"auth":%q}}}`, nsAuth))

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{DockerConfigPath: primary})
	if err != nil {
		t.Fatal(err)
	}
	if configs.DockerConfig == nil {
		t.Fatal("DockerConfig should load the aliased primary file")
	}
	if configs.ContainersAuthConfig == nil || len(configs.ContainersAuthConfigs) != 1 {
		t.Fatalf("ContainersAuthConfig = %v, len(ContainersAuthConfigs) = %d, want primary loaded", configs.ContainersAuthConfig, len(configs.ContainersAuthConfigs))
	}
	if configs.ContainersAuthConfig != configs.ContainersAuthConfigs[0] {
		t.Fatal("ContainersAuthConfig should be ContainersAuthConfigs[0]")
	}

	store, err := configs.CredentialStore(credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.(*credentialStore); ok {
		t.Fatal("flag off must not return credentialStore wrapper")
	}
	if _, ok := store.(*credentials.DynamicStore); ok {
		t.Fatal("flag off with Docker and auth.json must use NewStoreWithFallbacks, not a single DynamicStore")
	}
	cred, err := store.Get(context.Background(), "reg.example.com/ns/repo")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Username != "namespace" || cred.Password != "pass" {
		t.Fatalf("Get(reg.example.com/ns/repo) = %v, want namespace:pass", cred)
	}
}

func TestPreferContainersAuth_LoadAndStore(t *testing.T) {
	storeOpts := credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true}
	const host, dockerB64, authB64, xdgB64 = "reg.example.com", "ZG9ja2VyOnBhc3M=", "YXV0aGpzb246cGFzcw==", "eGRnOnBhc3M="

	t.Run("flag off: malformed distinct fallback is ignored", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		writeAuthJSON(t, filepath.Join(tmp, ".config", "containers"), host, authB64)
		writeFile(t, filepath.Join(tmp, "xdgconfig", "containers", "auth.json"), "not json{{{")
		writeFile(t, filepath.Join(tmp, "docker", "config.json"), `{"auths":{}}`)
		if _, err := LoadConfigsWithOptions(LoadConfigsOptions{}); err != nil {
			t.Fatalf("flag off LoadConfigs error: %v", err)
		}
	})

	t.Run("flag on: malformed distinct fallback is an error", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		writeAuthJSON(t, filepath.Join(tmp, ".config", "containers"), host, authB64)
		writeFile(t, filepath.Join(tmp, "xdgconfig", "containers", "auth.json"), "not json{{{")
		_, err := LoadConfigsWithOptions(LoadConfigsOptions{PreferContainersAuth: true})
		if err == nil {
			t.Fatal("flag on LoadConfigs: expected error for malformed fallback")
		}
	})

	t.Run("flag off: missing primary does not load fallback", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		writeAuthJSON(t, filepath.Join(tmp, "xdgconfig", "containers"), host, xdgB64)
		writeFile(t, filepath.Join(tmp, "docker", "config.json"), `{"auths":{}}`)
		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(configs.ContainersAuthConfigs) != 0 {
			t.Fatalf("ContainersAuthConfigs length = %d, want 0", len(configs.ContainersAuthConfigs))
		}
	})

	t.Run("flag on: missing primary loads fallback", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		writeAuthJSON(t, filepath.Join(tmp, "xdgconfig", "containers"), host, xdgB64)
		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{PreferContainersAuth: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(configs.ContainersAuthConfigs) != 1 {
			t.Fatalf("ContainersAuthConfigs length = %d, want 1", len(configs.ContainersAuthConfigs))
		}
		store, err := configs.CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		cred, err := store.Get(context.Background(), host)
		if err != nil {
			t.Fatal(err)
		}
		if cred.Username != "xdg" {
			t.Errorf("Get user = %q, want xdg", cred.Username)
		}
	})

	t.Run("ContainersAuthPath ignores fallbacks with flag on", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		writeAuthJSON(t, filepath.Join(tmp, "xdgconfig", "containers"), "fallback.example.com", xdgB64)
		override := writeAuthJSON(t, filepath.Join(tmp, "override"), "override.example.com", "b3ZlcnJpZGU6cGFzcw==")
		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
			PreferContainersAuth: true,
			ContainersAuthPath:   override,
			DockerConfigPath:     filepath.Join(tmp, "none.json"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(configs.ContainersAuthConfigs) != 1 {
			t.Fatalf("ContainersAuthConfigs length = %d, want 1", len(configs.ContainersAuthConfigs))
		}
		store, err := configs.CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		cred, err := store.Get(context.Background(), "override.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if cred.Username != "override" {
			t.Errorf("Get user = %q, want override", cred.Username)
		}
		cred, err = store.Get(context.Background(), "fallback.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if cred.Username != "" {
			t.Errorf("Get(fallback) user = %q, want empty", cred.Username)
		}
	})

	t.Run("empty auth.json Put target", func(t *testing.T) {
		for _, prefer := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefer=%v", prefer), func(t *testing.T) {
				tmp := t.TempDir()
				authPath, dockerPath := filepath.Join(tmp, "auth.json"), filepath.Join(tmp, "docker.json")
				writeFile(t, authPath, `{"auths":{}}`)
				writeFile(t, dockerPath, `{"auths":{}}`)
				authCfg, err := Load(authPath)
				if err != nil {
					t.Fatal(err)
				}
				dockerCfg, err := Load(dockerPath)
				if err != nil {
					t.Fatal(err)
				}
				store, err := (&Configs{
					DockerConfig:          dockerCfg,
					ContainersAuthConfig:  authCfg,
					ContainersAuthConfigs: []*Config{authCfg},
					PreferContainersAuth:  prefer,
				}).CredentialStore(storeOpts)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Put(context.Background(), host, credentials.Credential{Username: "user", Password: "pass"}); err != nil {
					t.Fatal(err)
				}
				wroteAuth := fileAuth(t, authPath, host) != ""
				wroteDocker := fileAuth(t, dockerPath, host) != ""
				if prefer {
					if !wroteAuth || wroteDocker {
						t.Errorf("prefer on: wroteAuth=%v wroteDocker=%v, want auth.json only", wroteAuth, wroteDocker)
					}
				} else if wroteAuth || !wroteDocker {
					t.Errorf("prefer off: wroteAuth=%v wroteDocker=%v, want Docker only", wroteAuth, wroteDocker)
				}
			})
		}
	})

	t.Run("flag off: NewStoreWithFallbacks construction", func(t *testing.T) {
		dockerCfg := NewConfig()
		if err := dockerCfg.SetAuthConfig(host, AuthConfig{Auth: dockerB64}); err != nil {
			t.Fatal(err)
		}
		authCfg := NewConfig()
		if err := authCfg.SetAuthConfig(host, AuthConfig{Auth: authB64}); err != nil {
			t.Fatal(err)
		}
		store, err := (&Configs{
			DockerConfig:          dockerCfg,
			ContainersAuthConfig:  authCfg,
			ContainersAuthConfigs: []*Config{authCfg},
		}).CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := store.(*credentialStore); ok {
			t.Fatal("flag off must return NewStoreWithFallbacks, not credentialStore")
		}
		if _, ok := store.(*credentials.DynamicStore); ok {
			t.Fatal("flag off with two stores must not return a single DynamicStore")
		}
	})

	t.Run("flag on: credentialStore wrapper", func(t *testing.T) {
		dockerCfg := NewConfig()
		if err := dockerCfg.SetAuthConfig(host, AuthConfig{Auth: dockerB64}); err != nil {
			t.Fatal(err)
		}
		authCfg := NewConfig()
		if err := authCfg.SetAuthConfig(host, AuthConfig{Auth: authB64}); err != nil {
			t.Fatal(err)
		}
		store, err := (&Configs{
			DockerConfig:          dockerCfg,
			ContainersAuthConfig:  authCfg,
			ContainersAuthConfigs: []*Config{authCfg},
			PreferContainersAuth:  true,
		}).CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := store.(*credentialStore); !ok {
			t.Fatalf("flag on with two stores type = %T, want *credentialStore", store)
		}
	})
}

func TestConfigs_CredentialStore_DeleteAllStores(t *testing.T) {
	tmp := t.TempDir()
	authPath, dockerPath := filepath.Join(tmp, "auth.json"), filepath.Join(tmp, "docker.json")
	const host, auth = "reg.example.com", "dXNlcjpwYXNz"
	writeFile(t, authPath, fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, auth))
	writeFile(t, dockerPath, fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, auth))
	authCfg, err := Load(authPath)
	if err != nil {
		t.Fatal(err)
	}
	dockerCfg, err := Load(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := (&Configs{
		DockerConfig: dockerCfg, ContainersAuthConfig: authCfg, ContainersAuthConfigs: []*Config{authCfg},
		PreferContainersAuth: true,
	}).CredentialStore(credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), host); err != nil {
		t.Fatalf("Delete(%s) error: %v", host, err)
	}
	cred, err := store.Get(context.Background(), host)
	if err != nil {
		t.Fatalf("Get(%s) error: %v", host, err)
	}
	if cred != credentials.EmptyCredential {
		t.Errorf("Get(%s) = %v, want empty after Delete", host, cred)
	}
	if fileAuth(t, authPath, host) != "" {
		t.Error("Delete should remove the host from auth.json")
	}
	if fileAuth(t, dockerPath, host) != "" {
		t.Error("Delete should remove the host from Docker config.json")
	}
}

func TestConfigs_CredentialStore_DeletePartialFailure(t *testing.T) {
	errFirst := errors.New("erase failed")
	errSecond := errors.New("docker erase failed")
	tests := []struct {
		name      string
		firstErr  error
		secondErr error
		wantIs    []error
	}{
		{
			name:     "first fails, second succeeds",
			firstErr: errFirst,
			wantIs:   []error{errFirst},
		},
		{
			name:      "both fail",
			firstErr:  errFirst,
			secondErr: errSecond,
			wantIs:    []error{errFirst, errSecond},
		},
		{
			name:      "first not-found, second real error",
			firstErr:  credentials.ErrCredentialsNotFound,
			secondErr: errSecond,
			wantIs:    []error{errSecond},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secondDeleted := false
			store := &credentialStore{
				stores: []credentials.Store{
					&stubCredentialStore{deleteErr: tt.firstErr},
					&stubCredentialStore{deleteErr: tt.secondErr, deleted: &secondDeleted},
				},
			}
			err := store.Delete(context.Background(), "reg.example.com")
			if !secondDeleted {
				t.Error("Delete should still visit the second store")
			}
			for _, want := range tt.wantIs {
				if !errors.Is(err, want) {
					t.Errorf("Delete() error = %v, want errors.Is %v", err, want)
				}
			}
			if tt.secondErr == nil && errors.Is(err, errSecond) {
				t.Errorf("Delete() error = %v, should not include second error", err)
			}
			if errors.Is(tt.firstErr, credentials.ErrCredentialsNotFound) && errors.Is(err, credentials.ErrCredentialsNotFound) {
				t.Errorf("Delete() error = %v, should not include ErrCredentialsNotFound", err)
			}
		})
	}
}

func TestConfigs_CredentialStore_PutTwoAuthJSON(t *testing.T) {
	tmp := t.TempDir()
	firstPath, secondPath := filepath.Join(tmp, "first.json"), filepath.Join(tmp, "second.json")
	writeFile(t, firstPath, `{"auths":{}}`)
	writeFile(t, secondPath, `{"auths":{}}`)
	firstCfg, err := Load(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondCfg, err := Load(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := (&Configs{
		ContainersAuthConfig:  firstCfg,
		ContainersAuthConfigs: []*Config{firstCfg, secondCfg},
	}).CredentialStore(credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "reg.example.com", credentials.Credential{Username: "user", Password: "pass"}); err != nil {
		t.Fatal(err)
	}
	if fileAuth(t, firstPath, "reg.example.com") == "" {
		t.Error("Put should write the first auth.json")
	}
	if fileAuth(t, secondPath, "reg.example.com") != "" {
		t.Error("Put should not write the second auth.json")
	}
}

func Test_credentialStore_MatchesNamespace(t *testing.T) {
	inner := credentials.NewStoreWithFallbacks(&stubCredentialStore{match: true}, &stubCredentialStore{match: true})
	store := &credentialStore{inner: inner}
	if !store.MatchesNamespace("reg.example.com/ns") {
		t.Error("MatchesNamespace should delegate to inner")
	}
	inner = credentials.NewStoreWithFallbacks(&stubCredentialStore{match: true}, &stubCredentialStore{})
	store = &credentialStore{inner: inner}
	if store.MatchesNamespace("reg.example.com/ns") {
		t.Error("MatchesNamespace should be false when inner is false")
	}
}

type stubCredentialStore struct {
	deleteErr error
	deleted   *bool
	match     bool
}

func (s *stubCredentialStore) Get(context.Context, string) (credentials.Credential, error) {
	return credentials.EmptyCredential, nil
}

func (s *stubCredentialStore) Put(context.Context, string, credentials.Credential) error {
	return nil
}

func (s *stubCredentialStore) Delete(context.Context, string) error {
	if s.deleted != nil {
		*s.deleted = true
	}
	return s.deleteErr
}

func (s *stubCredentialStore) MatchesNamespace(string) bool {
	return s.match
}

func TestConfigs_CredentialStore_PutAuthJSON(t *testing.T) {
	tmp := t.TempDir()
	authPath, dockerPath := filepath.Join(tmp, "auth.json"), filepath.Join(tmp, "docker.json")
	writeFile(t, authPath, `{"auths":{}}`)
	writeFile(t, dockerPath, `{"auths":{}}`)
	authCfg, err := Load(authPath)
	if err != nil {
		t.Fatal(err)
	}
	dockerCfg, err := Load(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := (&Configs{
		DockerConfig: dockerCfg, ContainersAuthConfig: authCfg, ContainersAuthConfigs: []*Config{authCfg},
		PreferContainersAuth: true,
	}).CredentialStore(credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "reg.example.com", credentials.Credential{Username: "user", Password: "pass"}); err != nil {
		t.Fatal(err)
	}
	if fileAuth(t, authPath, "reg.example.com") == "" {
		t.Error("Put should write to existing auth.json")
	}
	if fileAuth(t, dockerPath, "reg.example.com") != "" {
		t.Error("Put should not write Docker when auth.json exists")
	}
}
