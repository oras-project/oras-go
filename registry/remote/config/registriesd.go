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

	"gopkg.in/yaml.v3"
)

// Default system paths for registries.d configuration.
var (
	systemRegistriesDPath = "/etc/containers/registries.d"
)

// RegistriesDConfig represents a registries.d YAML configuration file.
// These files specify where signatures are stored for each registry.
// Reference: https://github.com/containers/image/blob/main/docs/containers-registries.d.5.md
type RegistriesDConfig struct {
	// DefaultDocker is the default configuration for all docker transport images.
	DefaultDocker *RegistriesDDockerConfig `yaml:"default-docker"`
	// Docker is a map of registry-specific configurations, keyed by namespace.
	Docker map[string]RegistriesDDockerConfig `yaml:"docker"`
}

// RegistriesDDockerConfig represents the docker-specific signature storage
// configuration for a registry namespace.
type RegistriesDDockerConfig struct {
	// Lookaside is the URL of the lookaside storage for reading signatures.
	Lookaside string `yaml:"lookaside"`
	// LookasideStaging is the URL of the lookaside storage for writing signatures.
	// If empty, Lookaside is used for both reading and writing.
	LookasideStaging string `yaml:"lookaside-staging"`
	// UseSigstoreAttachments indicates whether signatures should be stored
	// as OCI image attachments (sigstore format) instead of using lookaside storage.
	UseSigstoreAttachments bool `yaml:"use-sigstore-attachments"`

	// Legacy field names (sigstore was renamed to lookaside).
	// These are supported for backward compatibility.
	SigStore        string `yaml:"sigstore"`
	SigStoreStaging string `yaml:"sigstore-staging"`
}

// GetLookasideURLs returns the effective read and write lookaside URLs for the
// given image scope. It uses longest-prefix matching against the Docker
// namespace keys, falling back to the default-docker configuration.
//
// Returns empty strings if no configuration matches.
func (c *RegistriesDConfig) GetLookasideURLs(scope string) (readURL, writeURL string) {
	if c == nil {
		return "", ""
	}

	// Find the best matching Docker namespace entry.
	var bestMatch string
	var bestConfig RegistriesDDockerConfig

	for ns, cfg := range c.Docker {
		if !strings.HasPrefix(scope, ns) {
			continue
		}
		// Ensure prefix match is at a path boundary or exact match.
		if len(scope) > len(ns) && scope[len(ns)] != '/' {
			continue
		}
		if len(ns) > len(bestMatch) {
			bestMatch = ns
			bestConfig = cfg
		}
	}

	if bestMatch != "" {
		readURL = effectiveLookaside(bestConfig)
		writeURL = effectiveLookasideStaging(bestConfig)
		return readURL, writeURL
	}

	// Fall back to default-docker.
	if c.DefaultDocker != nil {
		readURL = effectiveLookaside(*c.DefaultDocker)
		writeURL = effectiveLookasideStaging(*c.DefaultDocker)
	}

	return readURL, writeURL
}

// effectiveLookaside returns the effective read URL, preferring the new
// "lookaside" field over the legacy "sigstore" field.
func effectiveLookaside(cfg RegistriesDDockerConfig) string {
	if cfg.Lookaside != "" {
		return cfg.Lookaside
	}
	return cfg.SigStore
}

// effectiveLookasideStaging returns the effective write URL. If no staging URL
// is configured, the read URL is used.
func effectiveLookasideStaging(cfg RegistriesDDockerConfig) string {
	if cfg.LookasideStaging != "" {
		return cfg.LookasideStaging
	}
	if cfg.SigStoreStaging != "" {
		return cfg.SigStoreStaging
	}
	// Fall back to the read URL.
	return effectiveLookaside(cfg)
}

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

	// 1. Load system configs
	config, err := loadRegistriesDDir(config, systemRegistriesDPath)
	if err != nil {
		return nil, err
	}

	// 2. Load user configs
	if homeDir, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(homeDir, ".config", "containers", "registries.d")
		config, err = loadRegistriesDDir(config, userPath)
		if err != nil {
			return nil, err
		}
	}

	if config == nil {
		// Return empty config rather than nil.
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
