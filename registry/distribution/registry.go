// Package distribution provides a client to the remote registry.
// Reference: https://github.com/distribution/distribution
package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"oras.land/oras-go/v2/registry"
)

// Registry is a HTTP client to a remote registry.
type Registry struct {
	// Client is the underlying HTTP client used to access the remtoe registry.
	Client *http.Client

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
		Client:    http.DefaultClient,
		Reference: ref,
	}, nil
}

// Repositories lists the name of repositories available in the registry.
// See also `RepositoryListPageSize`.
// Reference: https://docs.docker.com/registry/spec/api/#catalog
func (r *Registry) Repositories(ctx context.Context, fn func(repos []string) error) error {
	url := fmt.Sprintf("%s/v2/_catalog", r.endpoint())
	if r.RepositoryListPageSize > 0 {
		url = fmt.Sprintf("%s?n=%d", url, r.RepositoryListPageSize)
	}

	var err error
	for {
		url, err = r.repositories(ctx, fn, url)
		if err != nil {
			if err == errNoLink {
				return nil
			}
			return err
		}
	}
}

// repositories returns a single page of repository list with the next link.
func (r *Registry) repositories(ctx context.Context, fn func(repos []string) error, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", parseErrorResponse(resp)
	}
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return "", err
	}
	if err := fn(catalog.Repositories); err != nil {
		return "", err
	}

	return parseLink(resp)
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
		Client:          r.Client,
		Reference:       ref,
		PlainHTTP:       r.PlainHTTP,
		TagListPageSize: r.TagListPageSize,
	}, nil
}

// endpoint returns the base endpoint of the remote registry.
func (r *Registry) endpoint() string {
	scheme := "https"
	if r.PlainHTTP {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Reference.Host())
}
