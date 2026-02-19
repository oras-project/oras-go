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
