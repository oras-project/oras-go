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

// Package cache provides a cache layer for content stores.
package cache

import (
	"context"
	"io"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/internal/ioutil"
	"github.com/oras-project/oras-go/v3/registry"
)

// target wraps a ReadOnlyTarget with a caching layer.
type target struct {
	oras.ReadOnlyTarget
	cache content.Storage
}

// CacheReadOnlyTarget creates a new cached target.
// The returned target will first check the cache for content before
// fetching from the source. Fetched content is cached while being read.
//
// Note: The returned target implements only oras.ReadOnlyTarget. If the source
// implements additional interfaces (e.g., registry.ReferenceFetcher), those
// methods are also available through the returned target. However, type
// assertions for other interfaces beyond ReadOnlyTarget may fail since
// embedding an interface only promotes that interface's method set.
func CacheReadOnlyTarget(source oras.ReadOnlyTarget, cache content.Storage) oras.ReadOnlyTarget {
	t := &target{
		ReadOnlyTarget: source,
		cache:          cache,
	}
	if refFetcher, ok := source.(registry.ReferenceFetcher); ok {
		return &referenceTarget{
			target:           t,
			ReferenceFetcher: refFetcher,
		}
	}
	return t
}

// Fetch fetches the content identified by the descriptor.
// It first checks the cache, and if not found, fetches from the source
// while caching the content.
func (t *target) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	rc, err := t.cache.Fetch(ctx, desc)
	if err == nil {
		return rc, nil
	}

	rc, err = t.ReadOnlyTarget.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}

	return t.cacheReadCloser(ctx, rc, desc), nil
}

// cacheReadCloser wraps the reader to cache content while reading.
func (t *target) cacheReadCloser(ctx context.Context, rc io.ReadCloser, desc ocispec.Descriptor) io.ReadCloser {
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	var pushErr error
	go func() {
		defer wg.Done()
		pushErr = t.cache.Push(ctx, desc, pr)
		if pushErr != nil {
			pr.CloseWithError(pushErr)
		}
	}()

	closer := ioutil.CloserFunc(func() error {
		rcErr := rc.Close()
		if err := pw.Close(); err != nil {
			return err
		}
		wg.Wait()
		if pushErr != nil {
			return pushErr
		}
		return rcErr
	})

	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.TeeReader(rc, pw),
		Closer: closer,
	}
}

// Exists returns true if the described content exists.
// It checks both the cache and the source.
func (t *target) Exists(ctx context.Context, desc ocispec.Descriptor) (bool, error) {
	exists, err := t.cache.Exists(ctx, desc)
	if err == nil && exists {
		return true, nil
	}
	return t.ReadOnlyTarget.Exists(ctx, desc)
}

// referenceTarget extends target with ReferenceFetcher support.
type referenceTarget struct {
	*target
	registry.ReferenceFetcher
}

// FetchReference fetches the content identified by the reference.
// It first resolves the reference to a descriptor via a lightweight HEAD
// request, then serves from the local cache if available. On a cache miss,
// the content is fetched from the source and cached while being read.
//
// Trade-off: a cache miss requires one extra HEAD request (Resolve) compared
// to a direct FetchReference call. Cache hits avoid downloading any content
// body from the source.
func (t *referenceTarget) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	// Resolve via HEAD to get the descriptor without downloading content.
	desc, err := t.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	// Cache hit: serve from cache without issuing a GET to the source.
	if rc, err := t.cache.Fetch(ctx, desc); err == nil {
		return desc, rc, nil
	}

	// Cache miss: fetch from source and cache while reading.
	// Use the descriptor returned by FetchReference (not Resolve) to handle
	// the case where the tag was updated between the two calls.
	fetchedDesc, rc, err := t.ReferenceFetcher.FetchReference(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return fetchedDesc, t.cacheReadCloser(ctx, rc, fetchedDesc), nil
}
