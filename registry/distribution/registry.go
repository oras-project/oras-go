/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
// Package distribution provides a client to the remote registry.
// Reference: https://github.com/distribution/distribution
package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"oras.land/oras-go/v2/registry"
)

// RepositoryOptions is an alias of Repository to avoid name conflicts.
// It also hides all methods associated with Repository.
type RepositoryOptions Repository

// Registry is a HTTP client to a remote registry.
type Registry struct {
	// RepositoryOptions contains common options for Registry and Repository.
	// It is also used as a template for derived repositories.
	RepositoryOptions

	// RepositoryListPageSize specifies the page size when invoking the catalog
	// API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://docs.docker.com/registry/spec/api/#catalog
	RepositoryListPageSize int
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
		RepositoryOptions: RepositoryOptions{
			Reference: ref,
		},
	}, nil
}

// client returns a HTTP client used to access the remote registry.
// A default HTTP client is return if the client is not configured.
func (r *Registry) client() *http.Client {
	if r.Client == nil {
		return http.DefaultClient
	}
	return r.Client
}

// Repositories lists the name of repositories available in the registry.
// See also `RepositoryListPageSize`.
// Reference: https://docs.docker.com/registry/spec/api/#catalog
func (r *Registry) Repositories(ctx context.Context, fn func(repos []string) error) error {
	url := buildRegistryCatalogURL(r.PlainHTTP, r.Reference)
	var err error
	for err == nil {
		url, err = r.repositories(ctx, fn, url)
	}
	if err != errNoLink {
		return err
	}
	return nil
}

// repositories returns a single page of repository list with the next link.
func (r *Registry) repositories(ctx context.Context, fn func(repos []string) error, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if r.RepositoryListPageSize > 0 {
		q := req.URL.Query()
		q.Set("n", strconv.Itoa(r.RepositoryListPageSize))
		req.URL.RawQuery = q.Encode()
	}

	resp, err := r.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", parseErrorResponse(resp)
	}
	var page struct {
		Repositories []string `json:"repositories"`
	}
	lr := limitReader(resp.Body, r.MaxMetadataBytes)
	if err := json.NewDecoder(lr).Decode(&page); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %v", resp.Request.Method, resp.Request.URL, err)
	}
	if err := fn(page.Repositories); err != nil {
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

	repo := Repository(r.RepositoryOptions)
	repo.Reference = ref
	return &repo, nil
}
