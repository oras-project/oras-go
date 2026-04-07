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
)

const (
	containersConfigDir = "containers"
)

// currentResolver implements PathResolver using the current containers/image
// path resolution strategy.
type currentResolver struct {
	// systemConfDir is the system-wide config parent directory (e.g., "/etc").
	systemConfDir string
	// userConfRelDir is the user config directory relative to $HOME.
	userConfRelDir string
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
