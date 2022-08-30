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
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/platform"
	"oras.land/oras-go/v2/registry"
)

// defaultSizeLimit defines the default size limit for fetching content.
const defaultSizeLimit = 1 << 22 // 4 MiB

// Tag tags the descriptor identified by src with dst.
func Tag(ctx context.Context, target Target, src, dst string) error {
	if refTagger, ok := target.(registry.ReferenceTagger); ok {
		return refTagger.TagReference(ctx, src, dst)
	}
	refFetcher, okFetch := target.(registry.ReferenceFetcher)
	refPusher, okPush := target.(registry.ReferencePusher)
	if okFetch && okPush {
		desc, rc, err := refFetcher.FetchReference(ctx, src)
		if err != nil {
			return err
		}
		defer rc.Close()
		return refPusher.PushReference(ctx, desc, rc, dst)
	}
	desc, err := target.Resolve(ctx, src)
	if err != nil {
		return err
	}
	return target.Tag(ctx, desc, dst)
}

// DefaultResolveOptions provides the default ResolveOptions.
var DefaultResolveOptions ResolveOptions

// ResolveOptions contains parameters for oras.Resolve.
type ResolveOptions struct {
	// TargetPlatform ensures the resolved content matches the target platform
	// if the node is a manifest, or selects the first resolved content that
	// matches the target platform if the node is a manifest list.
	TargetPlatform *ocispec.Platform
}

// Resolve resolves a descriptor with provided reference from the target.
func Resolve(ctx context.Context, target ReadOnlyTarget, reference string, opts ResolveOptions) (ocispec.Descriptor, error) {
	if opts.TargetPlatform == nil {
		return target.Resolve(ctx, reference)
	}

	proxy := cas.NewProxy(target, cas.NewMemory())
	return resolve(ctx, target, proxy, reference, opts)
}

// resolve resolves a descriptor with provided reference from the target, with
// specified caching.
func resolve(ctx context.Context, target ReadOnlyTarget, proxy *cas.Proxy, reference string, opts ResolveOptions) (ocispec.Descriptor, error) {
	if opts.TargetPlatform == nil {
		return target.Resolve(ctx, reference)
	}

	if refFetcher, ok := target.(registry.ReferenceFetcher); ok {
		// optimize performance for ReferenceFetcher targets
		desc, rc, err := refFetcher.FetchReference(ctx, reference)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		defer rc.Close()

		switch desc.MediaType {
		case docker.MediaTypeManifestList, ocispec.MediaTypeImageIndex,
			docker.MediaTypeManifest, ocispec.MediaTypeImageManifest:
			// cache the fetched content
			err = proxy.Cache.Push(ctx, desc, rc)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
			// stop caching as SelectManifest may fetch a config blob
			proxy.StopCaching = true
			return platform.SelectManifest(ctx, proxy, desc, opts.TargetPlatform)
		default:
			return ocispec.Descriptor{}, fmt.Errorf("%s: %s: %w", desc.Digest, desc.MediaType, errdef.ErrUnsupported)
		}
	}

	desc, err := target.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return platform.SelectManifest(ctx, target, desc, opts.TargetPlatform)
}

// Fetch fetches the content identified by the reference.
func Fetch(ctx context.Context, target ReadOnlyTarget, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if refFetcher, ok := target.(registry.ReferenceFetcher); ok {
		return refFetcher.FetchReference(ctx, reference)
	}

	desc, err := target.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	rc, err := target.Fetch(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, rc, nil
}

// DefaultFetchContentOptions provides the default FetchContentOptions.
var DefaultFetchContentOptions = FetchContentOptions{
	SizeLimit: int64(defaultSizeLimit),
}

// FetchContentOptions contains parameters for oras.FetchContent.
type FetchContentOptions struct {
	// ResolveOptions contains parameters for resolving reference.
	ResolveOptions
	// SizeLimit limits the max size of the fetched content.
	SizeLimit int64
}

// FetchContent fetches the content bytes identified by the reference.
func FetchContent(ctx context.Context, target ReadOnlyTarget, reference string, opts FetchContentOptions) (ocispec.Descriptor, []byte, error) {
	if opts.TargetPlatform == nil {
		return fetchContent(ctx, target, reference, opts.SizeLimit)
	}

	proxy := cas.NewProxy(target, cas.NewMemory())
	desc, err := resolve(ctx, target, proxy, reference, opts.ResolveOptions)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	// if the content exists in cache, fetch it from cache
	// otherwise fetch without caching
	proxy.StopCaching = true
	bytes, err := content.FetchAllWithLimit(ctx, target, desc, opts.SizeLimit)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, bytes, nil
}

// fetchContent fetches the content bytes identified by the reference.
func fetchContent(ctx context.Context, resolver ReadOnlyTarget, reference string, limit int64) (ocispec.Descriptor, []byte, error) {
	var desc ocispec.Descriptor
	var rc io.ReadCloser
	var err error
	var bytes []byte

	if refFetcher, ok := resolver.(registry.ReferenceFetcher); ok {
		desc, rc, err = refFetcher.FetchReference(ctx, reference)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		defer rc.Close()

		if limit > 0 {
			if desc.Size > limit {
				return ocispec.Descriptor{}, nil, fmt.Errorf(
					"content size %v exceeds max size limit %v: %w",
					desc.Size,
					limit,
					content.ErrSizeExceedLimit)
			}
			bytes, err = content.ReadAll(io.LimitReader(rc, limit), desc)
		} else {
			bytes, err = content.ReadAll(rc, desc)
		}
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		return desc, bytes, nil
	}

	desc, err = resolver.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	bytes, err = content.FetchAllWithLimit(ctx, resolver, desc, limit)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, bytes, nil
}
