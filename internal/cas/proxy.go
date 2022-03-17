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

package cas

import (
	"context"
	"io"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/ioutil"
)

// Proxy is a caching proxy for the storage.
// The first fetch call of a described content will read from the remote and
// cache the fetched content.
// The subsequent fetch call will read from the local cache.
type Proxy struct {
	content.Storage
	Cache content.Storage
}

// NewProxy creates a proxy for the `base` storage, using the `cache` storage as
// the cache.
func NewProxy(base, cache content.Storage) *Proxy {
	return &Proxy{
		Storage: base,
		Cache:   cache,
	}
}

// Fetch fetches the content identified by the descriptor.
func (p *Proxy) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	rc, err := p.Cache.Fetch(ctx, target)
	if err == nil {
		return rc, nil
	}

	rc, err = p.Storage.Fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	var pushErr error
	go func() {
		defer wg.Done()
		pushErr = p.Cache.Push(ctx, target, pr)
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
	}, nil
}

// Exists returns true if the described content exists.
func (p *Proxy) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	exists, err := p.Cache.Exists(ctx, target)
	if err == nil && exists {
		return true, nil
	}
	return p.Storage.Exists(ctx, target)
}
