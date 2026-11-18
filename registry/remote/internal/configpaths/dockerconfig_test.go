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
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

var errNoHome = errors.New("no home")

func noHome() (string, error) { return "", errNoHome }

func TestUserHomeDir_OSHome(t *testing.T) {
	want := t.TempDir()
	home, err := userHomeDir(func() (string, error) { return want, nil }, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if home != want {
		t.Errorf("home = %q, want %q", home, want)
	}
}

func TestUserHomeDir_AccountHome(t *testing.T) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || u.HomeDir == "" {
		t.Skip("no account for the current uid")
	}
	home, err := userHomeDir(noHome, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if home != u.HomeDir {
		t.Errorf("home = %q, want %q", home, u.HomeDir)
	}
}

func TestUserHomeDir_NoAccount(t *testing.T) {
	const uid = 1<<31 - 2
	if _, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		t.Skipf("uid %d has an account", uid)
	}
	home, err := userHomeDir(noHome, uid)
	if !errors.Is(err, errNoHome) {
		t.Errorf("err = %v, want %v", err, errNoHome)
	}
	if home != "" {
		t.Errorf("home = %q, want empty", home)
	}
}

func TestUserHomeDir_NegativeUID(t *testing.T) {
	home, err := userHomeDir(noHome, -1)
	if err != errNoHome {
		t.Errorf("err = %v, want %v", err, errNoHome)
	}
	if home != "" {
		t.Errorf("home = %q, want empty", home)
	}
}

func TestDockerConfigPath_NoHOME(t *testing.T) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || u.HomeDir == "" {
		t.Skip("no account for the current uid")
	}
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir does not read $HOME on this platform")
	}
	got, err := DockerConfigPath()
	if want := filepath.Join(u.HomeDir, ".docker", "config.json"); err != nil || got != want {
		t.Errorf("DockerConfigPath() = %q, %v, want %q", got, err, want)
	}
}
