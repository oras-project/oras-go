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

package oras

import (
	"context"
	"errors"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/status"
	"oras.land/oras-go/v2/registry"
)

// CopyOptions contains parameters for oras.Copy.
type CopyOptions struct {
	CopyGraphOptions
	// MapRoot maps the resolved root node to a desired root node for copy.
	MapRoot func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error)
}

// CopyGraphOptions contains parameters for oras.CopyGraph.
type CopyGraphOptions struct {
	// Concurrency limits the maximum number of concurrent copy tasks.
	// If Concurrency is not specified, or the specified value is less
	// or equal to 0, the concurrency limit will be considered as infinity.
	Concurrency int64
	// CopySkipped will be called when the sub-DAG rooted by the current node is
	// skipped.
	CopySkipped func(ctx context.Context, desc ocispec.Descriptor) error
	// CopyNode copies a single content from the source CAS to the destination CAS.
	// If CopyNode is not provided, a default function will be used.
	CopyNode func(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor) error
}

var DefaultCopyGraphOptions = CopyGraphOptions{
	Concurrency: 3, // This value is consistent with dockerd and containerd.
}

// Copy copies a rooted directed acyclic graph (DAG) with the tagged root node
// in the source Target to the destination Target.
// The destination reference will be the same as the source reference if the
// destination reference is left blank.
// When MapRoot option is provided, the descriptor resolved from the source
// reference will be passed to the MapRoot, and the mapped descriptor will
// be used as the root node for copy.
// Returns the descriptor of the root node on successful copy.
func Copy(ctx context.Context, src Target, srcRef string, dst Target, dstRef string, opts CopyOptions) (ocispec.Descriptor, error) {
	if src == nil {
		return ocispec.Descriptor{}, errors.New("nil source target")
	}
	if dst == nil {
		return ocispec.Descriptor{}, errors.New("nil destination target")
	}
	if dstRef == "" {
		dstRef = srcRef
	}

	proxy := cas.NewProxy(src, cas.NewMemory())
	var root ocispec.Descriptor
	var err error
	if fetcher, ok := src.(registry.ReferenceFetcher); ok {
		var rc io.ReadCloser
		root, rc, err = fetcher.FetchReference(ctx, srcRef)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		err = proxy.Cache.Push(ctx, root, rc)
	} else {
		root, err = src.Resolve(ctx, srcRef)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		_, err = proxy.Fetch(ctx, root)
	}
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if opts.MapRoot != nil {
		mapped, err := opts.MapRoot(ctx, proxy, root)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		root = mapped
	}

	copyNode := opts.CopyNode
	if copyNode == nil {
		copyNode = CopyNode
	}
	opts.CopyNode = func(ctx context.Context, srcStorage, dstStorage content.Storage, desc ocispec.Descriptor) error {
		if desc.Digest != root.Digest {
			// non-root
			return copyNode(ctx, srcStorage, dstStorage, desc)
		}

		rc, err := srcStorage.Fetch(ctx, desc)
		if err != nil {
			return err
		}

		// dst supports reference pusher
		if pusher, ok := dst.(registry.ReferencePusher); ok {
			return pusher.PushReference(ctx, desc, rc, dstRef)
		}

		if err := copyNode(ctx, srcStorage, dstStorage, desc); err != nil {
			return err
		}
		return dst.Tag(ctx, desc, dstRef)
	}

	return root, CopyGraph(ctx, proxy, dst, root, opts.CopyGraphOptions)
}

// CopyGraph copies a rooted directed acyclic graph (DAG) from the source CAS to
// the destination CAS.
func CopyGraph(ctx context.Context, src, dst content.Storage, root ocispec.Descriptor, opts CopyGraphOptions) error {
	// use caching proxy on non-leaf nodes
	proxy := cas.NewProxy(src, cas.NewMemory())

	// track content status
	tracker := status.NewTracker()

	if opts.CopyNode == nil {
		opts.CopyNode = CopyNode
	}

	// prepare pre-handler
	preHandler := graph.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		// skip the descriptor if other go routine is working on it
		done, committed := tracker.TryCommit(desc)
		if !committed {
			return nil, graph.ErrSkipDesc
		}

		// skip if a rooted sub-DAG exists
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			return nil, err
		}
		if exists {
			// mark the content as done
			close(done)
			if opts.CopySkipped != nil {
				if err := opts.CopySkipped(ctx, desc); err != nil {
					return nil, err
				}
			}
			return nil, graph.ErrSkipDesc
		}

		// find down edges while non-leaf nodes will be fetched and cached
		return content.DownEdges(ctx, proxy, desc)
	})

	// prepare post-handler
	postHandler := graph.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) (_ []ocispec.Descriptor, err error) {
		defer func() {
			if err == nil {
				// mark the content as done on success
				done, _ := tracker.TryCommit(desc)
				close(done)
			}
		}()

		// leaf nodes does not exist in the cache.
		// copy them directly.
		exists, err := proxy.Cache.Exists(ctx, desc)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, opts.CopyNode(ctx, src, dst, desc)
		}

		// for non-leaf nodes, wait for its down edges to complete
		downEdges, err := content.DownEdges(ctx, proxy, desc)
		if err != nil {
			return nil, err
		}
		for _, node := range downEdges {
			done, committed := tracker.TryCommit(node)
			if committed {
				return nil, fmt.Errorf("%s: %s: down edge not committed", desc.Digest, node.Digest)
			}
			select {
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, opts.CopyNode(ctx, proxy.Cache, dst, desc)
	})

	var limiter *semaphore.Weighted
	if opts.Concurrency > 0 {
		limiter = semaphore.NewWeighted(opts.Concurrency)
	}
	// traverse the graph
	return graph.Dispatch(ctx, preHandler, postHandler, limiter, root)
}

// CopyNode copies a single content from the source CAS to the destination CAS.
func CopyNode(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor) error {
	rc, err := src.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()
	err = dst.Push(ctx, desc, rc)
	if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}
