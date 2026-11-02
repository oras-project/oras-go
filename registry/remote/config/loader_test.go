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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
		wantErr          bool
		wantN            int
		wantUser         map[string]string
		noUser           []string
		put              bool
	}{
		{
			name:     "primary and XDG config, same registry: primary wins",
			files:    [][3]string{{".config/containers", "reg.example.com", primaryB64}, {"xdgconfig/containers", "reg.example.com", xdgB64}},
			wantN:    2,
			wantUser: map[string]string{"reg.example.com": "primary"},
		},
		{
			name:  "primary and XDG config, different registries",
			files: [][3]string{{".config/containers", "primary.example.com", primaryB64}, {"xdgconfig/containers", "xdg.example.com", xdgB64}},
			wantN: 2,
			wantUser: map[string]string{
				"primary.example.com": "primary",
				"xdg.example.com":     "xdg",
			},
		},
		{
			name:     "primary missing, XDG config used",
			files:    [][3]string{{"xdgconfig/containers", "xdg.example.com", xdgB64}},
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
			name:    "invalid JSON in fallback is an error",
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
			opts := LoadConfigsOptions{}
			if tt.opts != nil {
				opts = tt.opts(tmp, override)
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
