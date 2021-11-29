package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/docker"
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

// blobStore detects the blob store for the given descriptor.
func (r *Repository) blobStore(desc ocispec.Descriptor) registry.BlobStore {
	if isManifest(desc) {
		return r.Manifests()
	}
	return r.Blobs()
}

// Fetch fetches the content identified by the descriptor.
func (r *Repository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return r.blobStore(target).Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (r *Repository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return r.blobStore(expected).Push(ctx, expected, content)
}

// Exists returns true if the described content exists.
func (r *Repository) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return r.blobStore(target).Exists(ctx, target)
}

// Delete removes the content identified by the descriptor.
func (r *Repository) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return r.blobStore(target).Delete(ctx, target)
}

// Blobs provides access to the blob CAS only, which contains config blobs,
// layers, and other generic blobs.
func (r *Repository) Blobs() registry.BlobStore {
	return &blobStore{repo: r}
}

// Manifests provides access to the manifest CAS only.
func (r *Repository) Manifests() registry.BlobStore {
	return &manifestStore{repo: r}
}

// Resolve resolves a reference to a descriptor.
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	panic("not implemented") // TODO: Implement
}

// Tag tags a descriptor with a reference string.
func (r *Repository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	panic("not implemented") // TODO: Implement
}

// Tags lists the tags available in the repository.
func (r *Repository) Tags(ctx context.Context, fn func(tags []string) error) error {
	url := fmt.Sprintf("%s/tags/list", r.endpoint())
	if r.TagListPageSize > 0 {
		url = fmt.Sprintf("%s?n=%d", url, r.TagListPageSize)
	}

	var err error
	for err == nil {
		url, err = r.tags(ctx, fn, url)
	}
	if err != errNoLink {
		return err
	}
	return nil
}

// tags returns a single page of tag list with the next link.
func (r *Repository) tags(ctx context.Context, fn func(tags []string) error, url string) (string, error) {
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
	var list struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", err
	}
	if err := fn(list.Tags); err != nil {
		return "", err
	}

	return parseLink(resp)
}

// endpoint returns the base endpoint of the remote registry.
func (r *Repository) endpoint() string {
	scheme := "https"
	if r.PlainHTTP {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/v2/%s", scheme, r.Reference.Host(), r.Reference.Repository)
}

type blobStore struct {
	repo *Repository
}

// Fetch fetches the content identified by the descriptor.
func (s *blobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	panic("not implemented") // TODO: Implement
}

// Push pushes the content, matching the expected descriptor.
func (s *blobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	panic("not implemented") // TODO: Implement
}

// Exists returns true if the described content exists.
func (s *blobStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	panic("not implemented") // TODO: Implement
}

// Delete removes the content identified by the descriptor.
func (s *blobStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	panic("not implemented") // TODO: Implement
}

type manifestStore struct {
	repo *Repository
}

// Fetch fetches the content identified by the descriptor.
func (s *manifestStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	panic("not implemented") // TODO: Implement
}

// Push pushes the content, matching the expected descriptor.
func (s *manifestStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	panic("not implemented") // TODO: Implement
}

// Exists returns true if the described content exists.
func (s *manifestStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	panic("not implemented") // TODO: Implement
}

// Delete removes the content identified by the descriptor.
func (s *manifestStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	panic("not implemented") // TODO: Implement
}

// isManifest determines if the given descriptor points to a manifest.
func isManifest(desc ocispec.Descriptor) bool {
	switch desc.MediaType {
	case docker.MediaTypeManifest, ocispec.MediaTypeImageManifest,
		docker.MediaTypeManifestList, ocispec.MediaTypeImageIndex,
		artifactspec.MediaTypeArtifactManifest:
		return true
	}
	return false
}
