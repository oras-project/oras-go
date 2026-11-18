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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/internal/configpaths"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
	"gopkg.in/yaml.v3"
)

// RegistriesDConfig is the registries.d signature storage configuration
// returned by [LoadRegistriesDConfig]. See [signature.RegistriesDConfig].
type RegistriesDConfig = signature.RegistriesDConfig

// RegistriesDDockerConfig is the signature storage configuration for one
// registry namespace. See [signature.RegistriesDDockerConfig].
type RegistriesDDockerConfig = signature.RegistriesDDockerConfig

// Default system paths for registries.d configuration.
var (
	systemRegistriesDPath = "/etc/containers/registries.d"
)

// LoadRegistriesDConfig loads a single registries.d YAML configuration file.
func LoadRegistriesDConfig(path string) (*RegistriesDConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read registries.d config at %s: %w", path, err)
	}

	var config RegistriesDConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse registries.d config at %s: %w", path, err)
	}

	if config.Docker == nil {
		config.Docker = make(map[string]RegistriesDDockerConfig)
	}

	return &config, nil
}

// LoadSystemRegistriesDConfig loads and merges registries.d configuration from
// system and user default locations.
// Load order (each layer overrides the previous):
//  1. /etc/containers/registries.d/*.yaml (alpha-numerical order)
//  2. $HOME/.config/containers/registries.d/*.yaml (alpha-numerical order)
func LoadSystemRegistriesDConfig() (*RegistriesDConfig, error) {
	var config *RegistriesDConfig

	// 1. Load system configs.
	config, err := loadRegistriesDDir(config, systemRegistriesDPath)
	if err != nil {
		return nil, err
	}

	// 2. Load user configs.
	if homeDir, err := configpaths.UserHomeDir(); err == nil {
		userPath := filepath.Join(homeDir, ".config", "containers", "registries.d")
		config, err = loadRegistriesDDir(config, userPath)
		if err != nil {
			return nil, err
		}
	}

	if config == nil {
		return &RegistriesDConfig{
			Docker: make(map[string]RegistriesDDockerConfig),
		}, nil
	}

	return config, nil
}

// LoadSystemRegistriesDConfigWithStrategy loads and merges registries.d
// configuration using the specified path resolution strategy.
// When [StrategyUAPI] is used, the behavior is EXPERIMENTAL and may change.
func LoadSystemRegistriesDConfigWithStrategy(strategy Strategy) (*RegistriesDConfig, error) {
	resolver := configpaths.NewResolver(configpaths.Strategy(strategy))
	dirs := resolver.RegistriesDDirs()

	var config *RegistriesDConfig
	for _, dir := range dirs {
		var err error
		config, err = loadRegistriesDDir(config, dir)
		if err != nil {
			return nil, err
		}
	}

	if config == nil {
		return &RegistriesDConfig{
			Docker: make(map[string]RegistriesDDockerConfig),
		}, nil
	}

	return config, nil
}

// loadRegistriesDDir loads and merges all YAML files from the given directory.
func loadRegistriesDDir(config *RegistriesDConfig, dirPath string) (*RegistriesDConfig, error) {
	if _, err := os.Stat(dirPath); err != nil {
		return config, nil
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read registries.d directory %s: %w", dirPath, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		filePath := filepath.Join(dirPath, name)
		cfg, err := LoadRegistriesDConfig(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load registries.d config %s: %w", filePath, err)
		}

		if config == nil {
			config = cfg
		} else {
			config = mergeRegistriesDConfig(config, cfg)
		}
	}

	return config, nil
}

// mergeRegistriesDConfig merges the overlay config into the base config.
// Overlay entries override base entries with the same key.
func mergeRegistriesDConfig(base, overlay *RegistriesDConfig) *RegistriesDConfig {
	result := &RegistriesDConfig{
		DefaultDocker: base.DefaultDocker,
		Docker:        make(map[string]RegistriesDDockerConfig),
	}

	// Copy base docker entries.
	for k, v := range base.Docker {
		result.Docker[k] = v
	}

	// Override with overlay.
	if overlay.DefaultDocker != nil {
		result.DefaultDocker = overlay.DefaultDocker
	}
	for k, v := range overlay.Docker {
		result.Docker[k] = v
	}

	return result
}
