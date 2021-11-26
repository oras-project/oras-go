// Package distribution provides a client to the remote registry.
// Reference: https://github.com/distribution/distribution
package distribution

import (
	"context"
	"net/http"

	"oras.land/oras-go/v2/registry"
)

// Registry is a HTTP client to a remote registry.
type Registry struct {
	// Transport specifies the mechanism by which individual requests are made.
	Transport http.RoundTripper

	// Reference references the remote registry.
	// It is also used as a template for derived repository.
	Reference registry.Reference

	// PlainHTTP signals the transport to access the remote registry via HTTP
	// instead of HTTPS.
	PlainHTTP bool

	// RepositoryListPageSize specifies the page size when invoking the catalog
	// API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://docs.docker.com/registry/spec/api/#catalog
	RepositoryListPageSize int

	// TagListPageSize specifies the page size when invoking the tag list API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://docs.docker.com/registry/spec/api/#tags
	// This option is used as a template for derived repository.
	TagListPageSize int
}

// NewRegistry creates a client to the remote registry with the specified domain
// name.
// Example: localhost:5000
func NewRegistry(name string) (*Registry, error) {
	ref := registry.Reference{
		Registry: name,
	}
	if err := ref.ValidateRegistry(); err != nil {
		return nil, err
	}
	return &Registry{
		Transport: http.DefaultTransport,
		Reference: ref,
	}, nil
}

// Repositories lists the name of repositories available in the registry.
func (r *Registry) Repositories(ctx context.Context, fn func(repos []string) error) error {
	panic("not implemented") // TODO: Implement
}

// Repository returns a repository reference by the given name.
func (r *Registry) Repository(ctx context.Context, name string) (registry.Repository, error) {
	ref := registry.Reference{
		Registry:   r.Reference.Registry,
		Repository: name,
	}
	if err := ref.ValidateRepository(); err != nil {
		return nil, err
	}
	return &Repository{
		Transport:       r.Transport,
		Reference:       ref,
		PlainHTTP:       r.PlainHTTP,
		TagListPageSize: r.TagListPageSize,
	}, nil
}
