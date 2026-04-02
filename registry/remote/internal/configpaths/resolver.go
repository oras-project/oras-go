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

// Package configpaths provides platform-aware configuration file path
// resolution for container tools configuration files.
package configpaths

// MergeStrategy controls how main configuration files are combined.
type MergeStrategy int

const (
	// MergeAll merges all found config files from all tiers.
	// This is the current containers/image behavior.
	MergeAll MergeStrategy = iota

	// FirstFoundWins uses the first main config file found (user → system
	// → vendor) but still merges all drop-in directories.
	// This is the UAPI specification behavior.
	FirstFoundWins
)

// Strategy specifies which path resolution approach to use.
type Strategy int

const (
	// ContainersImage uses the current containers/image two-tier path
	// resolution (system + user).
	ContainersImage Strategy = iota

	// UAPI uses the Podman 6 UAPI-based three-tier path resolution
	// (vendor + system + user) with rootful/rootless directories.
	// EXPERIMENTAL: behavior may change as the upstream specification evolves.
	UAPI
)

// PathResolver determines where to find configuration files.
type PathResolver interface {
	// MainConfigPaths returns the ordered paths for the main config file.
	// The name parameter is the config file base name without extension
	// (e.g., "registries" for registries.conf).
	MainConfigPaths(name string) []string

	// DropInDirs returns the ordered directories for drop-in config files.
	DropInDirs(name string) []string

	// MergeStrategy returns how main config files should be combined.
	MergeStrategy() MergeStrategy

	// AuthPrimaryPath returns the primary read/write path for auth.json.
	AuthPrimaryPath() (string, error)

	// AuthFallbackPaths returns fallback read-only paths for auth.json.
	AuthFallbackPaths() []string

	// CertsDirPaths returns base directories for certs.d certificate
	// discovery.
	CertsDirPaths() []string

	// RegistriesDDirs returns directories for registries.d signature
	// storage configuration.
	RegistriesDDirs() []string

	// PolicyPaths returns candidate paths for policy.json, in order of
	// preference (first found wins).
	PolicyPaths() []string

	// DockerConfigPath returns the Docker config.json path.
	DockerConfigPath() (string, error)
}

// NewResolver creates a PathResolver for the given strategy.
func NewResolver(s Strategy) PathResolver {
	switch s {
	case UAPI:
		return newUAPIResolver()
	default:
		return newCurrentResolver()
	}
}
