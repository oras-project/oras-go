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

package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// registryMirror represents a mirror configuration for a registry.
type registryMirror struct {
	Location string `toml:"location"`
	Insecure bool   `toml:"insecure,omitempty"`
}

// registryConfig represents a single registry configuration.
type registryConfig struct {
	Prefix   string           `toml:"prefix"`
	Location string           `toml:"location,omitempty"`
	Insecure bool             `toml:"insecure,omitempty"`
	Blocked  bool             `toml:"blocked,omitempty"`
	Mirror   []registryMirror `toml:"mirror,omitempty"`
}

// registriesConfFile represents the complete TOML structure of registries.conf.
type registriesConfFile struct {
	UnqualifiedSearchRegistries []string          `toml:"unqualified-search-registries,omitempty"`
	CredentialHelpers           []string          `toml:"credential-helpers,omitempty"`
	ShortNameMode               string            `toml:"short-name-mode,omitempty"`
	Registry                    []registryConfig  `toml:"registry,omitempty"`
	Aliases                     map[string]string `toml:"aliases,omitempty"`
}

// registriesConf represents a registries configuration that reads/writes
// the containers-registries.conf TOML format and implements the Config interface.
type registriesConf struct {
	// path is the file path to the registries.conf file.
	path string
	// rwLock is a read-write lock for thread-safe access.
	rwLock sync.RWMutex
	// config holds the TOML configuration structure.
	config registriesConfFile
	// credentials stores in-memory credentials (not persisted in TOML).
	credentials map[string]Credential
}

const (
	registriesConfUserDir    = ".config/containers"
	registriesConfFileName   = "registries.conf"
	registriesConfSystemPath = "/etc/containers/registries.conf"
)

// getDefaultRegistriesConfPath returns the path to the registries.conf file.
// It checks $HOME/.config/containers/registries.conf first, then falls back to
// /etc/containers/registries.conf.
func getDefaultRegistriesConfPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Try user-specific path first
	userPath := filepath.Join(homeDir, registriesConfUserDir, registriesConfFileName)
	if _, err := os.Stat(userPath); err == nil {
		return userPath, nil
	}

	// Fall back to system-wide path
	return registriesConfSystemPath, nil
}

// NewRegistriesConf creates a new registriesConf with the given path.
// It loads the existing TOML file if it exists.
func NewRegistriesConf(path string) (*registriesConf, error) {
	rc := &registriesConf{
		path:        path,
		credentials: make(map[string]Credential),
	}

	// Try to load existing file
	if err := rc.loadFile(); err != nil {
		return nil, fmt.Errorf("failed to load registries.conf from %s: %w", path, err)
	}

	return rc, nil
}

// loadFile loads the TOML configuration from the file.
func (rc *registriesConf) loadFile() error {
	data, err := os.ReadFile(rc.path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, start with empty configuration
			rc.config = registriesConfFile{
				Aliases: make(map[string]string),
			}
			return nil
		}
		return err
	}

	if err := toml.Unmarshal(data, &rc.config); err != nil {
		return fmt.Errorf("failed to parse TOML: %w", err)
	}

	if rc.config.Aliases == nil {
		rc.config.Aliases = make(map[string]string)
	}

	return nil
}

// saveFile saves the current configuration to the TOML file.
func (rc *registriesConf) saveFile() error {
	data, err := toml.Marshal(rc.config)
	if err != nil {
		return fmt.Errorf("failed to marshal TOML: %w", err)
	}

	return os.WriteFile(rc.path, data, 0644)
}

// GetCredential returns a Credential for the given server address.
// It supports hierarchical namespace matching as per containers-auth.json specification.
// For example, for "docker.io/terrylhowe/helm", it will try to match:
//  1. "docker.io/terrylhowe/helm" (exact match, including with any scheme prefix)
//  2. "docker.io/terrylhowe" (namespace match)
//  3. "docker.io" (registry match)
//
// Reference: https://github.com/containers/image/blob/main/docs/containers-auth.json.5.md
func (rc *registriesConf) GetCredential(serverAddress string) (Credential, error) {
	rc.rwLock.RLock()
	defer rc.rwLock.RUnlock()

	// Step 1: Try exact match first
	if cred, exists := rc.credentials[serverAddress]; exists {
		return cred, nil
	}

	// Step 2: Try hierarchical namespace matching (without scheme)
	// Only do hierarchical matching if serverAddress doesn't have a scheme.
	// If the user provides a scheme, they want exact scheme matching.
	for _, path := range getAuthPaths(serverAddress) {
		if cred, exists := rc.credentials[path]; exists {
			return cred, nil
		}
	}

	// No credentials found
	return EmptyCredential, nil
}

// PutCredential stores a credential for the given server address.
// Credentials are stored in memory and not persisted to the TOML file.
func (rc *registriesConf) PutCredential(serverAddress string, cred Credential) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	serverAddress = stripServerAddress(serverAddress)
	rc.credentials[serverAddress] = cred
	return nil
}

// DeleteCredential removes the credential for the given server address.
func (rc *registriesConf) DeleteCredential(serverAddress string) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	serverAddress = stripServerAddress(serverAddress)
	delete(rc.credentials, serverAddress)
	return nil
}

// GetCredentialHelper returns the credential helper for the given server address.
// In the registries.conf format, credential helpers are global, not per-server.
func (rc *registriesConf) GetCredentialHelper(serverAddress string) string {
	rc.rwLock.RLock()
	defer rc.rwLock.RUnlock()

	// registries.conf uses global credential helpers, return the first one
	if len(rc.config.CredentialHelpers) > 0 {
		return rc.config.CredentialHelpers[0]
	}
	return ""
}

// CredentialsStore returns the configured credentials store.
// In registries.conf, this is represented by credential-helpers.
func (rc *registriesConf) CredentialsStore() string {
	rc.rwLock.RLock()
	defer rc.rwLock.RUnlock()

	// Return the first credential helper as the "credentials store"
	if len(rc.config.CredentialHelpers) > 0 {
		return rc.config.CredentialHelpers[0]
	}
	return ""
}

// SetCredentialsStore sets the credentials store.
// In registries.conf, this sets the credential-helpers field.
func (rc *registriesConf) SetCredentialsStore(credsStore string) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	if credsStore == "" {
		rc.config.CredentialHelpers = nil
	} else {
		rc.config.CredentialHelpers = []string{credsStore}
	}

	return rc.saveFile()
}

// IsAuthConfigured returns whether authentication is configured.
func (rc *registriesConf) IsAuthConfigured() bool {
	rc.rwLock.RLock()
	defer rc.rwLock.RUnlock()

	return len(rc.config.CredentialHelpers) > 0 ||
		len(rc.credentials) > 0
}

// Path returns the path to the configuration.
func (rc *registriesConf) Path() string {
	return rc.path
}

// AddRegistry adds a new registry configuration to the TOML file.
func (rc *registriesConf) AddRegistry(prefix, location string, insecure, blocked bool) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	registry := registryConfig{
		Prefix:   prefix,
		Location: location,
		Insecure: insecure,
		Blocked:  blocked,
	}

	rc.config.Registry = append(rc.config.Registry, registry)
	return rc.saveFile()
}

// SetUnqualifiedSearchRegistries sets the list of unqualified search registries.
func (rc *registriesConf) SetUnqualifiedSearchRegistries(registries []string) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	rc.config.UnqualifiedSearchRegistries = registries
	return rc.saveFile()
}

// GetUnqualifiedSearchRegistries returns the list of unqualified search registries.
func (rc *registriesConf) GetUnqualifiedSearchRegistries() []string {
	rc.rwLock.RLock()
	defer rc.rwLock.RUnlock()

	return rc.config.UnqualifiedSearchRegistries
}

// AddAlias adds a short-name alias to the configuration.
func (rc *registriesConf) AddAlias(shortName, fullName string) error {
	rc.rwLock.Lock()
	defer rc.rwLock.Unlock()

	rc.config.Aliases[shortName] = fullName
	return rc.saveFile()
}

// stripServerAddress returns a serverAddress without scheme or trailing /
func stripServerAddress(serverAddress string) string {
	serverAddress = strings.TrimPrefix(serverAddress, "http://")
	serverAddress = strings.TrimPrefix(serverAddress, "https://")
	serverAddress = strings.TrimRight(serverAddress, "/")
	return serverAddress
}

// getAuthPaths returns a list of paths to check for credentials in hierarchical order.
// For example, for "docker.io/terrylhowe/helm", it returns:
//   - "docker.io/terrylhowe/helm"
//   - "docker.io/terrylhowe"
//   - "docker.io"
//
// This supports namespace-specific credentials as documented in containers-auth.json.
// Reference: https://github.com/containers/image/blob/main/docs/containers-auth.json.5.md
func getAuthPaths(serverAddress string) []string {
	addr := stripServerAddress(serverAddress)

	// Split by '/' to get all path components
	parts := strings.Split(addr, "/")
	if len(parts) == 0 {
		return []string{addr}
	}

	// Build paths from most specific to least specific
	paths := make([]string, 0, len(parts))
	for i := len(parts); i > 0; i-- {
		paths = append(paths, strings.Join(parts[:i], "/"))
	}
	return paths
}
