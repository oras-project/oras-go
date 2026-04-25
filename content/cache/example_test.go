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

package cache_test

import (
	"fmt"

	"github.com/oras-project/oras-go/v3/content/cache"
	"github.com/oras-project/oras-go/v3/content/memory"
)

// ExampleCacheReadOnlyTarget demonstrates how to wrap a ReadOnlyTarget with a cache layer.
func ExampleCacheReadOnlyTarget() {
	// Create a source store
	source := memory.New()

	// Create a cache store (using memory store for this example)
	cacheStore := memory.New()

	// Wrap the source with a cache layer
	cachedTarget := cache.CacheReadOnlyTarget(source, cacheStore)

	// The cached target is ready to use
	// Fetched content will be cached for subsequent fetches
	fmt.Println(cachedTarget != nil)

	// Output: true
}

// ExampleCache_ReadOnlyTarget demonstrates how to use the Cache helper
// to create a cached target with file-based storage.
func ExampleCache_ReadOnlyTarget() {
	// Create a source store
	source := memory.New()

	// Create a Cache configuration with a valid root directory
	// In practice, you might use cache.NewFromEnv() to read from ORAS_CACHE env var
	c := &cache.Cache{
		Root: "/tmp/oras-cache", // Specify cache root directory
	}

	// Get the cached target
	target, err := c.ReadOnlyTarget(source)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// The target wraps source with caching
	fmt.Println(target != nil)

	// Output: true
}
