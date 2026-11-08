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

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configpaths"
)

func TestLoadConfigs_LinuxPreferContainersAuth_RealResolver(t *testing.T) {
	const host, fallbackB64, homeB64 = "reg.example.com", "eGRnOnBhc3M=", "aG9tZTpwYXNz"
	storeOpts := credentials.StoreOptions{AllowPlaintextPut: true, IgnoreDefaultNativeStore: true}

	t.Run("missing runtime file loads XDG_CONFIG_HOME and Put writes it", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		runtimePath := isolatedRuntimeAuth(t)
		homePath := writeAuthJSON(t, filepath.Dir(isolatedHomeAuth(tmp)), host, homeB64)
		fallbackPath := writeAuthJSON(t, filepath.Dir(isolatedXDGConfigAuth(tmp)), host, fallbackB64)

		primary, err := configpaths.NewResolver(configpaths.ContainersImage).AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		if primary != runtimePath {
			t.Fatalf("NewResolver AuthPrimaryPath(true) = %q, want isolated runtime %q", primary, runtimePath)
		}

		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{PreferContainersAuth: true})
		if err != nil {
			t.Fatal(err)
		}
		if configs.ContainersAuthConfig.Path() != fallbackPath {
			t.Fatalf("loaded path = %q, want fallback %q", configs.ContainersAuthConfig.Path(), fallbackPath)
		}
		if configs.ContainersAuthConfig.Path() == homePath {
			t.Fatal("home auth.json must not become primary when the runtime file is missing")
		}
		store, err := configs.CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		assertPutWroteFallbackNotPrimary(t, store, fallbackPath, runtimePath, homePath)
	})

	t.Run("missing runtime directory skips primary and Put writes fallback", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		missingRuntime := filepath.Join(tmp, "no-such-runtime")
		t.Setenv("XDG_RUNTIME_DIR", missingRuntime)
		homePath := writeAuthJSON(t, filepath.Dir(isolatedHomeAuth(tmp)), host, homeB64)
		fallbackPath := writeAuthJSON(t, filepath.Dir(isolatedXDGConfigAuth(tmp)), host, fallbackB64)

		if _, err := configpaths.NewResolver(configpaths.ContainersImage).AuthPrimaryPath(true); err == nil {
			t.Fatal("AuthPrimaryPath(true) expected error when the runtime directory is missing")
		}

		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{PreferContainersAuth: true})
		if err != nil {
			t.Fatal(err)
		}
		if configs.ContainersAuthConfig.Path() != fallbackPath {
			t.Fatalf("loaded path = %q, want fallback %q", configs.ContainersAuthConfig.Path(), fallbackPath)
		}
		if configs.ContainersAuthConfig.Path() == homePath {
			t.Fatal("must not fall through to home as primary when the runtime directory is missing")
		}
		store, err := configs.CredentialStore(storeOpts)
		if err != nil {
			t.Fatal(err)
		}
		assertPutWroteFallbackNotPrimary(t, store, fallbackPath, filepath.Join(missingRuntime, "containers", "auth.json"), homePath)
	})

	t.Run("flag off still loads home when runtime file is missing", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		homePath := writeAuthJSON(t, filepath.Dir(isolatedHomeAuth(tmp)), host, homeB64)
		writeAuthJSON(t, filepath.Dir(isolatedXDGConfigAuth(tmp)), host, fallbackB64)
		configs, err := LoadConfigsWithOptions(LoadConfigsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(configs.ContainersAuthConfigs) != 1 || configs.ContainersAuthConfig.Path() != homePath {
			t.Fatalf("flag off path = %q, want home %q", configs.ContainersAuthConfig.Path(), homePath)
		}
	})

	t.Run("unset runtime is a per-uid path assertion only", func(t *testing.T) {
		tmp := t.TempDir()
		isolateLoadEnv(t, tmp)
		t.Setenv("XDG_RUNTIME_DIR", "")
		path, err := configpaths.NewResolver(configpaths.ContainersImage).AuthPrimaryPath(true)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.FromSlash(fmt.Sprintf("/run/containers/%d/auth.json", os.Getuid()))
		if path != want {
			t.Errorf("AuthPrimaryPath(true) unset = %q, want %q", path, want)
		}
	})
}
