package distribution

import (
	"context"
	"io"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry"
)

// Repository is a HTTP client to a remote repository.
type Repository struct {
	// Client is the underlying HTTP client used to access the remtoe registry.
	Client *http.Client

	// Reference references the remote repository.
	Reference registry.Reference

	// PlainHTTP signals the transport to access the remote repository via HTTP
	// instead of HTTPS.
	PlainHTTP bool

	// TagListPageSize specifies the page size when invoking the tag list API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://docs.docker.com/registry/spec/api/#tags
	TagListPageSize int
}

// NewRepository creates a client to the remote repository identified by a
// reference.
// Example: localhost:5000/hello-world
func NewRepository(reference string) (*Repository, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		return nil, err
	}
	return &Repository{
		Client:    http.DefaultClient,
		Reference: ref,
	}, nil
}

// Fetch fetches the content identified by the descriptor.
func (r *Repository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	panic("not implemented") // TODO: Implement
}

// Push pushes the content, matching the expected descriptor.
func (r *Repository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	panic("not implemented") // TODO: Implement
}

// Exists returns true if the described content exists.
func (r *Repository) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	panic("not implemented") // TODO: Implement
}

// Resolve resolves a reference to a descriptor.
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	panic("not implemented") // TODO: Implement
}

// Tag tags a descriptor with a reference string.
func (r *Repository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	panic("not implemented") // TODO: Implement
}

// Delete removes the content identified by the descriptor.
func (r *Repository) Delete(ctx context.Context, target ocispec.Descriptor) error {
	panic("not implemented") // TODO: Implement
}

// Blobs provides access to the blob CAS only, which contains config blobs,
// layers, and other generic blobs.
func (r *Repository) Blobs() registry.BlobStore {
	panic("not implemented") // TODO: Implement
}

// Manifests provides access to the manifest CAS only.
func (r *Repository) Manifests() registry.BlobStore {
	panic("not implemented") // TODO: Implement
}

// Tags lists the tags available in the repository.
func (r *Repository) Tags(ctx context.Context, fn func(tags []string) error) error {
	panic("not implemented") // TODO: Implement
}
