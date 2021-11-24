package registry

import (
	"context"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

// Repository is an ORAS target and an union of the blob and the manifest CASs.
// As specified by https://docs.docker.com/registry/spec/api/, it is natural to
// assume that content.Resolver interface only works for manifests. Tagging a
// blob may be resulted in an `ErrUnsupported` error. However, this interface
// does not restrict tagging blobs.
// Since a repository is an union of the blob and the manifest CASs, all
// operations defined in the `BlobStore` are executed depending on the media
// type of the given descriptor accordingly.
// Furthurmore, this interface also provides the ability to enforce the
// separation of the blob and the manifests CASs.
type Repository interface {
	oras.Target
	BlobStore

	// Blobs provides access to the blob CAS only, which contains config blobs,
	// layers, and other generic blobs.
	Blobs() BlobStore

	// Manifests provides access to the manifest CAS only.
	Manifests() BlobStore

	// Tags lists the tags available in the repository.
	// Since the returned tag list may be paginated by the underlying
	// implementation, a function should be passed in to process the paginated
	// tag list.
	// Note: When implemented by a remote registry, the tags API is called.
	// However, not all registries supports pagination or conforms the
	// specification.
	// Reference: https://docs.docker.com/registry/spec/api/#tags
	// See also `Tags()` in this package.
	Tags(ctx context.Context, fn func(tags []string) error) error
}

// BlobStore is a CAS with the ability to delete its content.
type BlobStore interface {
	content.Storage
	content.Deleter
}

// Tags lists the tags available in the repository.
func Tags(ctx context.Context, repo Repository) ([]string, error) {
	var res []string
	if err := repo.Tags(ctx, func(tags []string) error {
		res = append(res, tags...)
		return nil
	}); err != nil {
		return nil, err
	}
	return res, nil
}
