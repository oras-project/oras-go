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
)

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
