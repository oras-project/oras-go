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
	"strings"
	"testing"
)

var errNoHome = errors.New("no home")

func noHome() (string, error) { return "", errNoHome }

// unsetHome unsets $HOME and returns the account home, or skips the test.
func unsetHome(t *testing.T) string {
	t.Helper()
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || u.HomeDir == "" {
		t.Skip("no account for the current uid")
	}
	t.Setenv("HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir does not read $HOME on this platform")
	}
	return u.HomeDir
}

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
	want := unsetHome(t)
	home, err := UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if home != want {
		t.Errorf("home = %q, want %q", home, want)
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

func TestResolver_NoHOME(t *testing.T) {
	home := unsetHome(t)
	t.Setenv(xdgConfigHomeEnv, "")
	for name, s := range map[string]Strategy{"ContainersImage": ContainersImage, "UAPI": UAPI} {
		t.Run(name, func(t *testing.T) {
			paths := NewResolver(s).PolicyPaths()
			if len(paths) == 0 || !strings.HasPrefix(paths[0], home+string(filepath.Separator)) {
				t.Errorf("PolicyPaths() = %q, want the first under %q", paths, home)
			}
		})
	}
}
