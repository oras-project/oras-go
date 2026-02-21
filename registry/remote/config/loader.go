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

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

const (
	dockerConfigDirEnv  = "DOCKER_CONFIG"
	dockerConfigFileDir = ".docker"
	dockerConfigFile    = "config.json"

	// containersAuthFile is the name of the containers auth file.
	containersAuthFile = "auth.json"
	// containersConfigDir is the user-level containers config directory.
	containersConfigDir = "containers"
	// xdgRuntimeDirEnv is the XDG runtime directory environment variable.
	xdgRuntimeDirEnv = "XDG_RUNTIME_DIR"
)

// Configs holds loaded configuration from Docker config.json and
// system registries.conf. Fields are nil if the corresponding file
// was not found.
type Configs struct {
	// DockerConfig is the loaded Docker config.json, or nil if not found.
	DockerConfig *Config

	// ContainersAuthConfig is the loaded containers auth.json
	// (Podman/Buildah format), or nil if not found.
	// The auth.json format is identical to Docker config.json but uses
	// hierarchical namespace matching via GetAuthConfigHierarchical().
	ContainersAuthConfig *Config

	// RegistriesConfig is the loaded registries.conf, or nil if not found.
	RegistriesConfig *RegistriesConfig

	// PolicyConfig is the loaded containers-policy.json, or nil if not found.
	PolicyConfig *Policy

	// RegistriesDConfig is the loaded registries.d signature storage config,
	// or nil if no configuration was found.
	RegistriesDConfig *RegistriesDConfig

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

	// ContainersAuthPath overrides the containers auth.json path.
	// When empty, the default paths are searched:
	// $XDG_RUNTIME_DIR/containers/auth.json, then
	// $HOME/.config/containers/auth.json.
	ContainersAuthPath string

	// RegistriesConfigPath overrides the registries.conf path.
	// When empty, the system default locations are searched.
	RegistriesConfigPath string

	// PolicyConfigPath overrides the containers-policy.json path.
	// When empty, the default locations are searched
	// ($HOME/.config/containers/policy.json, then /etc/containers/policy.json).
	PolicyConfigPath string

	// RegistriesDPath overrides the registries.d directory path.
	// When empty, the system default locations are searched
	// (/etc/containers/registries.d and $HOME/.config/containers/registries.d).
	RegistriesDPath string

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

	// Load containers auth.json.
	if opts.ContainersAuthPath != "" {
		if _, err := os.Stat(opts.ContainersAuthPath); err == nil {
			cfg, err := Load(opts.ContainersAuthPath)
			if err != nil {
				return nil, err
			}
			result.ContainersAuthConfig = cfg
		}
	} else {
		authPath, err := defaultContainersAuthPath()
		if err == nil {
			if _, err := os.Stat(authPath); err == nil {
				cfg, err := Load(authPath)
				if err != nil {
					return nil, err
				}
				result.ContainersAuthConfig = cfg
			}
		}
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

	// Load policy config.
	policyPath := opts.PolicyConfigPath
	if policyPath != "" {
		if _, err := os.Stat(policyPath); err == nil {
			pol, err := LoadPolicy(policyPath)
			if err != nil {
				return nil, err
			}
			result.PolicyConfig = pol
		}
	} else {
		defaultPath, err := GetDefaultPolicyPath()
		if err == nil {
			if _, err := os.Stat(defaultPath); err == nil {
				pol, err := LoadPolicy(defaultPath)
				if err != nil {
					return nil, err
				}
				result.PolicyConfig = pol
			}
		}
	}

	// Load registries.d signature storage config.
	if opts.RegistriesDPath != "" {
		if _, err := os.Stat(opts.RegistriesDPath); err == nil {
			cfg, err := loadRegistriesDDir(nil, opts.RegistriesDPath)
			if err != nil {
				return nil, err
			}
			result.RegistriesDConfig = cfg
		}
	} else {
		cfg, err := LoadSystemRegistriesDConfig()
		if err != nil {
			return nil, err
		}
		// Only set if there's actual content.
		if cfg.DefaultDocker != nil || len(cfg.Docker) > 0 {
			result.RegistriesDConfig = cfg
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

// RegistryProperties creates a [properties.Registry] for the given reference
// string by combining settings from RegistriesConfig and CertsDir.
//
// It performs the following steps:
//  1. Creates base properties from RegistriesConfig (or plain reference parsing
//     if RegistriesConfig is nil).
//  2. Loads and applies TLS certificates from CertsDirPaths for the resolved
//     registry host.
func (c *Configs) RegistryProperties(ref string) (*properties.Registry, error) {
	props, err := NewRegistryProperties(ref, c.RegistriesConfig)
	if err != nil {
		return nil, err
	}

	if len(c.CertsDirPaths) > 0 {
		certs, err := LoadCertsDirFromPaths(props.Reference.Host(), c.CertsDirPaths)
		if err != nil {
			return nil, fmt.Errorf("failed to load certs for %s: %w", props.Reference.Host(), err)
		}
		if certs != nil {
			certs.ApplyToTransport(&props.Transport)
		}
	}

	return props, nil
}

// CredentialStore creates a [credentials.Store] combining Docker config and
// containers auth.json credentials. The Docker config store is used as the
// primary store, with the containers auth store as a fallback.
//
// Returns an error if neither DockerConfig nor ContainersAuthConfig is loaded.
func (c *Configs) CredentialStore(opts credentials.StoreOptions) (credentials.Store, error) {
	var stores []credentials.Store

	if c.DockerConfig != nil {
		stores = append(stores, credentials.NewStoreFromConfig(c.DockerConfig, opts))
	}
	if c.ContainersAuthConfig != nil {
		stores = append(stores, credentials.NewStoreFromConfig(c.ContainersAuthConfig, opts))
	}

	if len(stores) == 0 {
		return nil, fmt.Errorf("no credential configurations found")
	}
	if len(stores) == 1 {
		return stores[0], nil
	}
	return credentials.NewStoreWithFallbacks(stores[0], stores[1:]...), nil
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

// defaultContainersAuthPath returns the default containers auth.json path.
// It checks $XDG_RUNTIME_DIR/containers/auth.json first, then falls back
// to $HOME/.config/containers/auth.json.
func defaultContainersAuthPath() (string, error) {
	// Try XDG_RUNTIME_DIR first.
	if xdgRuntime := os.Getenv(xdgRuntimeDirEnv); xdgRuntime != "" {
		path := filepath.Join(xdgRuntime, containersConfigDir, containersAuthFile)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	// Fall back to $HOME/.config/containers/auth.json.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".config", containersConfigDir, containersAuthFile), nil
}
