package distribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/httputil"
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

// Resolve resolves a reference to a manifest descriptor.
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return r.Manifests().Resolve(ctx, reference)
}

// Tag tags a manifest descriptor with a reference string.
func (r *Repository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	ref, err := r.parseReference(reference)
	if err != nil {
		return err
	}

	rc, err := r.Manifests().Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	return r.push(ctx, desc, rc, ref.Reference)
}

// PushTag pushes the manifest with a reference tag.
func (r *Repository) PushTag(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	ref, err := r.parseReference(reference)
	if err != nil {
		return err
	}
	return r.push(ctx, expected, content, ref.Reference)
}

// push pushes the content, matching the expected descriptor.
func (r *Repository) push(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	url := fmt.Sprintf("%s/manifests/%s", r.endpoint(), reference)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, content)
	if err != nil {
		return err
	}
	if req.GetBody != nil && req.ContentLength != expected.Size {
		// short circuit a size mismatch for built-in types.
		return fmt.Errorf("mismatch content length %d: expect %d", req.ContentLength, expected.Size)
	}
	req.ContentLength = expected.Size
	req.Header.Set("Content-Type", expected.MediaType)

	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return parseErrorResponse(resp)
	}
	return verifyContentDigest(resp, expected.Digest)
}

// parseReference validates the reference.
// Both simplified or fully qualified references are accepted.
func (r *Repository) parseReference(reference string) (registry.Reference, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		ref = registry.Reference{
			Registry:   r.Reference.Registry,
			Repository: r.Reference.Repository,
			Reference:  reference,
		}
		if err = ref.ValidateReference(); err != nil {
			return registry.Reference{}, err
		}
		return ref, nil
	}
	if ref.Registry == r.Reference.Registry && ref.Repository == r.Reference.Repository {
		return ref, nil
	}
	return registry.Reference{}, fmt.Errorf("%w %q: expect %q", errdef.ErrInvalidReference, ref, r.Reference)
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

// UpEdges returns the manifest descriptors directly referencing the given
// manifest descriptor.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md
func (r *Repository) UpEdges(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	panic("not implemented") // TODO: Implement
}

// endpoint returns the base endpoint of the remote registry.
func (r *Repository) endpoint() string {
	scheme := "https"
	if r.PlainHTTP {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/v2/%s", scheme, r.Reference.Host(), r.Reference.Repository)
}

// delete removes the content identified by the descriptor in the entity "blobs"
// or "manifests".
func (r *Repository) delete(ctx context.Context, entity string, target ocispec.Descriptor) error {
	url := fmt.Sprintf("%s/%s/%s", r.endpoint(), entity, target.Digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusAccepted:
		return verifyContentDigest(resp, target.Digest)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", target.Digest, errdef.ErrNotFound)
	default:
		return parseErrorResponse(resp)
	}
}

// blobStore accesses the manifest part of the repository.
type blobStore struct {
	repo *Repository
}

// Fetch fetches the content identified by the descriptor.
func (s *blobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (rc io.ReadCloser, err error) {
	url := fmt.Sprintf("%s/blobs/%s", s.repo.endpoint(), target.Digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// probe server range request ability.
	// Docker spec allows range header form of "Range: bytes=<start>-<end>".
	// However, the remote server may still not RFC 7233 compliant.
	// Reference: https://docs.docker.com/registry/spec/api/#blob
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", target.Size-1))

	resp, err := s.repo.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK: // server does not support seek as `Range` was ignored.
		if size := resp.ContentLength; size != -1 && size != target.Size {
			return nil, fmt.Errorf("%s %q: mismatch Content-Length", resp.Request.Method, resp.Request.URL)
		}
		return resp.Body, nil
	case http.StatusPartialContent:
		return httputil.NewReadSeekCloser(s.repo.Client, req, resp.Body, target.Size), nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", target.Digest, errdef.ErrNotFound)
	default:
		return nil, parseErrorResponse(resp)
	}
}

// Push pushes the content, matching the expected descriptor.
// Existing content is not checked by Push() to minimize the number of out-going
// requests.
// Push is done by conventional 2-step monolithic upload to achieve maximum
// compability.
// Reference: https://docs.docker.com/registry/spec/api/#pushing-an-image
func (s *blobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	// start an upload
	url := fmt.Sprintf("%s/blobs/uploads/", s.repo.endpoint())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}

	resp, err := s.repo.Client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		return parseErrorResponse(resp)
	}
	resp.Body.Close()

	// monolithic upload
	location, err := resp.Location()
	if err != nil {
		return err
	}
	url = location.String()
	req, err = http.NewRequestWithContext(ctx, http.MethodPut, url, content)
	if err != nil {
		return err
	}
	if req.GetBody != nil && req.ContentLength != expected.Size {
		// short circuit a size mismatch for built-in types.
		return fmt.Errorf("mismatch content length %d: expect %d", req.ContentLength, expected.Size)
	}
	req.ContentLength = expected.Size
	// the expected media type is ignored as in the API doc.
	req.Header.Set("Content-Type", "application/octet-stream")
	q := req.URL.Query()
	q.Add("digest", expected.Digest.String())
	req.URL.RawQuery = q.Encode()

	resp, err = s.repo.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return parseErrorResponse(resp)
	}
	return nil
}

// Exists returns true if the described content exists.
func (s *blobStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	_, err := s.Resolve(ctx, target.Digest.String())
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes the content identified by the descriptor.
func (s *blobStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.repo.delete(ctx, "blobs", target)
}

// Resolve resolves a reference to a descriptor.
func (s *blobStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	ref, err := s.repo.parseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	refDigest, err := ref.Digest()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	url := fmt.Sprintf("%s/blobs/%s", s.repo.endpoint(), refDigest)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	resp, err := s.repo.Client.Do(req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// no-op
	case http.StatusNotFound:
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, parseErrorResponse(resp)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	size := resp.ContentLength
	if size == -1 {
		return ocispec.Descriptor{}, fmt.Errorf("%s %q: unknown response Content-Length", resp.Request.Method, resp.Request.URL)
	}
	if err := verifyContentDigest(resp, refDigest); err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    refDigest,
		Size:      size,
	}, nil
}

// manifestStore accesses the manifest part of the repository.
type manifestStore struct {
	repo *Repository
}

// Fetch fetches the content identified by the descriptor.
func (s *manifestStore) Fetch(ctx context.Context, target ocispec.Descriptor) (rc io.ReadCloser, err error) {
	url := fmt.Sprintf("%s/manifests/%s", s.repo.endpoint(), target.Digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", target.MediaType)

	resp, err := s.repo.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		// no-op
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", target.Digest, errdef.ErrNotFound)
	default:
		return nil, parseErrorResponse(resp)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("%s %q: invalid response Content-Type: %w", resp.Request.Method, resp.Request.URL, err)
	}
	if mediaType != target.MediaType {
		return nil, fmt.Errorf("%s %q: mismatch response Content-Type %q: expect %q", resp.Request.Method, resp.Request.URL, mediaType, target.MediaType)
	}
	if size := resp.ContentLength; size != -1 && size != target.Size {
		return nil, fmt.Errorf("%s %q: mismatch Content-Length", resp.Request.Method, resp.Request.URL)
	}
	if err := verifyContentDigest(resp, target.Digest); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Push pushes the content, matching the expected descriptor.
func (s *manifestStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return s.repo.push(ctx, expected, content, expected.Digest.String())
}

// Exists returns true if the described content exists.
func (s *manifestStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	_, err := s.Resolve(ctx, target.Digest.String())
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes the content identified by the descriptor.
func (s *manifestStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.repo.delete(ctx, "manifests", target)
}

// Resolve resolves a reference to a descriptor.
func (s *manifestStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	ref, err := s.repo.parseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	url := fmt.Sprintf("%s/manifests/%s", s.repo.endpoint(), ref.Reference)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	req.Header.Set("Accept", manifestAcceptHeader)

	resp, err := s.repo.Client.Do(req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// no-op
	case http.StatusNotFound:
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, parseErrorResponse(resp)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%s %q: invalid response Content-Type: %w", resp.Request.Method, resp.Request.URL, err)
	}
	size := resp.ContentLength
	if size == -1 {
		return ocispec.Descriptor{}, fmt.Errorf("%s %q: unknown response Content-Length", resp.Request.Method, resp.Request.URL)
	}

	// validate digest if reference is a digest
	if refDigest, err := ref.Digest(); err == nil {
		if err = verifyContentDigest(resp, refDigest); err != nil {
			return ocispec.Descriptor{}, err
		}
		return ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    refDigest,
			Size:      size,
		}, nil
	}

	digestStr := resp.Header.Get("Docker-Content-Digest")
	if digestStr == "" {
		return ocispec.Descriptor{}, fmt.Errorf("%s %q: empty response Docker-Content-Digest", resp.Request.Method, resp.Request.URL)
	}
	contentDigest, err := digest.Parse(digestStr)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%s %q: invalid response Docker-Content-Digest: %s", resp.Request.Method, resp.Request.URL, digestStr)
	}
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    contentDigest,
		Size:      size,
	}, nil
}

// verifyContentDigest verifies "Docker-Content-Digest" header if present.
// OCI distribution-spec states the Docker-Content-Digest header is optional.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.0.1/spec.md#legacy-docker-support-http-headers
func verifyContentDigest(resp *http.Response, expected digest.Digest) error {
	digestStr := resp.Header.Get("Docker-Content-Digest")
	if digestStr == "" {
		return nil
	}
	contentDigest, err := digest.Parse(digestStr)
	if err != nil {
		return fmt.Errorf("%s %q: invalid response Docker-Content-Digest: %s", resp.Request.Method, resp.Request.URL, digestStr)
	}
	if contentDigest != expected {
		return fmt.Errorf("%s: mismatch digest: %s", expected, contentDigest)
	}
	return nil
}
