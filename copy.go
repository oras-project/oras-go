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
	"encoding/json"
	"errors"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/platform"
	"oras.land/oras-go/v2/internal/registryutil"
	"oras.land/oras-go/v2/internal/status"
	"oras.land/oras-go/v2/registry"
)

var (
	// DefaultCopyOptions provides the default CopyOptions.
	DefaultCopyOptions = CopyOptions{
		CopyGraphOptions: DefaultCopyGraphOptions,
	}
	// DefaultCopyGraphOptions provides the default CopyGraphOptions.
	DefaultCopyGraphOptions = CopyGraphOptions{
		Concurrency: 3, // This value is consistent with dockerd and containerd.
	}
)

// CopyOptions contains parameters for oras.Copy.
type CopyOptions struct {
	CopyGraphOptions
	// MapRoot maps the resolved root node to a desired root node for copy.
	// When MapRoot is provided, the descriptor resolved from the source
	// reference will be passed to MapRoot, and the mapped descriptor will be
	// used as the root node for copy.
	MapRoot func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (ocispec.Descriptor, error)
}

// getPlatformFromConfig returns a platform object which is made up from the
// fields in config blob.
func getPlatformFromConfig(ctx context.Context, src content.Storage, desc ocispec.Descriptor, targetConfigMediaType string) (*ocispec.Platform, error) {
	if desc.MediaType != targetConfigMediaType {
		return nil, fmt.Errorf("mismatch MediaType %s: expect %s", desc.MediaType, targetConfigMediaType)
	}

	rc, err := src.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var platform ocispec.Platform
	if err = json.NewDecoder(rc).Decode(&platform); err != nil && err != io.EOF {
		return nil, err
	}

	return &platform, nil
}

// selectPlatform implements platform filter and returns the descriptor of the
// first matched manifest if the root is a manifest list. If the root is a
// manifest, then return the root descriptor if platform matches.
func selectPlatform(ctx context.Context, src content.Storage, root ocispec.Descriptor, p *ocispec.Platform) (ocispec.Descriptor, error) {
	switch root.MediaType {
	case docker.MediaTypeManifestList, ocispec.MediaTypeImageIndex:
		manifests, err := content.Successors(ctx, src, root)
		if err != nil {
			return ocispec.Descriptor{}, err
		}

		// platform filter
		for _, m := range manifests {
			if platform.Match(m.Platform, p) {
				return m, nil
			}
		}
		return ocispec.Descriptor{}, errdef.ErrNotFound
	case docker.MediaTypeManifest, ocispec.MediaTypeImageManifest:
		descs, err := content.Successors(ctx, src, root)
		if err != nil {
			return ocispec.Descriptor{}, err
		}

		configMediaType := docker.MediaTypeConfig
		if root.MediaType == ocispec.MediaTypeImageManifest {
			configMediaType = ocispec.MediaTypeImageConfig
		}

		cfgPlatform, err := getPlatformFromConfig(ctx, src, descs[0], configMediaType)
		if err != nil {
			return ocispec.Descriptor{}, err
		}

		if platform.Match(cfgPlatform, p) {
			return root, nil
		}
		return ocispec.Descriptor{}, errdef.ErrNotFound
	default:
		return ocispec.Descriptor{}, fmt.Errorf("%s: %s: %w", root.Digest, root.MediaType, errdef.ErrUnsupported)
	}
}

// WithTargetPlatform configures opts.MapRoot to select the manifest whose
// platform matches the given platform. When MapRoot is provided, the platform
// selection will be applied on the mapped root node.
// - If the root node is a manifest, it will remain the same if platform
// matches, otherwise ErrNotFound will be returned.
// - If the root node is a manifest list, it will be mapped to the first
// matching manifest if exists, otherwise ErrNotFound will be returned.
// - Otherwise ErrUnsupported will be returned.
func (opts *CopyOptions) WithTargetPlatform(p *ocispec.Platform) {
	mapRoot := opts.MapRoot
	opts.MapRoot = func(ctx context.Context, src content.Storage, root ocispec.Descriptor) (desc ocispec.Descriptor, err error) {
		if mapRoot != nil {
			if root, err = mapRoot(ctx, src, root); err != nil {
				return ocispec.Descriptor{}, err
			}
		}
		return selectPlatform(ctx, src, root, p)
	}
}

// CopyGraphOptions contains parameters for oras.CopyGraph.
type CopyGraphOptions struct {
	// Concurrency limits the maximum number of concurrent copy tasks.
	// If Concurrency is not specified, or the specified value is less
	// or equal to 0, the concurrency limit will be considered as infinity.
	Concurrency int64
	// PreCopy handles the current descriptor before copying it.
	PreCopy func(ctx context.Context, desc ocispec.Descriptor) error
	// PostCopy handles the current descriptor after copying it.
	PostCopy func(ctx context.Context, desc ocispec.Descriptor) error
	// OnCopySkipped will be called when the sub-DAG rooted by the current node
	// is skipped.
	OnCopySkipped func(ctx context.Context, desc ocispec.Descriptor) error
	// FindSuccessors finds the successors of the current node.
	// fetcher provides cached access to the source storage, and is suitable
	// for fetching non-leaf nodes like manifests. Since anything fetched from
	// fetcher will be cached in the memory, it is recommended to use original
	// source storage to fetch large blobs.
	// If FindSuccessors is nil, content.Successors will be used.
	FindSuccessors func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error)
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

	// use caching proxy on non-leaf nodes
	proxy := cas.NewProxy(src, cas.NewMemory())
	root, err := resolveRoot(ctx, src, srcRef, proxy)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if opts.MapRoot != nil {
		proxy.StopCaching = true
		root, err = opts.MapRoot(ctx, proxy, root)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		proxy.StopCaching = false
	}

	if err := prepareCopy(ctx, dst, dstRef, proxy, root, &opts); err != nil {
		return ocispec.Descriptor{}, err
	}

	if err := copyGraph(ctx, src, dst, proxy, root, opts.CopyGraphOptions); err != nil {
		return ocispec.Descriptor{}, err
	}

	return root, nil
}

// CopyGraph copies a rooted directed acyclic graph (DAG) from the source CAS to
// the destination CAS.
func CopyGraph(ctx context.Context, src, dst content.Storage, root ocispec.Descriptor, opts CopyGraphOptions) error {
	// use caching proxy on non-leaf nodes
	proxy := cas.NewProxy(src, cas.NewMemory())
	return copyGraph(ctx, src, dst, proxy, root, opts)
}

// copyGraph copies a rooted directed acyclic graph (DAG) from the source CAS to
// the destination CAS with specified caching.
func copyGraph(ctx context.Context, src, dst content.Storage, proxy *cas.Proxy, root ocispec.Descriptor, opts CopyGraphOptions) error {
	// track content status
	tracker := status.NewTracker()

	// if FindSuccessors is not provided, use the default one
	if opts.FindSuccessors == nil {
		opts.FindSuccessors = content.Successors
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
			if opts.OnCopySkipped != nil {
				if err := opts.OnCopySkipped(ctx, desc); err != nil {
					return nil, err
				}
			}
			return nil, graph.ErrSkipDesc
		}

		// find successors while non-leaf nodes will be fetched and cached
		return opts.FindSuccessors(ctx, proxy, desc)
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
			return nil, copyNode(ctx, src, dst, desc, opts)
		}

		// for non-leaf nodes, wait for its successors to complete
		successors, err := opts.FindSuccessors(ctx, proxy, desc)
		if err != nil {
			return nil, err
		}
		for _, node := range successors {
			done, committed := tracker.TryCommit(node)
			if committed {
				return nil, fmt.Errorf("%s: %s: successor not committed", desc.Digest, node.Digest)
			}
			select {
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, copyNode(ctx, proxy.Cache, dst, desc, opts)
	})

	var limiter *semaphore.Weighted
	if opts.Concurrency > 0 {
		limiter = semaphore.NewWeighted(opts.Concurrency)
	}
	// traverse the graph
	return graph.Dispatch(ctx, preHandler, postHandler, limiter, root)
}

// doCopyNode copies a single content from the source CAS to the destination CAS.
func doCopyNode(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor) error {
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

// copyNode copies a single content from the source CAS to the destination CAS,
// and apply the given options.
func copyNode(ctx context.Context, src, dst content.Storage, desc ocispec.Descriptor, opts CopyGraphOptions) error {
	if opts.PreCopy != nil {
		if err := opts.PreCopy(ctx, desc); err != nil {
			if err == graph.ErrSkipDesc {
				return nil
			}
			return err
		}
	}

	if err := doCopyNode(ctx, src, dst, desc); err != nil {
		return err
	}

	if opts.PostCopy != nil {
		return opts.PostCopy(ctx, desc)
	}
	return nil
}

// copyCachedNodeWithReference copies a single content with a reference from the
// source cache to the destination ReferencePusher.
func copyCachedNodeWithReference(ctx context.Context, src *cas.Proxy, dst registry.ReferencePusher, desc ocispec.Descriptor, dstRef string) error {
	rc, err := src.FetchCached(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	err = dst.PushReference(ctx, desc, rc, dstRef)
	if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}

// resolveRoot resolves the source reference to the root node.
func resolveRoot(ctx context.Context, src Target, srcRef string, proxy *cas.Proxy) (ocispec.Descriptor, error) {
	refFetcher, ok := src.(registry.ReferenceFetcher)
	if !ok {
		return src.Resolve(ctx, srcRef)
	}

	// optimize performance for ReferenceFetcher targets
	refProxy := &registryutil.Proxy{
		ReferenceFetcher: refFetcher,
		Proxy:            proxy,
	}
	root, rc, err := refProxy.FetchReference(ctx, srcRef)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer rc.Close()
	// cache root if it is a non-leaf node
	fetcher := content.FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		if descriptor.EqualOCI(target, root) {
			return rc, nil
		}
		return nil, errors.New("fetching only root node expected")
	})
	if _, err = content.Successors(ctx, fetcher, root); err != nil {
		return ocispec.Descriptor{}, err
	}

	// TODO: optimize special case where root is a leaf node (i.e. a blob)
	//       and dst is a ReferencePusher.
	return root, nil
}

// prepareCopy prepares the hooks for copy.
func prepareCopy(ctx context.Context, dst Target, dstRef string, proxy *cas.Proxy, root ocispec.Descriptor, opts *CopyOptions) error {
	if refPusher, ok := dst.(registry.ReferencePusher); ok {
		// optimize performance for ReferencePusher targets
		preCopy := opts.PreCopy
		opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			if preCopy != nil {
				if err := preCopy(ctx, desc); err != nil {
					return err
				}
			}
			if !descriptor.EqualOCI(desc, root) {
				// for non-root node, do nothing
				return nil
			}

			// for root node, prepare optimized copy
			if err := copyCachedNodeWithReference(ctx, proxy, refPusher, desc, dstRef); err != nil {
				return err
			}
			if opts.PostCopy != nil {
				if err := opts.PostCopy(ctx, desc); err != nil {
					return err
				}
			}
			// skip the regular copy workflow
			return graph.ErrSkipDesc
		}
	} else {
		postCopy := opts.PostCopy
		opts.PostCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
			if descriptor.EqualOCI(desc, root) {
				// for root node, tag it after copying it
				if err := dst.Tag(ctx, root, dstRef); err != nil {
					return err
				}
			}
			if postCopy != nil {
				return postCopy(ctx, desc)
			}
			return nil
		}
	}

	onCopySkipped := opts.OnCopySkipped
	opts.OnCopySkipped = func(ctx context.Context, desc ocispec.Descriptor) error {
		if onCopySkipped != nil {
			if err := onCopySkipped(ctx, desc); err != nil {
				return err
			}
		}
		if !descriptor.EqualOCI(desc, root) {
			return nil
		}
		// enforce tagging when root is skipped
		if refPusher, ok := dst.(registry.ReferencePusher); ok {
			return copyCachedNodeWithReference(ctx, proxy, refPusher, desc, dstRef)
		}
		return dst.Tag(ctx, root, dstRef)
	}

	return nil
}
