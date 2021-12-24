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
package auth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oras.land/oras-go/v2/errdef"
)

func Test_concurrentCache_GetScheme(t *testing.T) {
	cache := NewCache()

	// no entry in the cache
	ctx := context.Background()
	registry := "localhost:5000"
	got, err := cache.GetScheme(ctx, registry)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetScheme() error = %v, wantErr %v", err, want)
	}
	if got != SchemeUnknown {
		t.Errorf("concurrentCache.GetScheme() = %v, want %v", got, SchemeUnknown)
	}

	// set an cache entry
	scheme := SchemeBasic
	_, err = cache.Set(ctx, registry, scheme, "", func(c context.Context) (string, error) {
		return "foo", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetScheme(ctx, registry)
	if err != nil {
		t.Fatalf("concurrentCache.GetScheme() error = %v", err)
	}
	if got != scheme {
		t.Errorf("concurrentCache.GetScheme() = %v, want %v", got, scheme)
	}

	// set cache entry again
	scheme = SchemeBearer
	_, err = cache.Set(ctx, registry, scheme, "", func(c context.Context) (string, error) {
		return "bar", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetScheme(ctx, registry)
	if err != nil {
		t.Fatalf("concurrentCache.GetScheme() error = %v", err)
	}
	if got != scheme {
		t.Errorf("concurrentCache.GetScheme() = %v, want %v", got, scheme)
	}

	// test other registry
	registry = "localhost:5001"
	got, err = cache.GetScheme(ctx, registry)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetScheme() error = %v, wantErr %v", err, want)
	}
	if got != SchemeUnknown {
		t.Errorf("concurrentCache.GetScheme() = %v, want %v", got, SchemeUnknown)
	}
}

func Test_concurrentCache_GetToken(t *testing.T) {
	cache := NewCache()

	// no entry in the cache
	ctx := context.Background()
	registry := "localhost:5000"
	scheme := SchemeBearer
	key := "1st key"
	got, err := cache.GetToken(ctx, registry, scheme, key)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
	}
	if got != "" {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, "")
	}

	// set an cache entry
	_, err = cache.Set(ctx, registry, scheme, key, func(c context.Context) (string, error) {
		return "foo", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "foo"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// set cache entry again
	_, err = cache.Set(ctx, registry, scheme, key, func(c context.Context) (string, error) {
		return "bar", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "bar"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// test other key
	key = "2nd key"
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
	}
	if got != "" {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, "")
	}

	// set an cache entry
	_, err = cache.Set(ctx, registry, scheme, key, func(c context.Context) (string, error) {
		return "hello world", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "hello world"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// verify cache of the previous key as keys should not interference each
	// other
	key = "1st key"
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "bar"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// test other registry
	registry = "localhost:5001"
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
	}
	if got != "" {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, "")
	}

	// set an cache entry
	_, err = cache.Set(ctx, registry, scheme, key, func(c context.Context) (string, error) {
		return "foobar", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "foobar"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// verify cache of the previous registry as registries should not
	// interference each other
	registry = "localhost:5000"
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "bar"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// test other scheme
	scheme = SchemeBasic
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
	}
	if got != "" {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, "")
	}

	// set an cache entry
	_, err = cache.Set(ctx, registry, scheme, key, func(c context.Context) (string, error) {
		return "new scheme", nil
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// verify cache
	got, err = cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Fatalf("concurrentCache.GetToken() error = %v", err)
	}
	if want := "new scheme"; got != want {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, want)
	}

	// cache of the previous scheme should be invalidated due to scheme change.
	got, err = cache.GetToken(ctx, registry, SchemeBearer, key)
	if want := errdef.ErrNotFound; err != want {
		t.Fatalf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
	}
	if got != "" {
		t.Errorf("concurrentCache.GetToken() = %v, want %v", got, "")
	}
}

func Test_concurrentCache_Set(t *testing.T) {
	registries := []string{
		"localhost:5000",
		"localhost:5001",
	}
	scheme := SchemeBearer
	keys := []string{
		"foo",
		"bar",
	}
	count := len(registries) * len(keys)

	ctx := context.Background()
	cache := NewCache()

	// first round of fetch
	fetch := func(i int) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			return strconv.Itoa(i), nil
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for j := 0; j < count; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				key := keys[(i>>1)&1]
				got, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if err != nil {
					t.Errorf("concurrentCache.Set() error = %v", err)
				}
				if want := strconv.Itoa(i); got != want {
					t.Errorf("concurrentCache.Set() = %v, want %v", got, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < count; i++ {
		registry := registries[i&1]
		key := keys[(i>>1)&1]

		gotScheme, err := cache.GetScheme(ctx, registry)
		if err != nil {
			t.Fatalf("concurrentCache.GetScheme() error = %v", err)
		}
		if want := scheme; gotScheme != want {
			t.Errorf("concurrentCache.GetScheme() = %v, want %v", gotScheme, want)
		}

		gotToken, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Fatalf("concurrentCache.GetToken() error = %v", err)
		}
		if want := strconv.Itoa(i); gotToken != want {
			t.Errorf("concurrentCache.GetToken() = %v, want %v", gotToken, want)
		}
	}

	// repeated fetch
	fetch = func(i int) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			return strconv.Itoa(i) + " repeated", nil
		}
	}
	for i := 0; i < 10; i++ {
		for j := 0; j < count; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				key := keys[(i>>1)&1]
				got, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if err != nil {
					t.Errorf("concurrentCache.Set() error = %v", err)
				}
				if want := strconv.Itoa(i) + " repeated"; got != want {
					t.Errorf("concurrentCache.Set() = %v, want %v", got, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < count; i++ {
		registry := registries[i&1]
		key := keys[(i>>1)&1]

		gotScheme, err := cache.GetScheme(ctx, registry)
		if err != nil {
			t.Fatalf("concurrentCache.GetScheme() error = %v", err)
		}
		if want := scheme; gotScheme != want {
			t.Errorf("concurrentCache.GetScheme() = %v, want %v", gotScheme, want)
		}

		gotToken, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Fatalf("concurrentCache.GetToken() error = %v", err)
		}
		if want := strconv.Itoa(i) + " repeated"; gotToken != want {
			t.Errorf("concurrentCache.GetToken() = %v, want %v", gotToken, want)
		}
	}
}

func Test_concurrentCache_Set_Fetch_Once(t *testing.T) {
	registries := []string{
		"localhost:5000",
		"localhost:5001",
	}
	schemes := []Scheme{
		SchemeBasic,
		SchemeBearer,
	}
	keys := []string{
		"foo",
		"bar",
	}
	count := make([]int64, len(registries)*len(schemes)*len(keys))
	fetch := func(i int) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			time.Sleep(500 * time.Millisecond)
			atomic.AddInt64(&count[i], 1)
			return strconv.Itoa(i), nil
		}
	}

	ctx := context.Background()
	cache := NewCache()

	// first round of fetch
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for j := 0; j < len(count); j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				scheme := schemes[(i>>1)&1]
				key := keys[(i>>2)&1]
				got, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if err != nil {
					t.Errorf("concurrentCache.Set() error = %v", err)
				}
				if want := strconv.Itoa(i); got != want {
					t.Errorf("concurrentCache.Set() = %v, want %v", got, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < len(count); i++ {
		if got := count[i]; got != 1 {
			t.Errorf("fetch is called more than once: %d", got)
		}
	}

	// repeated fetch
	for i := 0; i < 10; i++ {
		for j := 0; j < len(count); j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				scheme := schemes[(i>>1)&1]
				key := keys[(i>>2)&1]
				got, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if err != nil {
					t.Errorf("concurrentCache.Set() error = %v", err)
				}
				if want := strconv.Itoa(i); got != want {
					t.Errorf("concurrentCache.Set() = %v, want %v", got, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < len(count); i++ {
		if got := count[i]; got != 2 {
			t.Errorf("fetch is called more than once: %d", got)
		}
	}
}

func Test_concurrentCache_Set_Fetch_Failure(t *testing.T) {
	registries := []string{
		"localhost:5000",
		"localhost:5001",
	}
	scheme := SchemeBearer
	keys := []string{
		"foo",
		"bar",
	}
	count := len(registries) * len(keys)

	ctx := context.Background()
	cache := NewCache()

	// first round of fetch
	fetch := func(i int) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			return "", errors.New(strconv.Itoa(i))
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for j := 0; j < count; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				key := keys[(i>>1)&1]
				_, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if want := strconv.Itoa(i); err == nil || err.Error() != want {
					t.Errorf("concurrentCache.Set() error = %v, wantErr %v", err, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < count; i++ {
		registry := registries[i&1]
		key := keys[(i>>1)&1]

		_, err := cache.GetScheme(ctx, registry)
		if want := errdef.ErrNotFound; err != want {
			t.Fatalf("concurrentCache.GetScheme() error = %v, wantErr %v", err, want)
		}

		_, err = cache.GetToken(ctx, registry, scheme, key)
		if want := errdef.ErrNotFound; err != want {
			t.Errorf("concurrentCache.GetToken() error = %v, wantErr %v", err, want)
		}
	}

	// repeated fetch
	fetch = func(i int) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			return strconv.Itoa(i), nil
		}
	}
	for i := 0; i < 10; i++ {
		for j := 0; j < count; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				registry := registries[i&1]
				key := keys[(i>>1)&1]
				got, err := cache.Set(ctx, registry, scheme, key, fetch(i))
				if err != nil {
					t.Errorf("concurrentCache.Set() error = %v", err)
				}
				if want := strconv.Itoa(i); got != want {
					t.Errorf("concurrentCache.Set() = %v, want %v", got, want)
				}
			}(j)
		}
	}
	wg.Wait()

	for i := 0; i < count; i++ {
		registry := registries[i&1]
		key := keys[(i>>1)&1]

		gotScheme, err := cache.GetScheme(ctx, registry)
		if err != nil {
			t.Fatalf("concurrentCache.GetScheme() error = %v", err)
		}
		if want := scheme; gotScheme != want {
			t.Errorf("concurrentCache.GetScheme() = %v, want %v", gotScheme, want)
		}

		gotToken, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Fatalf("concurrentCache.GetToken() error = %v", err)
		}
		if want := strconv.Itoa(i); gotToken != want {
			t.Errorf("concurrentCache.GetToken() = %v, want %v", gotToken, want)
		}
	}
}
