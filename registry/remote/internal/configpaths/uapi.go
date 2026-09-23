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

package configpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	xdgConfigHomeEnv = "XDG_CONFIG_HOME"
)

// uapiResolver implements PathResolver using the Podman 6 UAPI-based
// path resolution strategy.
//
// EXPERIMENTAL: This resolver is based on the Podman 6 design proposal
// for unified config file parsing. The behavior may change as the
// upstream specification evolves.
//
// Reference: https://uapi-group.org/specifications/specs/configuration_files_specification/
type uapiResolver struct {
	// vendorConfDir is the vendor/distro default directory
	// (e.g., "/usr/share"). Empty means no vendor tier.
	vendorConfDir string
	// systemConfDir is the system admin directory (e.g., "/etc").
	// Empty means no system tier.
	systemConfDir string
	// userConfDir returns the user config directory.
	// This is a function to defer environment variable lookups.
	userConfDir func() string
	// supportsRootfulRootless indicates whether rootful/rootless
	// directory splits are supported (false on Windows).
	supportsRootfulRootless bool
}

// userDir returns the user-level containers config directory.
func (r *uapiResolver) userDir() string {
	if r.userConfDir != nil {
		return r.userConfDir()
	}
	return ""
}

func (r *uapiResolver) MainConfigPaths(name string) []string {
	// UAPI order: user → system → vendor (first found wins).
	var paths []string
	if dir := r.userDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, name+".conf"))
	}
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, name+".conf"))
	}
	if r.vendorConfDir != "" {
		paths = append(paths, filepath.Join(r.vendorConfDir, containersConfigDir, name+".conf"))
	}
	return paths
}

func (r *uapiResolver) DropInDirs(name string) []string {
	// Drop-ins are always merged from all tiers: vendor → system → user.
	// Within each tier, rootful/rootless variants are included.
	var dirs []string

	if r.vendorConfDir != "" {
		base := filepath.Join(r.vendorConfDir, containersConfigDir)
		dirs = append(dirs, filepath.Join(base, name+".conf.d"))
		dirs = r.appendRootDirs(dirs, base, name)
	}

	if r.systemConfDir != "" {
		base := filepath.Join(r.systemConfDir, containersConfigDir)
		dirs = append(dirs, filepath.Join(base, name+".conf.d"))
		dirs = r.appendRootDirs(dirs, base, name)
	}

	if dir := r.userDir(); dir != "" {
		dirs = append(dirs, filepath.Join(dir, name+".conf.d"))
	}

	return dirs
}

// appendRootDirs appends rootful or rootless drop-in directories based
// on the current UID.
func (r *uapiResolver) appendRootDirs(dirs []string, base, name string) []string {
	if !r.supportsRootfulRootless {
		return dirs
	}

	uid := os.Getuid()
	if uid == 0 {
		dirs = append(dirs, filepath.Join(base, name+".rootful.conf.d"))
	} else {
		dirs = append(dirs, filepath.Join(base, name+".rootless.conf.d"))
		dirs = append(dirs, filepath.Join(base, name+".rootless.conf.d", strconv.Itoa(uid)))
	}
	return dirs
}

func (r *uapiResolver) MergeStrategy() MergeStrategy {
	return FirstFoundWins
}

func (r *uapiResolver) AuthPrimaryPath() (string, error) {
	if dir := r.userDir(); dir != "" {
		return filepath.Join(dir, containersAuthFile), nil
	}
	return "", fmt.Errorf("failed to determine user config directory for auth.json")
}

func (r *uapiResolver) AuthFallbackPaths() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	// Docker fallbacks
	paths = append(paths,
		filepath.Join(home, dockerConfigFileDir, dockerConfigFileName),
		filepath.Join(home, ".dockercfg"),
	)
	return paths
}

func (r *uapiResolver) CertsDirPaths() []string {
	var paths []string
	if r.vendorConfDir != "" {
		paths = append(paths, filepath.Join(r.vendorConfDir, containersConfigDir, "certs.d"))
	}
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, "certs.d"))
	}
	if dir := r.userDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, "certs.d"))
	}
	return paths
}

func (r *uapiResolver) RegistriesDDirs() []string {
	var dirs []string
	if r.vendorConfDir != "" {
		dirs = append(dirs, filepath.Join(r.vendorConfDir, containersConfigDir, "registries.d"))
	}
	if r.systemConfDir != "" {
		dirs = append(dirs, filepath.Join(r.systemConfDir, containersConfigDir, "registries.d"))
	}
	if dir := r.userDir(); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "registries.d"))
	}
	return dirs
}

func (r *uapiResolver) PolicyPaths() []string {
	// UAPI: user → system → vendor (first found wins).
	var paths []string
	if dir := r.userDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, "policy.json"))
	}
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, "policy.json"))
	}
	if r.vendorConfDir != "" {
		paths = append(paths, filepath.Join(r.vendorConfDir, containersConfigDir, "policy.json"))
	}
	return paths
}

func (r *uapiResolver) DockerConfigPath() (string, error) {
	configDir := os.Getenv(dockerConfigDirEnv)
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		configDir = filepath.Join(home, dockerConfigFileDir)
	}
	return filepath.Join(configDir, dockerConfigFileName), nil
}

// defaultXDGConfigHome returns $XDG_CONFIG_HOME/containers if set,
// otherwise $HOME/.config/containers.
func defaultXDGConfigHome() string {
	if xdg := os.Getenv(xdgConfigHomeEnv); xdg != "" {
		return filepath.Join(xdg, containersConfigDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", containersConfigDir)
	}
	return ""
}
