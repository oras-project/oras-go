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
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistriesDConfig(t *testing.T) {
	cfg, err := LoadRegistriesDConfig("./testdata/registriesd/default.yaml")
	if err != nil {
		t.Fatalf("LoadRegistriesDConfig() error: %v", err)
	}

	if cfg.DefaultDocker == nil {
		t.Fatal("DefaultDocker should not be nil")
	}
	if cfg.DefaultDocker.Lookaside != "https://sigstore.example.com" {
		t.Errorf("DefaultDocker.Lookaside = %v, want https://sigstore.example.com", cfg.DefaultDocker.Lookaside)
	}

	if len(cfg.Docker) != 2 {
		t.Fatalf("Docker entries = %d, want 2", len(cfg.Docker))
	}

	regCfg, ok := cfg.Docker["registry.example.com"]
	if !ok {
		t.Fatal("Docker[registry.example.com] not found")
	}
	if regCfg.Lookaside != "https://sigstore.example.com/registry" {
		t.Errorf("Docker[registry.example.com].Lookaside = %v, want https://sigstore.example.com/registry", regCfg.Lookaside)
	}

	nsCfg, ok := cfg.Docker["registry.example.com/namespace"]
	if !ok {
		t.Fatal("Docker[registry.example.com/namespace] not found")
	}
	if nsCfg.Lookaside != "https://sigstore.example.com/namespace" {
		t.Errorf("Lookaside = %v, want https://sigstore.example.com/namespace", nsCfg.Lookaside)
	}
	if nsCfg.LookasideStaging != "https://sigstore-staging.example.com/namespace" {
		t.Errorf("LookasideStaging = %v, want https://sigstore-staging.example.com/namespace", nsCfg.LookasideStaging)
	}
}

func TestLoadRegistriesDConfig_Legacy(t *testing.T) {
	cfg, err := LoadRegistriesDConfig("./testdata/registriesd/legacy.yaml")
	if err != nil {
		t.Fatalf("LoadRegistriesDConfig() error: %v", err)
	}

	if cfg.DefaultDocker == nil {
		t.Fatal("DefaultDocker should not be nil")
	}
	if cfg.DefaultDocker.SigStore != "https://legacy-sigstore.example.com" {
		t.Errorf("DefaultDocker.SigStore = %v, want https://legacy-sigstore.example.com", cfg.DefaultDocker.SigStore)
	}

	legCfg := cfg.Docker["legacy.example.com"]
	if legCfg.SigStore != "https://legacy-sigstore.example.com/legacy" {
		t.Errorf("SigStore = %v, want https://legacy-sigstore.example.com/legacy", legCfg.SigStore)
	}
	if legCfg.SigStoreStaging != "https://legacy-staging.example.com/legacy" {
		t.Errorf("SigStoreStaging = %v, want https://legacy-staging.example.com/legacy", legCfg.SigStoreStaging)
	}
}

func TestLoadRegistriesDConfig_SigstoreAttachments(t *testing.T) {
	cfg, err := LoadRegistriesDConfig("./testdata/registriesd/sigstore_attachments.yaml")
	if err != nil {
		t.Fatalf("LoadRegistriesDConfig() error: %v", err)
	}

	sigCfg := cfg.Docker["sigstore.example.com"]
	if !sigCfg.UseSigstoreAttachments {
		t.Error("UseSigstoreAttachments = false, want true")
	}
}

func TestLoadRegistriesDConfig_Invalid(t *testing.T) {
	_, err := LoadRegistriesDConfig("./testdata/registriesd/invalid.yaml")
	if err == nil {
		t.Fatal("LoadRegistriesDConfig() should return error for invalid YAML")
	}
}

func TestLoadRegistriesDConfig_NotFound(t *testing.T) {
	_, err := LoadRegistriesDConfig("./testdata/registriesd/nonexistent.yaml")
	if err == nil {
		t.Fatal("LoadRegistriesDConfig() should return error for missing file")
	}
}

func TestRegistriesDConfig_GetLookasideURLs(t *testing.T) {
	cfg, err := LoadRegistriesDConfig("./testdata/registriesd/default.yaml")
	if err != nil {
		t.Fatalf("LoadRegistriesDConfig() error: %v", err)
	}

	tests := []struct {
		name      string
		scope     string
		wantRead  string
		wantWrite string
	}{
		{
			name:      "Exact match on namespace",
			scope:     "registry.example.com/namespace",
			wantRead:  "https://sigstore.example.com/namespace",
			wantWrite: "https://sigstore-staging.example.com/namespace",
		},
		{
			name:      "Prefix match on namespace",
			scope:     "registry.example.com/namespace/repo",
			wantRead:  "https://sigstore.example.com/namespace",
			wantWrite: "https://sigstore-staging.example.com/namespace",
		},
		{
			name:      "Exact match on registry",
			scope:     "registry.example.com",
			wantRead:  "https://sigstore.example.com/registry",
			wantWrite: "https://sigstore.example.com/registry",
		},
		{
			name:      "Registry prefix for unknown namespace",
			scope:     "registry.example.com/other-ns",
			wantRead:  "https://sigstore.example.com/registry",
			wantWrite: "https://sigstore.example.com/registry",
		},
		{
			name:      "Fallback to default",
			scope:     "unknown.example.com/repo",
			wantRead:  "https://sigstore.example.com",
			wantWrite: "https://sigstore.example.com",
		},
		{
			name:      "No match partial hostname",
			scope:     "registry.example.com.evil.com/repo",
			wantRead:  "https://sigstore.example.com",
			wantWrite: "https://sigstore.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRead, gotWrite := cfg.GetLookasideURLs(tt.scope)
			if gotRead != tt.wantRead {
				t.Errorf("GetLookasideURLs(%s) readURL = %v, want %v", tt.scope, gotRead, tt.wantRead)
			}
			if gotWrite != tt.wantWrite {
				t.Errorf("GetLookasideURLs(%s) writeURL = %v, want %v", tt.scope, gotWrite, tt.wantWrite)
			}
		})
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Legacy(t *testing.T) {
	cfg, err := LoadRegistriesDConfig("./testdata/registriesd/legacy.yaml")
	if err != nil {
		t.Fatalf("LoadRegistriesDConfig() error: %v", err)
	}

	readURL, writeURL := cfg.GetLookasideURLs("legacy.example.com/repo")
	if readURL != "https://legacy-sigstore.example.com/legacy" {
		t.Errorf("readURL = %v, want https://legacy-sigstore.example.com/legacy", readURL)
	}
	if writeURL != "https://legacy-staging.example.com/legacy" {
		t.Errorf("writeURL = %v, want https://legacy-staging.example.com/legacy", writeURL)
	}

	// Unknown scope falls back to default.
	readURL, writeURL = cfg.GetLookasideURLs("unknown.example.com")
	if readURL != "https://legacy-sigstore.example.com" {
		t.Errorf("readURL = %v, want https://legacy-sigstore.example.com", readURL)
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Nil(t *testing.T) {
	var cfg *RegistriesDConfig
	readURL, writeURL := cfg.GetLookasideURLs("anything")
	if readURL != "" || writeURL != "" {
		t.Errorf("nil config GetLookasideURLs() = (%v, %v), want (\"\", \"\")", readURL, writeURL)
	}
}

func TestRegistriesDConfig_GetLookasideURLs_Empty(t *testing.T) {
	cfg := &RegistriesDConfig{
		Docker: make(map[string]RegistriesDDockerConfig),
	}
	readURL, writeURL := cfg.GetLookasideURLs("anything")
	if readURL != "" || writeURL != "" {
		t.Errorf("empty config GetLookasideURLs() = (%v, %v), want (\"\", \"\")", readURL, writeURL)
	}
}

func TestMergeRegistriesDConfig(t *testing.T) {
	base := &RegistriesDConfig{
		DefaultDocker: &RegistriesDDockerConfig{
			Lookaside: "https://base.example.com",
		},
		Docker: map[string]RegistriesDDockerConfig{
			"registry.example.com": {
				Lookaside: "https://base.example.com/registry",
			},
		},
	}
	overlay := &RegistriesDConfig{
		DefaultDocker: &RegistriesDDockerConfig{
			Lookaside: "https://overlay.example.com",
		},
		Docker: map[string]RegistriesDDockerConfig{
			"registry.example.com": {
				Lookaside: "https://overlay.example.com/registry",
			},
			"other.example.com": {
				Lookaside: "https://overlay.example.com/other",
			},
		},
	}

	merged := mergeRegistriesDConfig(base, overlay)

	if merged.DefaultDocker.Lookaside != "https://overlay.example.com" {
		t.Errorf("DefaultDocker.Lookaside = %v, want https://overlay.example.com", merged.DefaultDocker.Lookaside)
	}
	if len(merged.Docker) != 2 {
		t.Fatalf("Docker entries = %d, want 2", len(merged.Docker))
	}
	if merged.Docker["registry.example.com"].Lookaside != "https://overlay.example.com/registry" {
		t.Errorf("Docker[registry.example.com].Lookaside = %v, want overlay", merged.Docker["registry.example.com"].Lookaside)
	}
	if merged.Docker["other.example.com"].Lookaside != "https://overlay.example.com/other" {
		t.Errorf("Docker[other.example.com].Lookaside = %v, want overlay", merged.Docker["other.example.com"].Lookaside)
	}
}

func TestLoadSystemRegistriesDConfig_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create system registries.d directory with a YAML file.
	sysDir := filepath.Join(tmpDir, "system-registries.d")
	if err := os.MkdirAll(sysDir, 0755); err != nil {
		t.Fatal(err)
	}
	sysYAML := `
docker:
  sys.example.com:
    lookaside: https://sys-sigstore.example.com
`
	if err := os.WriteFile(filepath.Join(sysDir, "default.yaml"), []byte(sysYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Override system path.
	origPath := systemRegistriesDPath
	systemRegistriesDPath = sysDir
	defer func() { systemRegistriesDPath = origPath }()

	// Set HOME to tmp to avoid picking up user configs.
	t.Setenv("HOME", tmpDir)

	cfg, err := LoadSystemRegistriesDConfig()
	if err != nil {
		t.Fatalf("LoadSystemRegistriesDConfig() error: %v", err)
	}

	sysCfg, ok := cfg.Docker["sys.example.com"]
	if !ok {
		t.Fatal("Docker[sys.example.com] not found")
	}
	if sysCfg.Lookaside != "https://sys-sigstore.example.com" {
		t.Errorf("Lookaside = %v, want https://sys-sigstore.example.com", sysCfg.Lookaside)
	}
}

func TestLoadSystemRegistriesDConfig_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	// Override paths to nonexistent directories.
	origPath := systemRegistriesDPath
	systemRegistriesDPath = filepath.Join(tmpDir, "nonexistent")
	defer func() { systemRegistriesDPath = origPath }()

	t.Setenv("HOME", tmpDir)

	cfg, err := LoadSystemRegistriesDConfig()
	if err != nil {
		t.Fatalf("LoadSystemRegistriesDConfig() error: %v", err)
	}

	// Should return empty config, not nil.
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if cfg.DefaultDocker != nil {
		t.Error("DefaultDocker should be nil for empty config")
	}
	if len(cfg.Docker) != 0 {
		t.Errorf("Docker entries = %d, want 0", len(cfg.Docker))
	}
}

func TestLoadSystemRegistriesDConfigWithStrategy_ContainersImage(t *testing.T) {
	tmpDir := t.TempDir()

	// The resolver uses $HOME/.config/containers/registries.d for user-level.
	userRegDDir := filepath.Join(tmpDir, ".config", "containers", "registries.d")
	if err := os.MkdirAll(userRegDDir, 0755); err != nil {
		t.Fatal(err)
	}
	userYAML := `
docker:
  strategy-test.example.com:
    lookaside: https://strategy-sigstore.example.com
`
	if err := os.WriteFile(filepath.Join(userRegDDir, "default.yaml"), []byte(userYAML), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := systemRegistriesDPath
	systemRegistriesDPath = filepath.Join(tmpDir, "nonexistent")
	defer func() { systemRegistriesDPath = origPath }()

	t.Setenv("HOME", tmpDir)

	cfg, err := LoadSystemRegistriesDConfigWithStrategy(StrategyContainersImage)
	if err != nil {
		t.Fatalf("LoadSystemRegistriesDConfigWithStrategy() error: %v", err)
	}

	sysCfg, ok := cfg.Docker["strategy-test.example.com"]
	if !ok {
		t.Fatal("Docker[strategy-test.example.com] not found")
	}
	if sysCfg.Lookaside != "https://strategy-sigstore.example.com" {
		t.Errorf("Lookaside = %v, want https://strategy-sigstore.example.com", sysCfg.Lookaside)
	}
}

func TestLoadSystemRegistriesDConfigWithStrategy_UAPI(t *testing.T) {
	tmpDir := t.TempDir()

	origPath := systemRegistriesDPath
	systemRegistriesDPath = filepath.Join(tmpDir, "nonexistent")
	defer func() { systemRegistriesDPath = origPath }()

	t.Setenv("HOME", tmpDir)

	// With no files, should return empty config.
	cfg, err := LoadSystemRegistriesDConfigWithStrategy(StrategyUAPI)
	if err != nil {
		t.Fatalf("LoadSystemRegistriesDConfigWithStrategy(UAPI) error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if len(cfg.Docker) != 0 {
		t.Errorf("Docker entries = %d, want 0", len(cfg.Docker))
	}
}

func TestLoadSystemRegistriesDConfigWithStrategy_UAPI_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create user registries.d directory.
	userDir := filepath.Join(tmpDir, ".config", "containers", "registries.d")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	userYAML := `
docker:
  uapi-test.example.com:
    lookaside: https://uapi-sigstore.example.com
`
	if err := os.WriteFile(filepath.Join(userDir, "default.yaml"), []byte(userYAML), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := systemRegistriesDPath
	systemRegistriesDPath = filepath.Join(tmpDir, "nonexistent")
	defer func() { systemRegistriesDPath = origPath }()

	t.Setenv("HOME", tmpDir)

	cfg, err := LoadSystemRegistriesDConfigWithStrategy(StrategyUAPI)
	if err != nil {
		t.Fatalf("LoadSystemRegistriesDConfigWithStrategy(UAPI) error: %v", err)
	}

	if cfg == nil {
		t.Fatal("config should not be nil")
	}
}

func TestLoadRegistriesDDir_SkipsNonYamlFiles(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "registries.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	yamlContent := `
docker:
  skip-test.example.com:
    lookaside: https://skip-sigstore.example.com
`
	// Write a valid YAML file and some non-YAML files.
	if err := os.WriteFile(filepath.Join(dir, "valid.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	// Also create a subdirectory that should be skipped.
	if err := os.MkdirAll(filepath.Join(dir, "subdir.yaml"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadRegistriesDDir(nil, dir)
	if err != nil {
		t.Fatalf("loadRegistriesDDir() error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if _, ok := cfg.Docker["skip-test.example.com"]; !ok {
		t.Error("Docker[skip-test.example.com] not found")
	}
}

func TestLoadRegistriesDDir_SupportsYmlExtension(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "registries.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	ymlContent := `
docker:
  yml-test.example.com:
    lookaside: https://yml-sigstore.example.com
`
	if err := os.WriteFile(filepath.Join(dir, "test.yml"), []byte(ymlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadRegistriesDDir(nil, dir)
	if err != nil {
		t.Fatalf("loadRegistriesDDir() error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if _, ok := cfg.Docker["yml-test.example.com"]; !ok {
		t.Error("Docker[yml-test.example.com] not found")
	}
}

func TestLoadConfigs_RegistriesDConfig_ExplicitPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a registries.d directory.
	regdDir := filepath.Join(tmpDir, "registries.d")
	if err := os.MkdirAll(regdDir, 0755); err != nil {
		t.Fatal(err)
	}
	yamlContent := `
docker:
  test.example.com:
    lookaside: https://test-sigstore.example.com
`
	if err := os.WriteFile(filepath.Join(regdDir, "test.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfigsWithOptions(LoadConfigsOptions{
		DockerConfigPath: filepath.Join(tmpDir, "nonexistent.json"),
		RegistriesDPath:  regdDir,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions() error: %v", err)
	}
	if configs.RegistriesDConfig == nil {
		t.Fatal("RegistriesDConfig should not be nil")
	}

	readURL, _ := configs.RegistriesDConfig.GetLookasideURLs("test.example.com/repo")
	if readURL != "https://test-sigstore.example.com" {
		t.Errorf("readURL = %v, want https://test-sigstore.example.com", readURL)
	}
}
