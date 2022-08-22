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
func Resolve(ctx context.Context, target Target, reference string, opts ResolveOptions) (ocispec.Descriptor, error) {
	if opts.TargetPlatform == nil {
		return target.Resolve(ctx, reference)
	}

	proxy := cas.NewProxy(target, cas.NewMemory())
	return resolve(ctx, target, proxy, reference, opts)
}

// resolve resolves a descriptor with provided reference from the target, with
// specified caching.
func resolve(ctx context.Context, target Target, proxy *cas.Proxy, reference string, opts ResolveOptions) (ocispec.Descriptor, error) {
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
			err = proxy.Cache.Push(ctx, desc, rc)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
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

// DefaultFetchManifestOptions provides the default FetchManifestOptions.
var DefaultFetchManifestOptions FetchManifestOptions

// FetchManifestOptions contains parameters for oras.FetchManifest.
type FetchManifestOptions struct {
	// ResolveOptions contains parameters for resolving reference.
	ResolveOptions
}

// FetchManifest fetches the manifest identified by the reference.
func FetchManifest(ctx context.Context, target Target, reference string, opts FetchManifestOptions) (ocispec.Descriptor, []byte, error) {
	if opts.TargetPlatform == nil {
		return fetchContent(ctx, target, reference)
	}

	proxy := cas.NewProxy(target, cas.NewMemory())
	desc, err := resolve(ctx, target, proxy, reference, opts.ResolveOptions)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	bytes, err := content.FetchAll(ctx, proxy, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, bytes, nil
}

// FetchBlob fetches the blob identified by the reference.
func FetchBlob(ctx context.Context, target Target, ref string) (ocispec.Descriptor, []byte, error) {
	if repo, ok := target.(registry.Repository); ok {
		return fetchContent(ctx, repo.Blobs(), ref)
	}
	return fetchContent(ctx, target, ref)
}

// contentResolver provides content fetching and reference resolving.
type contentResolver interface {
	content.Fetcher
	content.Resolver
}

// fetchContent fetches the content identified by the reference.
func fetchContent(ctx context.Context, resolver contentResolver, reference string) (ocispec.Descriptor, []byte, error) {
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

		bytes, err = content.ReadAll(rc, desc)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}

		return desc, bytes, nil
	}

	desc, err = resolver.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	bytes, err = content.FetchAll(ctx, resolver, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return desc, bytes, nil
}
