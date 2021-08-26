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

// Resolve creates a resolver that can resolve, fetch, and discover
func (r *Registry) Resolve(ctx context.Context, reference string) (remotes.Resolver, error) {
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

// getDescriptor tries to resolve the reference to a descriptor using the headers
func (r *Registry) getDescriptor(ctx context.Context) (ocispec.Descriptor, error) {
	if r == nil {
		return ocispec.Descriptor{}, fmt.Errorf("reference is nil")
	}

	request, err := endpoints.e3HEAD.prepare()(ctx, r.host, r.namespace, r.ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return ocispec.Descriptor{}, fmt.Errorf("non successful error code %d", resp.StatusCode)
	}

	d := resp.Header.Get("Docker-Content-Digest")
	c := resp.Header.Get("Content-Type")
	s := resp.ContentLength

	err = digest.Digest(d).Validate()
	if err == nil && c != "" && s > 0 {
		// TODO: Write annotations
		return ocispec.Descriptor{
			Digest:    digest.Digest(d),
			MediaType: c,
			Size:      s,
		}, nil
	}

	return ocispec.Descriptor{}, err
}

// getDescriptorWithManifest tries to resolve the reference by fetching the manifest
func (r *Registry) getDescriptorWithManifest(ctx context.Context) (*ocispec.Manifest, error) {
	if r == nil {
		return nil, fmt.Errorf("reference to this registry pointer is nil")
	}

	// If we didn't get a digest by this point, we need to pull the manifest
	request, err := endpoints.e3GET.prepare()(ctx, r.host, r.namespace, r.ref)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	manifest := &ocispec.Manifest{}
	err = json.NewDecoder(resp.Body).Decode(manifest)
	if err != nil {
		return nil, err
	}

	return manifest, nil
}

func (r *Registry) fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	if r == nil {
		return nil, fmt.Errorf("reference is nil")
	}

	err := r.prefetch(ctx, desc)
	if err != nil {
		return nil, err
	}

	request, err := endpoints.e2GET.prepareWithDescriptor()(ctx, r.host, r.namespace, desc)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Could not fetch content")
	}

	return resp.Body, nil
}

func (r *Registry) prefetch(ctx context.Context, desc ocispec.Descriptor) error {
	if r == nil {
		return fmt.Errorf("reference is nil")
	}

	request, err := endpoints.e2HEAD.prepareWithDescriptor()(ctx, r.host, r.namespace, desc)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return nil
}

func (r *Registry) discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error) {
	return nil, fmt.Errorf("discover api has not been implemented") // TODO
}

func (r *Registry) push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return nil, fmt.Errorf("push api has not been implemented") // TODO
}
