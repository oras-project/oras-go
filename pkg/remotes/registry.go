package remotes

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Registry is an opaqueish type which represents an OCI V2 API registry
type Registry struct {
	client      *http.Client
	host        string
	namespace   string
	descriptors map[reference]*ocispec.Descriptor
	manifest    map[reference]*ocispec.Manifest
}

type address struct {
	host string
	ns   string
	loc  string
}

type reference struct {
	add   address
	media string
	digst digest.Digest
	sig   digest.Digest
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

// ping ensures that the registry is alive and a registry
func (r *Registry) ping(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("reference is nil")
	}

	request, err := endpoints.e1.prepare()(ctx, r.host, r.namespace, "")
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

// resolve resolves a reference to a descriptor
func (r *Registry) resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	if r == nil {
		return "", ocispec.Descriptor{}, fmt.Errorf("reference is nil")
	}

	// ensure the registry is running
	err = r.ping(ctx)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	// format the reference
	manifestRef := reference{
		add: address{
			host: r.host,
			ns:   r.namespace,
			loc:  ref,
		},
		digst: "",
	}

	// format the manifests request
	m := manifests{ref: manifestRef}

	// Return early if we can get the manifest early
	desc, err = m.getDescriptor(ctx, r.client)
	if err == nil && desc.Digest != "" {
		manifestRef.digst = desc.Digest
		r.descriptors[manifestRef] = &desc

		return ref, desc, nil
	}

	// Get the manifest to retrieve the desc
	manifest, err := m.getDescriptorWithManifest(ctx, r.client)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	manifestRef.digst = desc.Digest
	r.descriptors[manifestRef] = &manifest.Config
	r.manifest[manifestRef] = manifest

	return ref, manifest.Config, nil
}

func (r *Registry) fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	if r == nil {
		return nil, fmt.Errorf("reference is nil")
	}

	// ensure the registry is running
	err := r.ping(ctx)
	if err != nil {
		return nil, err
	}

	return blob{
		ref: reference{
			add: address{
				host: r.host,
				ns:   r.namespace,
				loc:  "",
			},
			digst: desc.Digest,
			media: desc.MediaType,
		},
	}.fetch(ctx, r.client)
}

func (r *Registry) discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) (*Artifacts, error) {
	if r == nil {
		return nil, fmt.Errorf("reference is nil")
	}

	// ensure the registry is running
	err := r.ping(ctx)
	if err != nil {
		return nil, err
	}

	return artifacts{
		artifactType: artifactType,
		ref: reference{
			add: address{
				host: r.host,
				ns:   r.namespace,
				loc:  "",
			},
			digst: desc.Digest,
			media: desc.MediaType,
		},
	}.discover(ctx, r.client)
}

func (r *Registry) push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return nil, fmt.Errorf("push api has not been implemented") // TODO
}
