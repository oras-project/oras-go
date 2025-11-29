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

	"github.com/oras-project/oras-go/v3/registry/remote/internal/ioutil"
)

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
func (cfg *Config) GetAuthConfig(serverAddress string) (AuthConfig, error) {
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
			return AuthConfig{}, nil
		}
	}
	var authCfg AuthConfig
	if err := json.Unmarshal(authCfgBytes, &authCfg); err != nil {
		return AuthConfig{}, fmt.Errorf("failed to unmarshal auth field: %w: %v", ErrInvalidConfigFormat, err)
	}
	return authCfg, nil
}

// PutAuthConfig puts authCfg for serverAddress.
func (cfg *Config) PutAuthConfig(serverAddress string, authCfg AuthConfig) error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	authCfgBytes, err := json.Marshal(authCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal auth field: %w", err)
	}
	cfg.authsCache[serverAddress] = authCfgBytes
	return cfg.saveFile()
}

// DeleteAuthConfig deletes the corresponding credential for serverAddress.
func (cfg *Config) DeleteAuthConfig(serverAddress string) error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	if _, ok := cfg.authsCache[serverAddress]; !ok {
		// no ops
		return nil
	}
	delete(cfg.authsCache, serverAddress)
	return cfg.saveFile()
}

// GetCredentialHelper returns the credential helpers for serverAddress.
func (cfg *Config) GetCredentialHelper(serverAddress string) string {
	return cfg.credentialHelpers[serverAddress]
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

// SetCredentialsStore puts the configured credentials store.
func (cfg *Config) SetCredentialsStore(credsStore string) error {
	cfg.rwLock.Lock()
	defer cfg.rwLock.Unlock()

	cfg.credentialsStore = credsStore
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
