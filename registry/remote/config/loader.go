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
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	dockerConfigDirEnv  = "DOCKER_CONFIG"
	dockerConfigFileDir = ".docker"
	dockerConfigFile    = "config.json"
)

// Configs holds loaded configuration from Docker config.json and
// system registries.conf. Fields are nil if the corresponding file
// was not found.
type Configs struct {
	// DockerConfig is the loaded Docker config.json, or nil if not found.
	DockerConfig *Config

	// RegistriesConfig is the loaded registries.conf, or nil if not found.
	RegistriesConfig *RegistriesConfig

	// CertsDirPaths is the resolved list of base directories for
	// containers-certs.d certificate discovery.
	CertsDirPaths []string
}

// LoadConfigsOptions configures LoadConfigs behavior.
type LoadConfigsOptions struct {
	// DockerConfigPath overrides the Docker config.json path.
	// When empty, the default path is used ($DOCKER_CONFIG/config.json
	// or $HOME/.docker/config.json).
	DockerConfigPath string

	// RegistriesConfigPath overrides the registries.conf path.
	// When empty, the system default locations are searched.
	RegistriesConfigPath string

	// CertsDirPaths overrides the containers-certs.d base directories.
	// When empty, the default paths are used (/etc/containers/certs.d
	// and $HOME/.config/containers/certs.d).
	CertsDirPaths []string
}

// LoadConfigs loads Docker config.json and system registries.conf from
// their default locations. Missing files are silently skipped.
// Returns an error only if a file exists but cannot be parsed.
func LoadConfigs() (*Configs, error) {
	return LoadConfigsWithOptions(LoadConfigsOptions{})
}

// LoadConfigsWithOptions loads configs from specified or default paths.
// Missing files are silently skipped.
// Returns an error only if a file exists but cannot be parsed.
func LoadConfigsWithOptions(opts LoadConfigsOptions) (*Configs, error) {
	result := &Configs{}

	// Load Docker config.
	dockerPath := opts.DockerConfigPath
	if dockerPath == "" {
		var err error
		dockerPath, err = defaultDockerConfigPath()
		if err != nil {
			return nil, fmt.Errorf("failed to determine Docker config path: %w", err)
		}
	}
	if _, err := os.Stat(dockerPath); err == nil {
		cfg, err := Load(dockerPath)
		if err != nil {
			return nil, err
		}
		result.DockerConfig = cfg
	}

	// Load registries.conf.
	if opts.RegistriesConfigPath != "" {
		if _, err := os.Stat(opts.RegistriesConfigPath); err == nil {
			cfg, err := LoadRegistriesConfig(opts.RegistriesConfigPath)
			if err != nil {
				return nil, err
			}
			result.RegistriesConfig = cfg
		}
	} else {
		cfg, err := LoadSystemRegistriesConfig()
		if err != nil {
			if !errors.Is(err, ErrRegistriesConfigNotFound) {
				return nil, err
			}
			// Not found — leave nil.
		} else {
			result.RegistriesConfig = cfg
		}
	}

	// Populate certs.d paths.
	if len(opts.CertsDirPaths) > 0 {
		result.CertsDirPaths = opts.CertsDirPaths
	} else {
		result.CertsDirPaths = defaultCertsDirPaths()
	}

	return result, nil
}

// defaultDockerConfigPath returns the default Docker config.json path.
// It checks $DOCKER_CONFIG first, then falls back to $HOME/.docker/config.json.
func defaultDockerConfigPath() (string, error) {
	configDir := os.Getenv(dockerConfigDirEnv)
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		configDir = filepath.Join(homeDir, dockerConfigFileDir)
	}
	return filepath.Join(configDir, dockerConfigFile), nil
}
