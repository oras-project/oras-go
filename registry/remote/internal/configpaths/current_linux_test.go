//go:build linux

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
	"testing"
)

func TestNewCurrentResolver_AuthUsesXDGRuntime(t *testing.T) {
	r := newCurrentResolver()
	if !r.authUsesXDGRuntime {
		t.Errorf("authUsesXDGRuntime = %v, want true", r.authUsesXDGRuntime)
	}
}

func TestNewResolver_AuthPrimaryPath_PreferContainersAuth(t *testing.T) {
	homeDir := t.TempDir()
	homeAuth := filepath.Join(homeDir, ".config", "containers", "auth.json")
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	r := NewResolver(ContainersImage)

	t.Run("prefer on file present", func(t *testing.T) {
		xdg := t.TempDir()
		want := filepath.Join(xdg, "containers", "auth.json")
		if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		if path != want {
			t.Errorf("AuthPrimaryPath(true) = %q, want runtime %q", path, want)
		}
	})

	t.Run("prefer on file missing", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		want := filepath.Join(xdg, "containers", "auth.json")
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		if path != want {
			t.Errorf("AuthPrimaryPath(true) = %q, want runtime %q", path, want)
		}
		if path == homeAuth {
			t.Fatal("must not return home as primary when the runtime file is missing")
		}
	})

	t.Run("prefer off file missing uses home", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		path, err := r.AuthPrimaryPath(false)
		if err != nil {
			t.Fatal(err)
		}
		if path != homeAuth {
			t.Errorf("AuthPrimaryPath(false) = %q, want home %q", path, homeAuth)
		}
	})

	t.Run("prefer off file present uses runtime", func(t *testing.T) {
		xdg := t.TempDir()
		want := filepath.Join(xdg, "containers", "auth.json")
		if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", xdg)
		path, err := r.AuthPrimaryPath(false)
		if err != nil {
			t.Fatal(err)
		}
		if path != want {
			t.Errorf("AuthPrimaryPath(false) = %q, want runtime %q", path, want)
		}
	})

	t.Run("prefer on runtime directory missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-such-runtime")
		t.Setenv("XDG_RUNTIME_DIR", missing)
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
		off, err := r.AuthPrimaryPath(false)
		if err != nil {
			t.Fatal(err)
		}
		if off != homeAuth {
			t.Errorf("AuthPrimaryPath(false) = %q, want home %q when runtime dir is missing", off, homeAuth)
		}
	})

	t.Run("prefer on unset is per-uid path only", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		path, err := r.AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		want := linuxPerUIDAuthPath(os.Getuid())
		if path != want {
			t.Errorf("AuthPrimaryPath(true) = %q, want %q", path, want)
		}
		if path == homeAuth {
			t.Fatal("must not return home as primary when XDG_RUNTIME_DIR is unset")
		}
		off, err := r.AuthPrimaryPath(false)
		if err != nil {
			t.Fatal(err)
		}
		if off != homeAuth {
			t.Errorf("AuthPrimaryPath(false) = %q, want home %q when XDG_RUNTIME_DIR is unset", off, homeAuth)
		}
	})
}
