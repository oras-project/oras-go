package oras

import (
	"context"

	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

type resolver struct {
	ref        string
	desc       ocispec.Descriptor
	fetcher    remotes.FetcherFunc
	pusher     remotes.PusherFunc
	discoverer DiscoverFunc
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
	return ref, r.desc, nil
}

// TODO
func AdaptOCISpec(desc ocispec.Descriptor) v1.Descriptor {
	next := v1.Descriptor{}
	next.Annotations = desc.Annotations
	next.Digest = desc.Digest
	next.MediaType = desc.MediaType
	next.Size = desc.Size
	next.URLs = desc.URLs

	return next
}
