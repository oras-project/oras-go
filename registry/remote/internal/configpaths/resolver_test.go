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
	"runtime"
	"strings"
	"testing"
)

func TestNewResolver_Default(t *testing.T) {
	r := NewResolver(ContainersImage)
	if r == nil {
		t.Fatal("NewResolver(ContainersImage) returned nil")
	}
	if r.MergeStrategy() != MergeAll {
		t.Errorf("MergeStrategy() = %d, want MergeAll (%d)", r.MergeStrategy(), MergeAll)
	}
}

func TestNewResolver_UAPI(t *testing.T) {
	r := NewResolver(UAPI)
	if r == nil {
		t.Fatal("NewResolver(UAPI) returned nil")
	}
	if r.MergeStrategy() != FirstFoundWins {
		t.Errorf("MergeStrategy() = %d, want FirstFoundWins (%d)", r.MergeStrategy(), FirstFoundWins)
	}
}

func TestCurrentResolver_MainConfigPaths(t *testing.T) {
	r := NewResolver(ContainersImage)
	paths := r.MainConfigPaths("registries")
	if len(paths) == 0 {
		t.Fatal("MainConfigPaths returned empty slice")
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "registries.conf") {
			t.Errorf("path %q does not end with registries.conf", p)
		}
	}
}

func TestCurrentResolver_DropInDirs(t *testing.T) {
	r := NewResolver(ContainersImage)
	dirs := r.DropInDirs("registries")
	if len(dirs) == 0 {
		t.Fatal("DropInDirs returned empty slice")
	}
	for _, d := range dirs {
		if !strings.HasSuffix(d, "registries.conf.d") {
			t.Errorf("dir %q does not end with registries.conf.d", d)
		}
	}
}

func TestUAPIResolver_MainConfigPaths(t *testing.T) {
	r := NewResolver(UAPI)
	paths := r.MainConfigPaths("registries")
	if len(paths) == 0 {
		t.Fatal("MainConfigPaths returned empty slice")
	}
	// First path should be user-level (contains home dir or XDG path).
	// On all platforms the user path comes first in UAPI order.
	first := paths[0]
	if !strings.HasSuffix(first, "registries.conf") {
		t.Errorf("first path %q does not end with registries.conf", first)
	}
	// Verify user path comes before system path by checking that the first
	// path does not start with a system directory.
	if runtime.GOOS != "windows" {
		if strings.HasPrefix(first, "/etc/") || strings.HasPrefix(first, "/usr/") {
			t.Errorf("first UAPI path %q appears to be a system path, expected user path first", first)
		}
	}
}

func TestUAPIResolver_DropInDirs(t *testing.T) {
	r := NewResolver(UAPI)
	dirs := r.DropInDirs("registries")
	if len(dirs) == 0 {
		t.Fatal("DropInDirs returned empty slice")
	}
	// On non-Windows, expect rootful or rootless directories.
	if runtime.GOOS != "windows" {
		foundRoot := false
		for _, d := range dirs {
			if strings.Contains(d, "rootful") || strings.Contains(d, "rootless") {
				foundRoot = true
				break
			}
		}
		if !foundRoot {
			t.Error("DropInDirs on non-Windows should include rootful or rootless directories")
		}
	}
	// All conf.d dirs should contain "registries" in the path.
	for _, d := range dirs {
		if !strings.Contains(d, "registries") {
			t.Errorf("dir %q does not contain 'registries'", d)
		}
	}
}

func TestUAPIResolver_XDGConfigHome(t *testing.T) {
	// Save and restore XDG_CONFIG_HOME.
	origXDG := os.Getenv(xdgConfigHomeEnv)
	defer os.Setenv(xdgConfigHomeEnv, origXDG)

	customDir := t.TempDir()
	os.Setenv(xdgConfigHomeEnv, customDir)

	r := NewResolver(UAPI)
	paths := r.MainConfigPaths("registries")
	if len(paths) == 0 {
		t.Fatal("MainConfigPaths returned empty slice")
	}
	// The first path (user level) should be under our custom XDG dir.
	if !strings.HasPrefix(paths[0], customDir) {
		t.Errorf("first path %q should start with XDG_CONFIG_HOME %q", paths[0], customDir)
	}
}
