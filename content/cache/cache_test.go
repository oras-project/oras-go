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

package cache

import (
	"os"
	"testing"

	"github.com/oras-project/oras-go/v3/content/memory"
)

func TestNewFromEnv(t *testing.T) {
	// save and restore original env
	orig := os.Getenv(CacheEnvVar)
	defer os.Setenv(CacheEnvVar, orig)

	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "empty env",
			envValue: "",
			want:     "",
		},
		{
			name:     "set env",
			envValue: "/tmp/oras-cache",
			want:     "/tmp/oras-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(CacheEnvVar, tt.envValue)
			cache := NewFromEnv()
			if cache.Root != tt.want {
				t.Errorf("NewFromEnv().Root = %v, want %v", cache.Root, tt.want)
			}
		})
	}
}

func TestCache_Enabled(t *testing.T) {
	tests := []struct {
		name  string
		cache *Cache
		want  bool
	}{
		{
			name:  "nil cache",
			cache: nil,
			want:  false,
		},
		{
			name:  "empty root",
			cache: &Cache{Root: ""},
			want:  false,
		},
		{
			name:  "with root",
			cache: &Cache{Root: "/tmp/cache"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cache.Enabled()
			if got != tt.want {
				t.Errorf("Cache.Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_CachedTarget_Disabled(t *testing.T) {
	source := memory.New()

	// empty root - disabled
	cache := &Cache{Root: ""}
	target, err := cache.CachedTarget(source)
	if err != nil {
		t.Fatal("CachedTarget() error =", err)
	}
	if target != source {
		t.Error("CachedTarget() should return original source when disabled")
	}

	// nil cache - disabled
	var nilCache *Cache
	target, err = nilCache.CachedTarget(source)
	if err != nil {
		t.Fatal("CachedTarget() on nil error =", err)
	}
	if target != source {
		t.Error("CachedTarget() on nil should return original source")
	}
}

func TestCache_CachedTarget_Enabled(t *testing.T) {
	source := memory.New()

	// create temp directory for cache
	tmpDir, err := os.MkdirTemp("", "oras-cache-test")
	if err != nil {
		t.Fatal("failed to create temp dir:", err)
	}
	defer os.RemoveAll(tmpDir)

	cache := &Cache{Root: tmpDir}
	target, err := cache.CachedTarget(source)
	if err != nil {
		t.Fatal("CachedTarget() error =", err)
	}
	if target == source {
		t.Error("CachedTarget() should return wrapped target when enabled")
	}
}

func TestCache_CachedTarget_InvalidPath(t *testing.T) {
	source := memory.New()

	// invalid path
	cache := &Cache{Root: "/nonexistent/path/that/should/not/exist/oras-cache"}
	_, err := cache.CachedTarget(source)
	if err == nil {
		t.Error("CachedTarget() should return error for invalid path")
	}
}
