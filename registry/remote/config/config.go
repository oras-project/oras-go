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

package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
========
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
	"github.com/oras-project/oras-go/v3/registry/remote/internal/ioutil"
)

func init() {
	// Register config loader with credentials package
	credentials.SetDefaultConfigLoader(func(configPath string) (credentials.ConfigFile, error) {
		return Load(configPath)
	})
}

// Type aliases for backward compatibility.
// These types have been moved to the credentials package.
// Deprecated: Use credentials.AuthConfig instead.
type AuthConfig = credentials.AuthConfig

// ErrInvalidAuthConfig is returned when the auth config format is invalid.
// Deprecated: Use credentials.ErrInvalidAuthConfig instead.
var ErrInvalidAuthConfig = credentials.ErrInvalidAuthConfig

// NewAuthConfig creates an AuthConfig based on credential components.
// Deprecated: Use credentials.NewAuthConfig instead.
var NewAuthConfig = credentials.NewAuthConfig

// EncodeAuth base64-encodes username and password into base64(username:password).
// Deprecated: Use credentials.EncodeAuth instead.
var EncodeAuth = credentials.EncodeAuth

const (
	// configFieldAuths is the "auths" field in the config file.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L19
	configFieldAuths = "auths"
	// configFieldCredentialsStore is the "credsStore" field in the config file.
	configFieldCredentialsStore = "credsStore"
	// configFieldCredentialHelpers is the "credHelpers" field in the config file.
	configFieldCredentialHelpers = "credHelpers"
)

// ErrInvalidConfigFormat is returned when the config format is invalid.
var ErrInvalidConfigFormat = errors.New("invalid config format")

<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
========
// ErrNoConfigPath is returned when Save is called on a Config with no path.
var ErrNoConfigPath = errors.New("no config path configured")

>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
// Config represents a docker configuration file.
// References:
//   - https://docs.docker.com/engine/reference/commandline/cli/#docker-cli-configuration-file-configjson-properties
//   - https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L17-L44
type Config struct {
	// path is the path to the config file.
	path string
	// rwLock is a read-write-lock for the file store.
	rwLock sync.RWMutex
	// content is the content of the config file.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L17-L44
	content map[string]json.RawMessage
	// authsCache is a cache of the auths field of the config.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L19
	authsCache map[string]json.RawMessage
	// credentialsStore is the credsStore field of the config.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L28
	credentialsStore string
	// credentialHelpers is the credHelpers field of the config.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L29
	credentialHelpers map[string]string
}

// NewConfig creates an in-memory Config with no file backing.
// Use this when you want to configure credentials programmatically
// without reading from or writing to a file.
func NewConfig() *Config {
	return &Config{
		content:           make(map[string]json.RawMessage),
		authsCache:        make(map[string]json.RawMessage),
		credentialHelpers: make(map[string]string),
	}
}

// NewConfigWithPath creates an in-memory Config with a file path configured.
// The file is not read; use Load() to read from an existing file.
// The path is used when Save() is called.
func NewConfigWithPath(configPath string) *Config {
	return &Config{
		path:              configPath,
		content:           make(map[string]json.RawMessage),
		authsCache:        make(map[string]json.RawMessage),
		credentialHelpers: make(map[string]string),
	}
}

// Load loads Config from the given config path.
func Load(configPath string) (*Config, error) {
	cfg := &Config{path: configPath}
	configFile, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// init content and caches if the content file does not exist
			cfg.content = make(map[string]json.RawMessage)
			cfg.authsCache = make(map[string]json.RawMessage)
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to open config file at %s: %w", configPath, err)
	}
	defer configFile.Close()

	// decode config content if the config file exists
	if err := json.NewDecoder(configFile).Decode(&cfg.content); err != nil {
		if errors.Is(err, io.EOF) {
			// empty or whitespace only file
			cfg.content = make(map[string]json.RawMessage)
			cfg.authsCache = make(map[string]json.RawMessage)
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to decode config file %s: %w: %v", configPath, ErrInvalidConfigFormat, err)
	}

	if credsStoreBytes, ok := cfg.content[configFieldCredentialsStore]; ok {
		if err := json.Unmarshal(credsStoreBytes, &cfg.credentialsStore); err != nil {
			return nil, fmt.Errorf("failed to unmarshal creds store field: %w: %v", ErrInvalidConfigFormat, err)
		}
	}

	if credHelpersBytes, ok := cfg.content[configFieldCredentialHelpers]; ok {
		if err := json.Unmarshal(credHelpersBytes, &cfg.credentialHelpers); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cred helpers field: %w: %v", ErrInvalidConfigFormat, err)
		}
	}

	if authsBytes, ok := cfg.content[configFieldAuths]; ok {
		if err := json.Unmarshal(authsBytes, &cfg.authsCache); err != nil {
			return nil, fmt.Errorf("failed to unmarshal auths field: %w: %v", ErrInvalidConfigFormat, err)
		}
	}
	if cfg.authsCache == nil {
		cfg.authsCache = make(map[string]json.RawMessage)
	}

	return cfg, nil
}

// GetAuthConfig returns an AuthConfig for serverAddress.
<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
func (cfg *Config) GetAuthConfig(serverAddress string) (AuthConfig, error) {
========
func (cfg *Config) GetAuthConfig(serverAddress string) (credentials.AuthConfig, error) {
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
	cfg.rwLock.RLock()
	defer cfg.rwLock.RUnlock()

	authCfgBytes, ok := cfg.authsCache[serverAddress]
	if !ok {
		// NOTE: the auth key for the server address may have been stored with
		// a http/https prefix in legacy config files, e.g. "registry.example.com"
		// can be stored as "https://registry.example.com/".
		var matched bool
		for addr, auth := range cfg.authsCache {
			if ToHostname(addr) == serverAddress {
				matched = true
				authCfgBytes = auth
				break
			}
		}
		if !matched {
<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
			return AuthConfig{}, nil
========
			return credentials.AuthConfig{}, nil
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
		}
	}
	var authCfg credentials.AuthConfig
	if err := json.Unmarshal(authCfgBytes, &authCfg); err != nil {
<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
		return AuthConfig{}, fmt.Errorf("failed to unmarshal auth field: %w: %v", ErrInvalidConfigFormat, err)
========
		return credentials.AuthConfig{}, fmt.Errorf("failed to unmarshal auth field: %w: %v", ErrInvalidConfigFormat, err)
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
	}
	return authCfg, nil
}

<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
// PutAuthConfig puts authCfg for serverAddress.
func (cfg *Config) PutAuthConfig(serverAddress string, authCfg AuthConfig) error {
========
// SetAuthConfig sets authCfg for serverAddress in memory without saving to file.
// Use Save() to persist changes, or PutAuthConfig() to set and save atomically.
func (cfg *Config) SetAuthConfig(serverAddress string, authCfg credentials.AuthConfig) error {
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	authCfgBytes, err := json.Marshal(authCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal auth field: %w", err)
	}
	cfg.authsCache[serverAddress] = authCfgBytes
	return nil
}

<<<<<<<< HEAD:registry/remote/internal/configuration/config.go
// DeleteAuthConfig deletes the corresponding credential for serverAddress.
========
// PutAuthConfig puts authCfg for serverAddress and saves to file if a path is configured.
func (cfg *Config) PutAuthConfig(serverAddress string, authCfg credentials.AuthConfig) error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	authCfgBytes, err := json.Marshal(authCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal auth field: %w", err)
	}
	cfg.authsCache[serverAddress] = authCfgBytes
	if cfg.path != "" {
		return cfg.saveFile()
	}
	return nil
}

// RemoveAuthConfig removes the credential for serverAddress from memory without saving.
// Use Save() to persist changes, or DeleteAuthConfig() to remove and save atomically.
func (cfg *Config) RemoveAuthConfig(serverAddress string) {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	delete(cfg.authsCache, serverAddress)
}

// DeleteAuthConfig deletes the corresponding credential for serverAddress and saves to file.
>>>>>>>> 21e90fe (feat: add containers-certs.d support and config-to-properties bridge):registry/remote/config/config.go
func (cfg *Config) DeleteAuthConfig(serverAddress string) error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	if _, ok := cfg.authsCache[serverAddress]; !ok {
		// no ops
		return nil
	}
	delete(cfg.authsCache, serverAddress)
	if cfg.path != "" {
		return cfg.saveFile()
	}
	return nil
}

// GetCredentialHelper returns the credential helper for serverAddress.
func (cfg *Config) GetCredentialHelper(serverAddress string) string {
	cfg.rwLock.RLock()
	defer cfg.rwLock.RUnlock()

	return cfg.credentialHelpers[serverAddress]
}

// SetCredentialHelper sets the credential helper for serverAddress in memory.
func (cfg *Config) SetCredentialHelper(serverAddress, helper string) {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	if cfg.credentialHelpers == nil {
		cfg.credentialHelpers = make(map[string]string)
	}
	cfg.credentialHelpers[serverAddress] = helper
}

// CredentialHelpers returns a copy of all configured credential helpers.
func (cfg *Config) CredentialHelpers() map[string]string {
	cfg.rwLock.RLock()
	defer cfg.rwLock.RUnlock()

	result := make(map[string]string, len(cfg.credentialHelpers))
	for k, v := range cfg.credentialHelpers {
		result[k] = v
	}
	return result
}

// CredentialsStore returns the configured credentials store.
func (cfg *Config) CredentialsStore() string {
	cfg.rwLock.RLock()
	defer cfg.rwLock.RUnlock()

	return cfg.credentialsStore
}

// Path returns the path to the config file.
func (cfg *Config) Path() string {
	return cfg.path
}

// SetPath sets the file path for this config.
// This is used by Save() to determine where to write the config.
func (cfg *Config) SetPath(path string) {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	cfg.path = path
}

// SetCredentialsStore sets the credentials store in memory without saving.
func (cfg *Config) SetCredentialsStore(credsStore string) {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	cfg.credentialsStore = credsStore
}

// Save saves the config to the configured file path.
// Returns ErrNoConfigPath if no path is configured.
func (cfg *Config) Save() error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	if cfg.path == "" {
		return ErrNoConfigPath
	}
	return cfg.saveFile()
}

// IsAuthConfigured returns whether there is authentication configured in this
// config file or not.
func (cfg *Config) IsAuthConfigured() bool {
	return cfg.credentialsStore != "" ||
		len(cfg.credentialHelpers) > 0 ||
		len(cfg.authsCache) > 0
}

// saveFile saves Config into the file.
func (cfg *Config) saveFile() (returnErr error) {
	// marshal content
	// credentialHelpers is skipped as it's never set
	if cfg.credentialsStore != "" {
		credsStoreBytes, err := json.Marshal(cfg.credentialsStore)
		if err != nil {
			return fmt.Errorf("failed to marshal creds store: %w", err)
		}
		cfg.content[configFieldCredentialsStore] = credsStoreBytes
	} else {
		// omit empty
		delete(cfg.content, configFieldCredentialsStore)
	}
	authsBytes, err := json.Marshal(cfg.authsCache)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	cfg.content[configFieldAuths] = authsBytes
	jsonBytes, err := json.MarshalIndent(cfg.content, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// write the content to a ingest file for atomicity
	configDir := filepath.Dir(cfg.path)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to make directory %s: %w", configDir, err)
	}
	ingest, err := ioutil.Ingest(configDir, bytes.NewReader(jsonBytes))
	if err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}
	defer func() {
		if returnErr != nil {
			// clean up the ingest file in case of error
			os.Remove(ingest)
		}
	}()

	// overwrite the config file
	if err := os.Rename(ingest, cfg.path); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}
	return nil
}

// ToHostname normalizes a server address to just its hostname, removing
// the scheme and the path parts.
// It is used to match keys in the auths map, which may be either stored as
// hostname or as hostname including scheme (in legacy docker config files).
// Reference: https://github.com/docker/cli/blob/v24.0.6/cli/config/credentials/file_store.go#L71
func ToHostname(addr string) string {
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr, _, _ = strings.Cut(addr, "/")
	return addr
}
