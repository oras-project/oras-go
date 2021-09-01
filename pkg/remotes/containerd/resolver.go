package remotes

import (
	"context"
	"fmt"

	orasRemotes "oras.land/oras-go/pkg/remotes"

	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type resolver struct {
	ref        string
	desc       ocispec.Descriptor
	fetcher    remotes.FetcherFunc
	pusher     remotes.PusherFunc
	resolver   ResolverFunc
	discoverer orasRemotes.DiscoverFunc
}

// Resolve creates a resolver that can resolve, fetch, and discover
func DiscoverFetch(ctx context.Context, fetcher remotes.FetcherFunc, resolverfunc ResolverFunc, discoverer orasRemotes.DiscoverFunc, reference string) (remotes.Resolver, error) {
	_, err := orasRemotes.ValidateReference(reference)
	if err == nil {
		return resolver{
			ref:        reference,
			resolver:   resolverfunc,
			fetcher:    fetcher,
			discoverer: discoverer,
			pusher:     nil}, nil
	}

	return nil, err
}

// PushPull creates a resolver that can do everything above and also push to the registry as well
func PushPull(ctx context.Context, fetcher remotes.FetcherFunc, pusher remotes.PusherFunc, resolverfunc ResolverFunc, discoverer orasRemotes.DiscoverFunc, desc ocispec.Descriptor) (remotes.Resolver, error) {
	if desc.Digest != "" {
		return resolver{
			desc:       desc,
			resolver:   resolverfunc,
			fetcher:    fetcher,
			discoverer: discoverer,
			pusher:     pusher,
		}, nil
	}

	return nil, fmt.Errorf("invalid digest")
}

// Resolve attempts to resolve the reference into a name and descriptor.
//
// The argument `ref` should be a scheme-less URI representing the remote.
// Structurally, it has a host and path. The "host" can be used to directly
// reference a specific host or be matched against a specific handler.
//
// The returned name should be used to identify the referenced entity.
// Dependending on the remote namespace, this may be immutable or mutable.
// While the name may differ from ref, it should itself be a valid ref.
//
// If the resolution fails, an error will be returned.
func (r resolver) Resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	if r.resolver != nil {
		return r.resolver(ctx, ref)
	}

	return ref, r.desc, nil
}

type ResolverFunc = func(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error)
