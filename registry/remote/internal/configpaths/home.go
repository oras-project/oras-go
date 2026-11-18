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
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	dockerConfigDirEnv   = "DOCKER_CONFIG"
	dockerConfigFileDir  = ".docker"
	dockerConfigFileName = "config.json"
)

// UserHomeDir returns [os.UserHomeDir], or else the home directory of the
// current account.
func UserHomeDir() (string, error) {
	return userHomeDir(os.UserHomeDir, os.Getuid())
}

// userHomeDir returns osHome(), or else the home directory of uid's account,
// as the Docker CLI and containers/image do. [user.LookupId], unlike
// [user.Current], neither caches nor reads $HOME.
func userHomeDir(osHome func() (string, error), uid int) (string, error) {
	home, err := osHome()
	if err == nil || uid < 0 {
		return home, err
	}
	u, uerr := user.LookupId(strconv.Itoa(uid))
	switch {
	case uerr != nil:
		return "", fmt.Errorf("%w (account lookup: %v)", err, uerr)
	case u.HomeDir == "":
		return "", err
	}
	return u.HomeDir, nil
}

// DockerConfigPath returns $DOCKER_CONFIG/config.json, or
// ~/.docker/config.json when $DOCKER_CONFIG is unset.
func DockerConfigPath() (string, error) {
	configDir := os.Getenv(dockerConfigDirEnv)
	if configDir == "" {
		home, err := UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		configDir = filepath.Join(home, dockerConfigFileDir)
	}
	return filepath.Join(configDir, dockerConfigFileName), nil
}
