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
	"io/fs"
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
	path, err := r.AuthPrimaryPath(false)
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("containers", "auth.json")) {
		t.Errorf("path %q does not end with containers/auth.json", path)
	}
}

func TestCurrentResolver_AuthPrimaryPath_XDGRuntime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_RUNTIME_DIR is only used on Linux")
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
	path, err := r.AuthPrimaryPath(false)
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "containers", "auth.json")
	if path != want {
		t.Errorf("AuthPrimaryPath() = %q, want %q", path, want)
	}
}

func TestCurrentResolver_AuthPrimaryPath_XDGRuntimeFlag(t *testing.T) {
	xdgDir := t.TempDir()
	xdgAuthDir := filepath.Join(xdgDir, "containers")
	if err := os.MkdirAll(xdgAuthDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgAuthDir, "auth.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	homeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdgDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	tests := []struct {
		name               string
		authUsesXDGRuntime bool
		want               string
	}{
		{
			name:               "true uses XDG path when auth.json exists",
			authUsesXDGRuntime: true,
			want:               filepath.Join(xdgDir, "containers", "auth.json"),
		},
		{
			name:               "false uses home path",
			authUsesXDGRuntime: false,
			want:               filepath.Join(homeDir, ".config", "containers", "auth.json"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &currentResolver{
				userConfRelDir:     ".config/containers",
				authUsesXDGRuntime: tt.authUsesXDGRuntime,
			}
			path, err := r.AuthPrimaryPath(false)
			if err != nil {
				t.Fatalf("AuthPrimaryPath() error: %v", err)
			}
			if path != tt.want {
				t.Errorf("AuthPrimaryPath() = %q, want %q", path, tt.want)
			}
		})
	}
}

func TestLinuxPerUIDAuthPath(t *testing.T) {
	got := linuxPerUIDAuthPath(1000)
	want := filepath.FromSlash("/run/containers/1000/auth.json")
	if got != want {
		t.Errorf("linuxPerUIDAuthPath(1000) = %q, want %q", got, want)
	}
}

func TestLinuxPreferContainersAuthPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	wantFile := filepath.Join(runtimeDir, "containers", "auth.json")

	t.Run("runtime dir exists returns auth path without requiring file", func(t *testing.T) {
		path, err := linuxPreferContainersAuthPath(runtimeDir, 1000, func(string) (os.FileInfo, error) {
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if path != wantFile {
			t.Errorf("path = %q, want %q", path, wantFile)
		}
	})
	t.Run("runtime dir missing is an error not home", func(t *testing.T) {
		path, err := linuxPreferContainersAuthPath(runtimeDir, 1000, func(string) (os.FileInfo, error) {
			return nil, fs.ErrNotExist
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
		if path != "" {
			t.Errorf("path = %q, want empty", path)
		}
	})
	t.Run("other stat error still returns auth path", func(t *testing.T) {
		path, err := linuxPreferContainersAuthPath(runtimeDir, 1000, func(string) (os.FileInfo, error) {
			return nil, errors.New("permission denied")
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if path != wantFile {
			t.Errorf("path = %q, want %q", path, wantFile)
		}
	})
	t.Run("unset runtime uses per-uid path", func(t *testing.T) {
		path, err := linuxPreferContainersAuthPath("", 42, func(string) (os.FileInfo, error) {
			t.Fatal("dirStat should not run when runtime dir is unset")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want := linuxPerUIDAuthPath(42)
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})
}

func TestCurrentResolver_AuthPrimaryPath_PreferContainersAuth(t *testing.T) {
	homeDir := t.TempDir()
	homeAuth := filepath.Join(homeDir, ".config", "containers", "auth.json")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	t.Run("flag off file missing uses home", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: true}
		path, err := r.AuthPrimaryPath(false)
		if err != nil {
			t.Fatal(err)
		}
		if path != homeAuth {
			t.Errorf("path = %q, want home %q", path, homeAuth)
		}
	})
	t.Run("flag on file missing still uses runtime path", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: true}
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(xdg, "containers", "auth.json")
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
		if path == homeAuth {
			t.Fatal("must not return home as primary when runtime file is missing")
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("test setup: runtime auth.json should be missing, stat err = %v", err)
		}
	})
	t.Run("flag on file present uses runtime path", func(t *testing.T) {
		xdg := t.TempDir()
		want := filepath.Join(xdg, "containers", "auth.json")
		if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: true}
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})
	t.Run("flag on runtime directory missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-such-runtime")
		t.Setenv("XDG_RUNTIME_DIR", missing)
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: true}
		path, err := r.AuthPrimaryPath(true)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
		if path != "" {
			t.Errorf("path = %q, want empty (not home %q)", path, homeAuth)
		}
	})
	t.Run("flag on unset uses per-uid path", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: true}
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		want := linuxPerUIDAuthPath(os.Getuid())
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
		if path == homeAuth {
			t.Fatal("must not return home as primary when XDG_RUNTIME_DIR is unset")
		}
	})
	t.Run("non-linux ignores prefer and XDG", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		r := &currentResolver{userConfRelDir: ".config/containers", authUsesXDGRuntime: false}
		for _, prefer := range []bool{false, true} {
			path, err := r.AuthPrimaryPath(prefer)
			if err != nil {
				t.Fatal(err)
			}
			if path != homeAuth {
				t.Errorf("prefer=%v path = %q, want home %q", prefer, path, homeAuth)
			}
		}
	})
}

func TestUAPIResolver_AuthPrimaryPath_IgnoresPrefer(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	r := NewResolver(UAPI)
	want := filepath.Join(tmpDir, "containers", "auth.json")
	for _, prefer := range []bool{false, true} {
		path, err := r.AuthPrimaryPath(prefer)
		if err != nil {
			t.Fatal(err)
		}
		if path != want {
			t.Errorf("prefer=%v path = %q, want %q", prefer, path, want)
		}
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

func TestCurrentResolver_AuthFallbackPaths(t *testing.T) {
	homeDir := t.TempDir()
	xdgDir := t.TempDir()
	dockerDir := t.TempDir()
	homeAuth := filepath.Join(homeDir, ".config", "containers", "auth.json")
	xdgAuth := filepath.Join(xdgDir, "containers", "auth.json")

	tests := []struct {
		name          string
		xdgConfigHome string
		dockerConfig  string
		want          []string
	}{
		{
			name:          "XDG_CONFIG_HOME set",
			xdgConfigHome: xdgDir,
			want:          []string{xdgAuth},
		},
		{
			name: "XDG_CONFIG_HOME unset",
			want: []string{homeAuth},
		},
		{
			name:          "empty XDG_CONFIG_HOME treated as unset",
			xdgConfigHome: "",
			want:          []string{homeAuth},
		},
		{
			name:          "DOCKER_CONFIG set is not returned",
			xdgConfigHome: xdgDir,
			dockerConfig:  dockerDir,
			want:          []string{xdgAuth},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", homeDir)
			t.Setenv("USERPROFILE", homeDir)
			t.Setenv("XDG_CONFIG_HOME", tt.xdgConfigHome)
			t.Setenv("DOCKER_CONFIG", tt.dockerConfig)

			r := &currentResolver{userConfRelDir: ".config/containers"}
			got := r.AuthFallbackPaths()
			if len(got) != len(tt.want) {
				t.Fatalf("AuthFallbackPaths() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("AuthFallbackPaths()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
			for _, p := range got {
				if strings.HasSuffix(p, filepath.Join(".docker", "config.json")) || strings.HasSuffix(p, ".dockercfg") {
					t.Errorf("AuthFallbackPaths() unexpectedly includes Docker path %q", p)
				}
			}
		})
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
	path, err := r.AuthPrimaryPath(false)
	if err != nil {
		t.Fatalf("AuthPrimaryPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "containers", "auth.json")
	if path != want {
		t.Errorf("AuthPrimaryPath() = %q, want %q", path, want)
	}
}

func TestUAPIResolver_AuthPrimaryPath_NoUserDir(t *testing.T) {
	r := &uapiResolver{
		userConfDir: nil,
	}
	_, err := r.AuthPrimaryPath(true)
	if err == nil {
		t.Error("AuthPrimaryPath() expected error when userConfDir is nil")
	}
}

func TestUAPIResolver_UserDir_NilFunc(t *testing.T) {
	r := &uapiResolver{
		userConfDir: nil,
	}
	got := r.userDir()
	if got != "" {
		t.Errorf("userDir() = %q, want empty string", got)
	}
}

func TestUAPIResolver_AuthFallbackPaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
	paths := r.AuthFallbackPaths()
	for _, p := range paths {
		if strings.Contains(p, ".docker") || strings.HasSuffix(p, ".dockercfg") {
			t.Errorf("AuthFallbackPaths() includes Docker path %q", p)
		}
	}
}

func TestUAPIResolver_CertsDirPaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
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

func TestUAPIResolver_RegistriesDDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
	dirs := r.RegistriesDDirs()
	if len(dirs) == 0 {
		t.Fatal("RegistriesDDirs returned empty")
	}
	for _, d := range dirs {
		if !strings.HasSuffix(d, "registries.d") {
			t.Errorf("dir %q does not end with registries.d", d)
		}
	}
}

func TestUAPIResolver_PolicyPaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	r := NewResolver(UAPI)
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

func TestUAPIResolver_DockerConfigPath(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	r := NewResolver(UAPI)
	path, err := r.DockerConfigPath()
	if err != nil {
		t.Fatalf("DockerConfigPath() error: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join(".docker", "config.json")) {
		t.Errorf("path %q does not end with .docker/config.json", path)
	}
}

func TestUAPIResolver_DockerConfigPath_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", tmpDir)
	r := NewResolver(UAPI)
	path, err := r.DockerConfigPath()
	if err != nil {
		t.Fatalf("DockerConfigPath() error: %v", err)
	}
	want := filepath.Join(tmpDir, "config.json")
	if path != want {
		t.Errorf("DockerConfigPath() = %q, want %q", path, want)
	}
}

func TestDefaultXDGConfigHome_WithXDGSet(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	got := defaultXDGConfigHome()
	want := filepath.Join(tmpDir, "containers")
	if got != want {
		t.Errorf("defaultXDGConfigHome() = %q, want %q", got, want)
	}
}

func TestDefaultXDGConfigHome_WithoutXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	got := defaultXDGConfigHome()
	want := filepath.Join(tmpDir, ".config", "containers")
	if got != want {
		t.Errorf("defaultXDGConfigHome() = %q, want %q", got, want)
	}
}

func TestUAPIResolver_CertsDirPaths_AllTiers(t *testing.T) {
	r := &uapiResolver{
		vendorConfDir: "/usr/share",
		systemConfDir: "/etc",
		userConfDir:   func() string { return "/home/testuser/.config/containers" },
	}
	paths := r.CertsDirPaths()
	if len(paths) != 3 {
		t.Fatalf("CertsDirPaths() returned %d paths, want 3", len(paths))
	}
	if paths[0] != filepath.Join("/usr/share", "containers", "certs.d") {
		t.Errorf("paths[0] = %q, want vendor certs.d", paths[0])
	}
	if paths[1] != filepath.Join("/etc", "containers", "certs.d") {
		t.Errorf("paths[1] = %q, want system certs.d", paths[1])
	}
	if paths[2] != filepath.Join("/home/testuser/.config/containers", "certs.d") {
		t.Errorf("paths[2] = %q, want user certs.d", paths[2])
	}
}

func TestUAPIResolver_RegistriesDDirs_AllTiers(t *testing.T) {
	r := &uapiResolver{
		vendorConfDir: "/usr/share",
		systemConfDir: "/etc",
		userConfDir:   func() string { return "/home/testuser/.config/containers" },
	}
	dirs := r.RegistriesDDirs()
	if len(dirs) != 3 {
		t.Fatalf("RegistriesDDirs() returned %d dirs, want 3", len(dirs))
	}
	if dirs[0] != filepath.Join("/usr/share", "containers", "registries.d") {
		t.Errorf("dirs[0] = %q, want vendor registries.d", dirs[0])
	}
	if dirs[1] != filepath.Join("/etc", "containers", "registries.d") {
		t.Errorf("dirs[1] = %q, want system registries.d", dirs[1])
	}
	if dirs[2] != filepath.Join("/home/testuser/.config/containers", "registries.d") {
		t.Errorf("dirs[2] = %q, want user registries.d", dirs[2])
	}
}

func TestUAPIResolver_PolicyPaths_AllTiers(t *testing.T) {
	r := &uapiResolver{
		vendorConfDir: "/usr/share",
		systemConfDir: "/etc",
		userConfDir:   func() string { return "/home/testuser/.config/containers" },
	}
	paths := r.PolicyPaths()
	if len(paths) != 3 {
		t.Fatalf("PolicyPaths() returned %d paths, want 3", len(paths))
	}
	// UAPI order: user → system → vendor.
	if paths[0] != filepath.Join("/home/testuser/.config/containers", "policy.json") {
		t.Errorf("paths[0] = %q, want user policy.json", paths[0])
	}
	if paths[1] != filepath.Join("/etc", "containers", "policy.json") {
		t.Errorf("paths[1] = %q, want system policy.json", paths[1])
	}
	if paths[2] != filepath.Join("/usr/share", "containers", "policy.json") {
		t.Errorf("paths[2] = %q, want vendor policy.json", paths[2])
	}
}

func TestUAPIResolver_MainConfigPaths_NoOptionalDirs(t *testing.T) {
	r := &uapiResolver{
		vendorConfDir: "",
		systemConfDir: "",
		userConfDir:   nil,
	}
	paths := r.MainConfigPaths("registries")
	if len(paths) != 0 {
		t.Errorf("MainConfigPaths() returned %d paths, want 0", len(paths))
	}
}

func TestUAPIResolver_DropInDirs_NoOptionalDirs(t *testing.T) {
	r := &uapiResolver{
		vendorConfDir:           "",
		systemConfDir:           "",
		userConfDir:             nil,
		supportsRootfulRootless: false,
	}
	dirs := r.DropInDirs("registries")
	if len(dirs) != 0 {
		t.Errorf("DropInDirs() returned %d dirs, want 0", len(dirs))
	}
}

func TestUAPIResolver_AppendRootDirs_NotSupported(t *testing.T) {
	r := &uapiResolver{
		supportsRootfulRootless: false,
	}
	dirs := r.appendRootDirs(nil, "/etc/containers", "registries")
	if len(dirs) != 0 {
		t.Errorf("appendRootDirs() returned %d dirs, want 0 when not supported", len(dirs))
	}
}
