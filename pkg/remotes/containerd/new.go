package remotes

import (
	"context"

	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasRemotes "oras.land/oras-go/pkg/remotes"
)

func NewDiscoverFetchResolver(ctx context.Context, registry *orasRemotes.Registry, reference string) (remotes.Resolver, error) {
	funcs := registry.AsFunctions()

	return DiscoverFetch(ctx, funcs.Fetcher(), funcs.Resolver(), funcs.Discoverer(), reference)
}

func NewPushPullResolver(ctx context.Context, registry *orasRemotes.Registry, desc ocispec.Descriptor) (remotes.Resolver, error) {
	funcs := registry.AsFunctions()

	return PushPull(ctx, funcs.Fetcher(), containerdPusher(funcs), funcs.Resolver(), funcs.Discoverer(), desc)
}
