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
	"os"
	"path/filepath"
	"strconv"
)

const (
	xdgConfigHomeEnv = "XDG_CONFIG_HOME"
)

// uapiResolver implements PathResolver using the UAPI-based three-tier
// path resolution (vendor + system + user) with rootful/rootless directories.
type uapiResolver struct {
	vendorConfDir           string
	systemConfDir           string
	userConfDir             func() string
	supportsRootfulRootless bool
}

func (r *uapiResolver) userDir() string {
	if r.userConfDir != nil {
		return r.userConfDir()
	}
	return ""
}

// MainConfigPaths returns paths in UAPI order: user -> system -> vendor
// (first found wins).
func (r *uapiResolver) MainConfigPaths(name string) []string {
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

// DropInDirs returns drop-in directories from all tiers in merge order:
// vendor -> system -> user.
func (r *uapiResolver) DropInDirs(name string) []string {
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

func defaultXDGConfigHome() string {
	if xdg := os.Getenv(xdgConfigHomeEnv); xdg != "" {
		return filepath.Join(xdg, containersConfigDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", containersConfigDir)
	}
	return ""
}
