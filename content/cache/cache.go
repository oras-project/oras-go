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

	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/oci"
)

// CacheEnvVar is the environment variable name for specifying the cache root.
const CacheEnvVar = "ORAS_CACHE"

// Cache provides options for configuring a cache layer.
type Cache struct {
	// Root specifies the cache root directory.
	// If empty, caching is disabled.
	Root string
}

// NewFromEnv creates a Cache with the root set from the ORAS_CACHE environment variable.
func NewFromEnv() *Cache {
	return &Cache{
		Root: os.Getenv(CacheEnvVar),
	}
}

// Enabled returns true if caching is enabled (i.e., Root is non-empty).
func (c *Cache) Enabled() bool {
	return c != nil && c.Root != ""
}

// CachedTarget wraps the source ReadOnlyTarget with a caching layer if
// caching is enabled. If caching is disabled, the original source is returned.
func (c *Cache) CachedTarget(source oras.ReadOnlyTarget) (oras.ReadOnlyTarget, error) {
	if !c.Enabled() {
		return source, nil
	}

	ociStore, err := oci.New(c.Root)
	if err != nil {
		return nil, err
	}

	return New(source, ociStore), nil
}
