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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/status"
)

const defaultConcurrencyLimit = int64(3)

type CopyOptions struct {
	CopyGraphOptions
	ManifestFilter func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) (ocispec.Descriptor, error)
}

type CopyGraphOptions struct {
	Concurrency        int64
	PreCopyHandler     func(ctx context.Context, desc ocispec.Descriptor) error
	PostCopyHandler    func(ctx context.Context, desc ocispec.Descriptor) error
	SkippedCopyHandler func(ctx context.Context, desc ocispec.Descriptor) error
}

// Copy copies a rooted directed acyclic graph (DAG) with the tagged root node
// in the source Target to the destination Target.
// The destination reference will be the same as the source reference if the
// destination reference is left blank.
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

	root, err := src.Resolve(ctx, srcRef)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	// TODO: manifest filter? 1. media type filter 2. platform filter
	if opts.ManifestFilter != nil {
		filtered, err := opts.ManifestFilter(ctx, src, root)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		root = filtered
	}

	if err := CopyGraph(ctx, src, dst, root, opts.CopyGraphOptions); err != nil {
		return ocispec.Descriptor{}, err
	}

	if err := dst.Tag(ctx, root, dstRef); err != nil {
		return ocispec.Descriptor{}, err
	}

	return root, nil
}

// CopyGraph copies a rooted directed acyclic graph (DAG) from the source CAS to
// the destination CAS.
func CopyGraph(ctx context.Context, src, dst content.Storage, root ocispec.Descriptor, opts CopyGraphOptions) error {
	// use caching proxy on non-leaf nodes
	proxy := cas.NewProxy(src, cas.NewMemory())

	// track content status
	tracker := status.NewTracker()

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
			if opts.SkippedCopyHandler != nil {
				if err := opts.SkippedCopyHandler(ctx, desc); err != nil {
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
			return nil, handleCopyNode(ctx, src, dst, desc, opts)
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
		return nil, handleCopyNode(ctx, proxy.Cache, dst, desc, opts)
	})

	var concurrency = defaultConcurrencyLimit
	if opts.Concurrency > 0 {
		concurrency = opts.Concurrency
	}
	// traverse the graph
	return graph.Dispatch(ctx, preHandler, postHandler, semaphore.NewWeighted(concurrency), root)
}

func handleCopyNode(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor, opts CopyGraphOptions) error {
	if opts.PreCopyHandler != nil {
		if err := opts.PreCopyHandler(ctx, desc); err != nil {
			return err
		}
	}

	if err := copyNode(ctx, src, dst, desc); err != nil {
		return err
	}

	if opts.PostCopyHandler != nil {
		if err := opts.PostCopyHandler(ctx, desc); err != nil {
			return err
		}
	}
	return nil
}

// copyNode copies a single content from the source CAS to the destination CAS.
func copyNode(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor) error {
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
