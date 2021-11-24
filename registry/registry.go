// Package registry provides high-level operations to manage registries.
package registry

import "context"

// Registry represents a collection of repositories.
type Registry interface {
	// Repositories lists the name of repositories available in the registry.
	// Since the returned repositories may be paginated by the underlying
	// implementation, a function should be passed in to process the paginated
	// repository list.
	// Note: When implemented by a remote registry, the catalog API is called.
	// However, not all registries supports pagination or conforms the
	// specification.
	// Reference: https://docs.docker.com/registry/spec/api/#catalog
	// See also `Repositories()` in this package.
	Repositories(ctx context.Context, fn func(repos []string) error) error

	// Repository returns a repository reference by the given name.
	Repository(ctx context.Context, name string) (Repository, error)
}

// Repositories lists the name of repositories available in the registry.
func Repositories(ctx context.Context, reg Registry) ([]string, error) {
	var res []string
	if err := reg.Repositories(ctx, func(repos []string) error {
		res = append(res, repos...)
		return nil
	}); err != nil {
		return nil, err
	}
	return res, nil
}
