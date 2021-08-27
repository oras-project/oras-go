package remotes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Registry is an opaqueish type which represents an OCI V2 API registry
// Note: It is exported intentionally
type Registry struct {
	client    *http.Client
	host      string
	namespace string
	ref       string
	manifest  *ocispec.Manifest
}

type reference {
	host string 
	ns string 
	ref string 
	desc ocispec.Descriptor
}

// Resolve creates a resolver that can resolve, fetch, and discover
func (r *Registry) DiscoverFetch(ctx context.Context, reference string) (remotes.Resolver, error) {
	if r == nil {
		return nil, fmt.Errorf("reference is nil")
	}

	_, err := validateReference(reference)
	if err == nil {
		return resolver{
			ref:        reference,
			resolver:   r.resolve,
			fetcher:    r.fetch,
			discoverer: r.discover,
			pusher:     nil}, nil
	}

	return nil, err
}

// PushPull creates a resolver that can do everything above and also push to the registry as well
func (r *Registry) PushPull(ctx context.Context, desc ocispec.Descriptor) (remotes.Resolver, error) {
	if r == nil {
		return nil, fmt.Errorf("reference is nil")
	}

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

// resolve resolves a reference to a descriptor
func (r *Registry) resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	if r == nil {
		return "", ocispec.Descriptor{}, fmt.Errorf("reference is nil")
	}

	err = r.ping(ctx)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	// desc, err = r.getDescriptor(ctx)
	// if err == nil && desc.Digest != "" {
	// 	return ref, desc, nil
	// }

	manifest, err := r.getDescriptorWithManifest(ctx)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	r.manifest = manifest
	return ref, manifest.Config, nil
}

// ping ensures that the registry is alive and valid
func (r *Registry) ping(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("reference is nil")
	}

	request, err := endpoints.e1.prepare()(ctx, r.host, r.namespace, r.ref)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("non successful error code %d", resp.StatusCode)
	}

	return nil
}

func (r *Registry) fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	return blobs{desc}.resolve(ctx, r.client)
}

func (r *Registry) discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error) {
	return nil, fmt.Errorf("discover api has not been implemented") // TODO
}

func (r *Registry) push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return nil, fmt.Errorf("push api has not been implemented") // TODO
}
