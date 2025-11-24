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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// RegistriesConfig represents a registries.conf configuration file.
// Reference: https://github.com/containers/image/blob/main/docs/containers-registries.conf.5.md
type RegistriesConfig struct {
	// UnqualifiedSearchRegistries is the list of registries to try when pulling unqualified images.
	UnqualifiedSearchRegistries []string `toml:"unqualified-search-registries"`
	// ShortNameMode controls short-name lookup behavior: "enforcing", "permissive", or "disabled".
	ShortNameMode string `toml:"short-name-mode"`
	// Registries is a list of registry configurations.
	Registries []Registry `toml:"registry"`
	// Aliases maps short names to fully qualified references.
	Aliases map[string]string `toml:"aliases"`
}

// Registry represents configuration for a specific registry namespace.
type Registry struct {
	// Prefix identifies which images match this configuration (e.g., "docker.io", "*.example.com").
	Prefix string `toml:"prefix"`
	// Location is the actual registry location (defaults to Prefix if empty).
	Location string `toml:"location"`
	// Insecure allows HTTP or unverified HTTPS.
	Insecure bool `toml:"insecure"`
	// Blocked prevents pulling from this registry.
	Blocked bool `toml:"blocked"`
	// Mirrors is a list of mirror configurations.
	Mirrors []Mirror `toml:"mirror"`
	// MirrorByDigestOnly restricts mirrors to digest-based pulls only.
	MirrorByDigestOnly bool `toml:"mirror-by-digest-only"`
}

// Mirror represents a registry mirror configuration.
type Mirror struct {
	// Location is the mirror's address.
	Location string `toml:"location"`
	// Insecure allows HTTP or unverified HTTPS for this mirror.
	Insecure bool `toml:"insecure"`
	// PullFromMirror controls when to use this mirror: "all", "digest-only", or "tag-only".
	PullFromMirror string `toml:"pull-from-mirror"`
}

// ErrRegistriesConfigNotFound is returned when no registries.conf file is found.
var ErrRegistriesConfigNotFound = fmt.Errorf("registries.conf not found")

// Default system paths for registries.conf.
var (
	systemRegistriesConfPath    = "/etc/containers/registries.conf"
	systemRegistriesConfDirPath = "/etc/containers/registries.conf.d"
)

// LoadRegistriesConfig loads a registries.conf file from the given path.
func LoadRegistriesConfig(path string) (*RegistriesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read registries config at %s: %w", path, err)
	}

	var config RegistriesConfig
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse registries config at %s: %w", path, err)
	}

	// Initialize maps if nil
	if config.Aliases == nil {
		config.Aliases = make(map[string]string)
	}

	return &config, nil
}

// LoadSystemRegistriesConfig loads registries.conf from system default locations.
// Checks: user config, then /etc/containers/registries.conf, then /etc/containers/registries.conf.d/*.conf
func LoadSystemRegistriesConfig() (*RegistriesConfig, error) {
	var config *RegistriesConfig

	// Check user config first
	if homeDir, err := os.UserHomeDir(); err == nil {
		userConfigPath := filepath.Join(homeDir, ".config", "containers", "registries.conf")
		if cfg, err := LoadRegistriesConfig(userConfigPath); err == nil {
			config = cfg
		}
	}

	// Load system config if no user config
	if config == nil {
		if _, err := os.Stat(systemRegistriesConfPath); err == nil {
			cfg, err := LoadRegistriesConfig(systemRegistriesConfPath)
			if err != nil {
				return nil, err
			}
			config = cfg
		}
	}

	// Load and merge drop-in configs from .conf.d directory
	if _, err := os.Stat(systemRegistriesConfDirPath); err == nil {
		entries, err := os.ReadDir(systemRegistriesConfDirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read registries.conf.d directory: %w", err)
		}

		// Sort entries for deterministic ordering
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
				continue
			}

			confPath := filepath.Join(systemRegistriesConfDirPath, entry.Name())
			dropInConfig, err := LoadRegistriesConfig(confPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load drop-in config %s: %w", confPath, err)
			}

			if config == nil {
				config = dropInConfig
			} else {
				config = mergeRegistriesConfig(config, dropInConfig)
			}
		}
	}

	if config == nil {
		return nil, ErrRegistriesConfigNotFound
	}

	return config, nil
}

// mergeRegistriesConfig merges the drop-in config into the base config.
// Drop-in configs override or extend the base configuration.
func mergeRegistriesConfig(base, dropIn *RegistriesConfig) *RegistriesConfig {
	result := &RegistriesConfig{
		UnqualifiedSearchRegistries: base.UnqualifiedSearchRegistries,
		ShortNameMode:               base.ShortNameMode,
		Registries:                  make([]Registry, len(base.Registries)),
		Aliases:                     make(map[string]string),
	}

	// Copy base registries
	copy(result.Registries, base.Registries)

	// Copy base aliases
	for k, v := range base.Aliases {
		result.Aliases[k] = v
	}

	// Override with drop-in values
	if len(dropIn.UnqualifiedSearchRegistries) > 0 {
		result.UnqualifiedSearchRegistries = dropIn.UnqualifiedSearchRegistries
	}
	if dropIn.ShortNameMode != "" {
		result.ShortNameMode = dropIn.ShortNameMode
	}

	// Merge registries (drop-in registries with same prefix override base)
	for _, dropInReg := range dropIn.Registries {
		found := false
		for i, baseReg := range result.Registries {
			if baseReg.Prefix == dropInReg.Prefix {
				result.Registries[i] = dropInReg
				found = true
				break
			}
		}
		if !found {
			result.Registries = append(result.Registries, dropInReg)
		}
	}

	// Merge aliases (drop-in aliases override base)
	for k, v := range dropIn.Aliases {
		result.Aliases[k] = v
	}

	return result
}

// FindRegistry finds the best matching registry configuration for the given image reference.
// It matches by longest prefix first and supports wildcard prefixes like "*.example.com".
func (rc *RegistriesConfig) FindRegistry(ref string) *Registry {
	if rc == nil {
		return nil
	}

	var bestMatch *Registry
	bestMatchLen := -1

	for i := range rc.Registries {
		reg := &rc.Registries[i]
		prefix := reg.Prefix
		if prefix == "" {
			continue
		}

		if matchesPrefix(ref, prefix) {
			matchLen := len(prefix)
			// For wildcard prefixes, use the non-wildcard part length
			if strings.HasPrefix(prefix, "*.") {
				matchLen = len(prefix) - 1 // Account for the wildcard
			}

			if matchLen > bestMatchLen {
				bestMatch = reg
				bestMatchLen = matchLen
			}
		}
	}

	return bestMatch
}

// matchesPrefix checks if the reference matches the given prefix.
// Supports wildcard prefixes like "*.example.com".
func matchesPrefix(ref, prefix string) bool {
	// Handle wildcard prefix
	if strings.HasPrefix(prefix, "*.") {
		suffix := prefix[1:] // Remove the "*", keep the "."
		// Check if ref ends with the suffix or if the ref's host part matches
		refHost := extractHost(ref)
		return strings.HasSuffix(refHost, suffix)
	}

	// Exact prefix match or ref starts with prefix followed by "/" or ":"
	if ref == prefix {
		return true
	}
	if strings.HasPrefix(ref, prefix+"/") {
		return true
	}
	if strings.HasPrefix(ref, prefix+":") {
		return true
	}
	// Check if prefix matches the registry host
	if strings.HasPrefix(ref, prefix) && (len(ref) == len(prefix) || ref[len(prefix)] == '/' || ref[len(prefix)] == ':') {
		return true
	}

	return false
}

// extractHost extracts the host part from a reference.
func extractHost(ref string) string {
	// Remove any tag or digest suffix first
	if idx := strings.Index(ref, "@"); idx != -1 {
		ref = ref[:idx]
	}
	if idx := strings.Index(ref, ":"); idx != -1 {
		// Check if this is a port number or tag
		rest := ref[idx+1:]
		if strings.Contains(rest, "/") {
			// This is a port number, keep looking
		} else if !strings.Contains(ref[:idx], "/") {
			// No slash before colon and no slash after, this is host:port or host:tag
			// If there's no slash at all, treat the whole thing as the host
			return ref
		}
	}

	// Get the first path component
	if idx := strings.Index(ref, "/"); idx != -1 {
		return ref[:idx]
	}

	return ref
}

// ResolveAlias resolves a short name to a fully qualified reference.
func (rc *RegistriesConfig) ResolveAlias(shortName string) (string, bool) {
	if rc == nil || rc.Aliases == nil {
		return "", false
	}

	resolved, ok := rc.Aliases[shortName]
	return resolved, ok
}

// IsBlocked returns true if the given reference is blocked.
func (rc *RegistriesConfig) IsBlocked(ref string) bool {
	reg := rc.FindRegistry(ref)
	if reg == nil {
		return false
	}
	return reg.Blocked
}

// GetMirrors returns the mirrors for the given reference, in order of preference.
func (rc *RegistriesConfig) GetMirrors(ref string) []Mirror {
	reg := rc.FindRegistry(ref)
	if reg == nil {
		return nil
	}
	return reg.Mirrors
}

// RewriteReference rewrites a reference to its actual location.
// If the registry has a Location specified, the prefix is replaced with it.
func (rc *RegistriesConfig) RewriteReference(ref string) string {
	reg := rc.FindRegistry(ref)
	if reg == nil {
		return ref
	}

	// If no location specified, return the original reference
	if reg.Location == "" {
		return ref
	}

	prefix := reg.Prefix
	location := reg.Location

	// Handle wildcard prefixes
	if strings.HasPrefix(prefix, "*.") {
		// For wildcard prefixes, we don't rewrite
		return ref
	}

	// Replace prefix with location
	if strings.HasPrefix(ref, prefix) {
		return location + ref[len(prefix):]
	}

	return ref
}

// GetLocation returns the effective location for a registry.
// If Location is empty, returns the Prefix.
func (r *Registry) GetLocation() string {
	if r.Location != "" {
		return r.Location
	}
	return r.Prefix
}
