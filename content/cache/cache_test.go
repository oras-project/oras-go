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
	orig := os.Getenv(EnvVarName)
	defer os.Setenv(EnvVarName, orig)

	tests := []struct {
		name     string
		envValue string
		wantNil  bool
		wantRoot string
	}{
		{
			name:     "empty env returns nil",
			envValue: "",
			wantNil:  true,
		},
		{
			name:     "set env returns cache",
			envValue: "/tmp/oras-cache",
			wantNil:  false,
			wantRoot: "/tmp/oras-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(EnvVarName, tt.envValue)
			cache := NewFromEnv()
			if tt.wantNil {
				if cache != nil {
					t.Errorf("NewFromEnv() = %v, want nil", cache)
				}
			} else {
				if cache == nil {
					t.Fatal("NewFromEnv() = nil, want non-nil")
				}
				if cache.Root != tt.wantRoot {
					t.Errorf("NewFromEnv().Root = %v, want %v", cache.Root, tt.wantRoot)
				}
			}
		})
	}
}

func TestCache_ReadOnlyTarget(t *testing.T) {
	source := memory.New()

	// create temp directory for cache
	tmpDir, err := os.MkdirTemp("", "oras-cache-test")
	if err != nil {
		t.Fatal("failed to create temp dir:", err)
	}
	defer os.RemoveAll(tmpDir)

	cache := &Cache{Root: tmpDir}
	target, err := cache.ReadOnlyTarget(source)
	if err != nil {
		t.Fatal("ReadOnlyTarget() error =", err)
	}
	if target == source {
		t.Error("ReadOnlyTarget() should return wrapped target")
	}
}

