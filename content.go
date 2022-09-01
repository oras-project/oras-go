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
	return resolve(ctx, target, nil, reference, opts)
}

// resolve resolves a descriptor with provided reference from the target, with
// specified caching.
func resolve(ctx context.Context, target ReadOnlyTarget, proxy *cas.Proxy, reference string, opts ResolveOptions) (ocispec.Descriptor, error) {
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
			if proxy == nil {
				proxy = cas.NewProxy(target, cas.NewMemory())
			}
			if err := proxy.Cache.Push(ctx, desc, rc); err != nil {
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

// DefaultFetchOptions provides the default FetchOptions.
var DefaultFetchOptions = FetchOptions{}

// FetchOptions contains parameters for oras.Fetch.
type FetchOptions struct {
	// ResolveOptions contains parameters for resolving reference.
	ResolveOptions
}

// Fetch fetches the content identified by the reference.
func Fetch(ctx context.Context, target ReadOnlyTarget, reference string, opts FetchOptions) (ocispec.Descriptor, io.ReadCloser, error) {
	if opts.TargetPlatform == nil {
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

	proxy := cas.NewProxy(target, cas.NewMemory())
	desc, err := resolve(ctx, target, proxy, reference, opts.ResolveOptions)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	// if the content exists in cache, fetch it from cache
	// otherwise fetch without caching
	proxy.StopCaching = true
	rc, err := proxy.Fetch(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, rc, nil
}

// DefaultFetchBytesOptions provides the default FetchBytesOptions.
var DefaultFetchBytesOptions = FetchBytesOptions{
	SizeLimit: int64(1 << 22), // 4 MiB
}

// FetchBytesOptions contains parameters for oras.FetchBytes.
type FetchBytesOptions struct {
	// FetchOptions contains parameters for fetching content.
	FetchOptions
	// SizeLimit limits the max size of the fetched content.
	// If SizeLimit is not specified, or the specified value is less than or
	// equal to 0, it will be considered as infinity.
	SizeLimit int64
}

// FetchBytes fetches the content bytes identified by the reference.
func FetchBytes(ctx context.Context, target ReadOnlyTarget, reference string, opts FetchBytesOptions) (ocispec.Descriptor, []byte, error) {
	desc, rc, err := Fetch(ctx, target, reference, opts.FetchOptions)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer rc.Close()

	if opts.SizeLimit > 0 && desc.Size > opts.SizeLimit {
		return ocispec.Descriptor{}, nil, fmt.Errorf(
			"content size %v exceeds max size limit %v: %w",
			desc.Size,
			opts.SizeLimit,
			errdef.ErrSizeExceedsLimit)
	}
	bytes, err := content.ReadAll(rc, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return desc, bytes, nil
}
