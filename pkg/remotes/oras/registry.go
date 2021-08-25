package oras

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Registry is an opaqueish type which represents an OCI V2 API registry
// Note: It is exported intentionally
type Registry struct {
	client    *http.Client
	namespace string
}

// Resolve creates a resolver that can resolve, fetch, and discover
func (r *Registry) Resolve(ctx context.Context, reference string) (remotes.Resolver, error) {
	if validateReference(reference) {
		return resolver{
			ref:        reference,
			resolver:   r.resolve,
			fetcher:    r.fetch,
			discoverer: r.discover,
			pusher:     nil}, nil
	}

	return nil, fmt.Errorf("invalid reference")
}

// PushPull creates a resolver that can do everything above and also push to the registry as well
func (r *Registry) PushPull(ctx context.Context, desc ocispec.Descriptor) (remotes.Resolver, error) {
	if desc.Digest != "" {
		return resolver{
			desc:       desc,
			resolver:   r.resolve,
			fetcher:    r.fetch,
			discoverer: r.discover,
			pusher:     r.push,
		}, nil
	}

	return nil, fmt.Errorf("invalid digest")
}

func (r *Registry) resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	// TODO
	// Resolve a ref to get a desc (Can I check disk for the OCIStore?)
	// Use a desc to discover a manifest
	// Use the manifest to fetch signature artifacts
	// Where to store? OCIStore?
	return "", ocispec.Descriptor{}, fmt.Errorf("resolve api has not been implemented")
}

func (r *Registry) fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, fmt.Errorf("fetch api has not been implemented") // TODO
}

func (r *Registry) discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error) {
	return nil, fmt.Errorf("discover api has not been implemented") // TODO
}

func (r *Registry) push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return nil, fmt.Errorf("push api has not been implemented") // TODO
}
