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
		t.Errorf("MergeStrategy() = %v, want MergeAll", r.MergeStrategy())
	}
}

func TestNewResolver_UAPI(t *testing.T) {
	r := NewResolver(UAPI)
	if r == nil {
		t.Fatal("NewResolver(UAPI) returned nil")
	}
	if r.MergeStrategy() != FirstFoundWins {
		t.Errorf("MergeStrategy() = %v, want FirstFoundWins", r.MergeStrategy())
	}
}

func TestCurrentResolver_MainConfigPaths(t *testing.T) {
	r := NewResolver(ContainersImage)
	paths := r.MainConfigPaths("registries")
	if len(paths) == 0 {
		t.Fatal("MainConfigPaths returned empty")
	}
	// All paths should end with registries.conf
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
		t.Fatal("DropInDirs returned empty")
	}
	for _, d := range dirs {
		if !strings.HasSuffix(d, "registries.conf.d") {
			t.Errorf("dir %q does not end with registries.conf.d", d)
		}
	}
}

func TestCurrentResolver_AuthPrimaryPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	r := NewResolver(ContainersImage)
	path, err := r.AuthPrimaryPath()
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("containers", "auth.json")) {
		t.Errorf("path %q does not end with containers/auth.json", path)
	}
}

func TestCurrentResolver_AuthPrimaryPath_XDGRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_RUNTIME_DIR not used on Windows")
	}
	tmpDir := t.TempDir()
	xdgAuthDir := filepath.Join(tmpDir, "containers")
	if err := os.MkdirAll(xdgAuthDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgAuthDir, "auth.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	r := NewResolver(ContainersImage)
	path, err := r.AuthPrimaryPath()
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "containers", "auth.json")
	if path != want {
		t.Errorf("AuthPrimaryPath() = %q, want %q", path, want)
	}
}

func TestCurrentResolver_CertsDirPaths(t *testing.T) {
	r := NewResolver(ContainersImage)
	paths := r.CertsDirPaths()
	if len(paths) == 0 {
		t.Fatal("CertsDirPaths returned empty")
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "certs.d") {
			t.Errorf("path %q does not end with certs.d", p)
		}
	}
}

func TestCurrentResolver_PolicyPaths(t *testing.T) {
	r := NewResolver(ContainersImage)
	paths := r.PolicyPaths()
	if len(paths) == 0 {
		t.Fatal("PolicyPaths returned empty")
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "policy.json") {
			t.Errorf("path %q does not end with policy.json", p)
		}
	}
}

func TestCurrentResolver_DockerConfigPath(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "")
	r := NewResolver(ContainersImage)
	path, err := r.DockerConfigPath()
	if err != nil {
		t.Fatalf("DockerConfigPath() error: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join(".docker", "config.json")) {
		t.Errorf("path %q does not end with .docker/config.json", path)
	}
}

func TestCurrentResolver_DockerConfigPath_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", tmpDir)
	r := NewResolver(ContainersImage)
	path, err := r.DockerConfigPath()
	if err != nil {
		t.Fatalf("DockerConfigPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "config.json")
	if path != want {
		t.Errorf("DockerConfigPath() = %q, want %q", path, want)
	}
}

func TestUAPIResolver_MainConfigPaths(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	t.Setenv("XDG_CONFIG_HOME", "")
	r := NewResolver(UAPI)
	paths := r.MainConfigPaths("registries")

	// Should have at least user path; on non-Windows also system and vendor.
	if len(paths) == 0 {
		t.Fatal("MainConfigPaths returned empty")
	}

	// First path should be user (UAPI: user → system → vendor).
	if !strings.Contains(paths[0], "fakehome") && !strings.Contains(paths[0], "APPDATA") {
		t.Errorf("first path %q should be user-level", paths[0])
	}

	for _, p := range paths {
		if !strings.HasSuffix(p, "registries.conf") {
			t.Errorf("path %q does not end with registries.conf", p)
		}
	}
}

func TestUAPIResolver_DropInDirs(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	t.Setenv("XDG_CONFIG_HOME", "")
	r := NewResolver(UAPI)
	dirs := r.DropInDirs("registries")

	if len(dirs) == 0 {
		t.Fatal("DropInDirs returned empty")
	}

	// Should include .conf.d directories.
	hasConfD := false
	for _, d := range dirs {
		if strings.HasSuffix(d, "registries.conf.d") {
			hasConfD = true
		}
	}
	if !hasConfD {
		t.Error("DropInDirs should include registries.conf.d directories")
	}

	// On non-Windows, should include rootful or rootless dirs.
	if runtime.GOOS != "windows" {
		hasRootDir := false
		for _, d := range dirs {
			if strings.Contains(d, "rootful") || strings.Contains(d, "rootless") {
				hasRootDir = true
			}
		}
		if !hasRootDir {
			t.Error("DropInDirs should include rootful/rootless directories on non-Windows")
		}
	}
}

func TestUAPIResolver_XDGConfigHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
	paths := r.MainConfigPaths("registries")

	// First path should use XDG_CONFIG_HOME.
	want := filepath.Join(tmpDir, "containers", "registries.conf")
	if len(paths) == 0 || paths[0] != want {
		t.Errorf("first MainConfigPath = %q, want %q", paths[0], want)
	}
}

func TestUAPIResolver_AuthPrimaryPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
	path, err := r.AuthPrimaryPath()
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "containers", "auth.json")
	if path != want {
		t.Errorf("AuthPrimaryPath() = %q, want %q", path, want)
	}
}
