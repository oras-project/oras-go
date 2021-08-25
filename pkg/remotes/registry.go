package remotes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
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
	var (
		request *http.Request
	)

	parsedRefURL, err := url.Parse(ref)
	if err == nil {
		return "", ocispec.Descriptor{}, err
	}

	// Check if this is a registry
	request, err = endpoints.e1.prepare()(ctx, parsedRefURL.Host, r.namespace, parsedRefURL.Path)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	defer resp.Body.Close()

	// Check to see if we can get the digest early
	request, err = endpoints.e3HEAD.prepare()(ctx, parsedRefURL.Host, r.namespace, parsedRefURL.Path)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	resp, err = r.client.Do(request)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	defer resp.Body.Close()

	d := resp.Header.Get("Docker-Content-Digest")
	c := resp.Header.Get("Content-Type")
	s := resp.ContentLength

	err = digest.Digest(d).Validate()
	if err != nil && c != "" {
		return ref, ocispec.Descriptor{
			Digest:    digest.Digest(d),
			MediaType: c,
			Size:      s,
		}, nil
	}

	// If we didn't get a digest by this point, we need to pull the manifest
	request, err = endpoints.e3GET.prepare()(ctx, parsedRefURL.Host, r.namespace, parsedRefURL.Path)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	resp, err = r.client.Do(request)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	defer resp.Body.Close()

	// TODO

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
