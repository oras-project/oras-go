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
)

const (
	dockerConfigDirEnv   = "DOCKER_CONFIG"
	dockerConfigFileDir  = ".docker"
	dockerConfigFileName = "config.json"

	containersAuthFile  = "auth.json"
	containersConfigDir = "containers"
	xdgRuntimeDirEnv    = "XDG_RUNTIME_DIR"
)

// currentResolver implements PathResolver using the current containers/image
// path resolution strategy.
type currentResolver struct {
	// systemConfDir is the system-wide config parent directory
	// (e.g., "/etc"). Empty means no system-level paths are used.
	systemConfDir string
	// userConfRelDir is the user config directory relative to $HOME
	// (e.g., ".config/containers").
	userConfRelDir string
	// authUsesXDGRuntime indicates whether auth.json primary path should
	// check $XDG_RUNTIME_DIR (true on Linux/FreeBSD, false on macOS/Windows).
	authUsesXDGRuntime bool
}

func (r *currentResolver) MainConfigPaths(name string) []string {
	var paths []string
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, name+".conf"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, r.userConfRelDir, name+".conf"))
	}
	return paths
}

func (r *currentResolver) DropInDirs(name string) []string {
	var dirs []string
	if r.systemConfDir != "" {
		dirs = append(dirs, filepath.Join(r.systemConfDir, containersConfigDir, name+".conf.d"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, r.userConfRelDir, name+".conf.d"))
	}
	return dirs
}

func (r *currentResolver) MergeStrategy() MergeStrategy {
	return MergeAll
}

func (r *currentResolver) AuthPrimaryPath() (string, error) {
	if r.authUsesXDGRuntime {
		if xdgRuntime := os.Getenv(xdgRuntimeDirEnv); xdgRuntime != "" {
			path := filepath.Join(xdgRuntime, containersConfigDir, containersAuthFile)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, r.userConfRelDir, containersAuthFile), nil
}

func (r *currentResolver) AuthFallbackPaths() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	// $HOME/.config/containers/auth.json (if not already primary)
	if r.authUsesXDGRuntime {
		paths = append(paths, filepath.Join(home, r.userConfRelDir, containersAuthFile))
	}
	// Docker fallbacks
	paths = append(paths,
		filepath.Join(home, dockerConfigFileDir, dockerConfigFileName),
		filepath.Join(home, ".dockercfg"),
	)
	return paths
}

func (r *currentResolver) CertsDirPaths() []string {
	var paths []string
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, "certs.d"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, r.userConfRelDir, "certs.d"))
	}
	return paths
}

func (r *currentResolver) RegistriesDDirs() []string {
	var dirs []string
	if r.systemConfDir != "" {
		dirs = append(dirs, filepath.Join(r.systemConfDir, containersConfigDir, "registries.d"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, r.userConfRelDir, "registries.d"))
	}
	return dirs
}

func (r *currentResolver) PolicyPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, r.userConfRelDir, "policy.json"))
	}
	if r.systemConfDir != "" {
		paths = append(paths, filepath.Join(r.systemConfDir, containersConfigDir, "policy.json"))
	}
	return paths
}

func (r *currentResolver) DockerConfigPath() (string, error) {
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
