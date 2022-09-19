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

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/opencontainers/distribution-spec/specs-go/v1/extensions"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/httputil"
	"oras.land/oras-go/v2/internal/ioutil"
	"oras.land/oras-go/v2/internal/registryutil"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/internal/errutil"
)

// dockerContentDigestHeader - The Docker-Content-Digest header, if present on
// the response, returns the canonical digest of the uploaded blob.
// See https://docs.docker.com/registry/spec/api/#digest-header
// See https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pull
const dockerContentDigestHeader = "Docker-Content-Digest"

// referrersApiRegex checks referrers API version.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md#versioning
var referrersApiRegex = regexp.MustCompile(`^oras/1\.(0|[1-9]\d*)$`)

// Client is an interface for a HTTP client.
type Client interface {
	// Do sends an HTTP request and returns an HTTP response.
	//
	// Unlike http.RoundTripper, Client can attempt to interpret the response
	// and handle higher-level protocol details such as redirects and
	// authentication.
	//
	// Like http.RoundTripper, Client should not modify the request, and must
	// always close the request body.
	Do(*http.Request) (*http.Response, error)
}

// Repository is an HTTP client to a remote repository.
type Repository struct {
	// Client is the underlying HTTP client used to access the remote registry.
	// If nil, auth.DefaultClient is used.
	Client Client

	// Reference references the remote repository.
	Reference registry.Reference

	// PlainHTTP signals the transport to access the remote repository via HTTP
	// instead of HTTPS.
	PlainHTTP bool

	// ManifestMediaTypes is used in `Accept` header for resolving manifests
	// from references. It is also used in identifying manifests and blobs from
	// descriptors. If an empty list is present, default manifest media types
	// are used.
	ManifestMediaTypes []string

	// TagListPageSize specifies the page size when invoking the tag list API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://docs.docker.com/registry/spec/api/#tags
	TagListPageSize int

	// ReferrerListPageSize specifies the page size when invoking the Referrers
	// API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md
	ReferrerListPageSize int

	// MaxMetadataBytes specifies a limit on how many response bytes are allowed
	// in the server's response to the metadata APIs, such as catalog list, tag
	// list, and referrers list.
	// If less than or equal to zero, a default (currently 4MiB) is used.
	MaxMetadataBytes int64
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
		Reference: ref,
	}, nil
}

// client returns an HTTP client used to access the remote repository.
// A default HTTP client is return if the client is not configured.
func (r *Repository) client() Client {
	if r.Client == nil {
		return auth.DefaultClient
	}
	return r.Client
}

// blobStore detects the blob store for the given descriptor.
func (r *Repository) blobStore(desc ocispec.Descriptor) registry.BlobStore {
	if isManifest(r.ManifestMediaTypes, desc) {
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
func (r *Repository) Manifests() registry.ManifestStore {
	return &manifestStore{repo: r}
}

// Resolve resolves a reference to a manifest descriptor.
// See also `ManifestMediaTypes`.
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return r.Manifests().Resolve(ctx, reference)
}

// Tag tags a manifest descriptor with a reference string.
func (r *Repository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	return r.Manifests().Tag(ctx, desc, reference)
}

// PushReference pushes the manifest with a reference tag.
func (r *Repository) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	return r.Manifests().PushReference(ctx, expected, content, reference)
}

// FetchReference fetches the manifest identified by the reference.
// The reference can be a tag or digest.
func (r *Repository) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	return r.Manifests().FetchReference(ctx, reference)
}

// ParseReference resolves a tag or a digest reference to a fully qualified
// reference from a base reference r.Reference.
// Tag, digest, or fully qualified references are accepted as input.
// If reference is a fully qualified reference, then ParseReference parses it
// and returns the parsed reference. If the parsed reference does not share
// the same base reference with the Repository r, ParseReference returns a
// wrapped error ErrInvalidReference.
func (r *Repository) ParseReference(reference string) (registry.Reference, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		// reference is not a FQDN
		if index := strings.IndexByte(reference, '@'); index != -1 {
			// drop tag since the digest is present
			reference = reference[index+1:]
		}
		ref = registry.Reference{
			Registry:   r.Reference.Registry,
			Repository: r.Reference.Repository,
			Reference:  reference,
		}
		if err = ref.ValidateReference(); err != nil {
			return registry.Reference{}, err
		}
	} else if ref.Registry != r.Reference.Registry || ref.Repository != r.Reference.Repository {
		return registry.Reference{}, fmt.Errorf("%w %q: expect %q", errdef.ErrInvalidReference, ref, r.Reference)
	}
	if ref.Reference == "" {
		return registry.Reference{}, fmt.Errorf("%w %q: empty reference", errdef.ErrInvalidReference, ref)
	}
	return ref, nil
}

// Tags lists the tags available in the repository.
// See also `TagListPageSize`.
// If `last` is NOT empty, the entries in the response start after the
// tag specified by `last`. Otherwise, the response starts from the top
// of the Tags list.
// References:
// - https://github.com/opencontainers/distribution-spec/blob/main/spec.md#content-discovery
// - https://docs.docker.com/registry/spec/api/#tags
func (r *Repository) Tags(ctx context.Context, last string, fn func(tags []string) error) error {
	ctx = registryutil.WithScopeHint(ctx, r.Reference, auth.ActionPull)
	url := buildRepositoryTagListURL(r.PlainHTTP, r.Reference)
	var err error
	for err == nil {
		url, err = r.tags(ctx, last, fn, url)
		// clear `last` for subsequent pages
		last = ""
	}
	if err != errNoLink {
		return err
	}
	return nil
}

// tags returns a single page of tag list with the next link.
func (r *Repository) tags(ctx context.Context, last string, fn func(tags []string) error, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if r.TagListPageSize > 0 || last != "" {
		q := req.URL.Query()
		if r.TagListPageSize > 0 {
			q.Set("n", strconv.Itoa(r.TagListPageSize))
		}
		if last != "" {
			q.Set("last", last)
		}
		req.URL.RawQuery = q.Encode()
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errutil.ParseErrorResponse(resp)
	}
	var page struct {
		Tags []string `json:"tags"`
	}
	lr := limitReader(resp.Body, r.MaxMetadataBytes)
	if err := json.NewDecoder(lr).Decode(&page); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	if err := fn(page.Tags); err != nil {
		return "", err
	}

	return parseLink(resp)
}

// Predecessors returns the descriptors of ORAS Artifact manifests directly
// referencing the given manifest descriptor.
// Predecessors internally leverages Referrers, and converts the result ORAS
// Artifact descriptors to OCI descriptors.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md
func (r *Repository) Predecessors(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	var res []ocispec.Descriptor
	if err := r.Referrers(ctx, desc, "", func(referrers []ocispec.Descriptor) error {
		for _, referrer := range referrers {
			res = append(res, referrer)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// Referrers lists the descriptors of ORAS Artifact manifests directly
// referencing the given manifest descriptor. fn is called for each page of
// the referrers result. If artifactType is not empty, only referrers of the
// same artifact type are fed to fn.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md
func (r *Repository) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	ref := r.Reference
	ref.Reference = desc.Digest.String()
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildArtifactReferrerURL(r.PlainHTTP, ref, artifactType)
	var err error

	var legacyAPI bool
	url, err = r.referrers(ctx, artifactType, fn, url, legacyAPI)
	// Fallback to legacy url
	if errors.Is(err, errdef.ErrNotFound) {
		url = buildArtifactReferrerURLLegacy(r.PlainHTTP, ref, artifactType)
		legacyAPI = true
		err = nil
	}

	for err == nil {
		url, err = r.referrers(ctx, artifactType, fn, url, legacyAPI)
	}
	if err != errNoLink {
		return err
	}
	return nil
}

// referrers returns a single page of the manifest descriptors directly
// referencing the given manifest descriptor with the next link.
func (r *Repository) referrers(ctx context.Context, artifactType string, fn func(referrers []ocispec.Descriptor) error, url string, legacyAPI bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if r.ReferrerListPageSize > 0 {
		q := req.URL.Query()
		q.Set("n", strconv.Itoa(r.ReferrerListPageSize))
		req.URL.RawQuery = q.Encode()
	}

	resp, err := r.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, errdef.ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return "", errutil.ParseErrorResponse(resp)
	}
	if !legacyAPI {
		if err := verifyOrasApiVersion(resp); err != nil {
			return "", err
		}
	}

	var page struct {
		References []ocispec.Descriptor `json:"references"`
		Referrers  []ocispec.Descriptor `json:"referrers"`
	}
	lr := limitReader(resp.Body, r.MaxMetadataBytes)
	if err := json.NewDecoder(lr).Decode(&page); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	var refs []ocispec.Descriptor
	if legacyAPI {
		refs = page.References
	} else {
		refs = page.Referrers
	}
	// Server may not support filtering. We still need to filter on client side
	// for sure.
	refs = filterReferrers(refs, artifactType)
	if len(refs) > 0 {
		if err := fn(refs); err != nil {
			return "", err
		}
	}

	return parseLink(resp)
}

// filterReferrers filters a slice of referrers by artifactType in place.
// The returned slice contains matching referrers.
func filterReferrers(refs []ocispec.Descriptor, artifactType string) []ocispec.Descriptor {
	if artifactType == "" {
		return refs
	}
	var j int
	for i, ref := range refs {
		if ref.ArtifactType == artifactType {
			if i != j {
				refs[j] = ref
			}
			j++
		}
	}
	return refs[:j]
}

// DiscoverExtensions lists all supported extensions in current repository.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md#api-discovery
func (r *Repository) DiscoverExtensions(ctx context.Context) ([]extensions.Extension, error) {
	ctx = registryutil.WithScopeHint(ctx, r.Reference, auth.ActionPull)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildDiscoveryURL(r.PlainHTTP, r.Reference), nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errutil.ParseErrorResponse(resp)
	}

	var extensionList extensions.ExtensionList
	lr := limitReader(resp.Body, r.MaxMetadataBytes)
	if err := json.NewDecoder(lr).Decode(&extensionList); err != nil {
		return nil, fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	return extensionList.Extensions, nil
}

// delete removes the content identified by the descriptor in the entity "blobs"
// or "manifests".
func (r *Repository) delete(ctx context.Context, target ocispec.Descriptor, isManifest bool) error {
	ref := r.Reference
	ref.Reference = target.Digest.String()
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionDelete)
	buildURL := buildRepositoryBlobURL
	if isManifest {
		buildURL = buildRepositoryManifestURL
	}
	url := buildURL(r.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := r.client().Do(req)
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
		return errutil.ParseErrorResponse(resp)
	}
}

// blobStore accesses the blob part of the repository.
type blobStore struct {
	repo *Repository
}

// Fetch fetches the content identified by the descriptor.
func (s *blobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (rc io.ReadCloser, err error) {
	ref := s.repo.Reference
	ref.Reference = target.Digest.String()
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// probe server range request ability.
	// Docker spec allows range header form of "Range: bytes=<start>-<end>".
	// However, the remote server may still not RFC 7233 compliant.
	// Reference: https://docs.docker.com/registry/spec/api/#blob
	if target.Size > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", target.Size-1))
	}

	resp, err := s.repo.client().Do(req)
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
		return httputil.NewReadSeekCloser(s.repo.client(), req, resp.Body, target.Size), nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", target.Digest, errdef.ErrNotFound)
	default:
		return nil, errutil.ParseErrorResponse(resp)
	}
}

// Push pushes the content, matching the expected descriptor.
// Existing content is not checked by Push() to minimize the number of out-going
// requests.
// Push is done by conventional 2-step monolithic upload instead of a single
// `POST` request for better overall performance. It also allows early fail on
// authentication errors.
// References:
// - https://docs.docker.com/registry/spec/api/#pushing-an-image
// - https://docs.docker.com/registry/spec/api/#initiate-blob-upload
// - https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pushing-a-blob-monolithically
func (s *blobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	// start an upload
	// pushing usually requires both pull and push actions.
	// Reference: https://github.com/distribution/distribution/blob/v2.7.1/registry/handlers/app.go#L921-L930
	ctx = registryutil.WithScopeHint(ctx, s.repo.Reference, auth.ActionPull, auth.ActionPush)
	url := buildRepositoryBlobUploadURL(s.repo.PlainHTTP, s.repo.Reference)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	reqHostname := req.URL.Hostname()
	reqPort := req.URL.Port()

	client := s.repo.client()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		return errutil.ParseErrorResponse(resp)
	}
	resp.Body.Close()

	// monolithic upload
	location, err := resp.Location()
	if err != nil {
		return err
	}
	// work-around solution for https://github.com/oras-project/oras-go/issues/177
	// For some registries, if the port 443 is explicitly set to the hostname
	// like registry.wabbit-networks.io:443/myrepo, blob push will fail since
	// the hostname of the Location header in the response is set to
	// registry.wabbit-networks.io instead of registry.wabbit-networks.io:443.
	locationHostname := location.Hostname()
	locationPort := location.Port()
	// if location port 443 is missing, add it back
	if reqPort == "443" && locationHostname == reqHostname && locationPort == "" {
		location.Host = locationHostname + ":" + reqPort
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
	q.Set("digest", expected.Digest.String())
	req.URL.RawQuery = q.Encode()

	// reuse credential from previous POST request
	if auth := resp.Request.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return errutil.ParseErrorResponse(resp)
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
	return s.repo.delete(ctx, target, false)
}

// Resolve resolves a reference to a descriptor.
func (s *blobStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	refDigest, err := ref.Digest()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	resp, err := s.repo.client().Do(req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return generateBlobDescriptor(resp, refDigest)
	case http.StatusNotFound:
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, errutil.ParseErrorResponse(resp)
	}
}

// FetchReference fetches the blob identified by the reference.
// The reference must be a digest.
func (s *blobStore) FetchReference(ctx context.Context, reference string) (desc ocispec.Descriptor, rc io.ReadCloser, err error) {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	refDigest, err := ref.Digest()
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	// probe server range request ability.
	// Docker spec allows range header form of "Range: bytes=<start>-<end>".
	// The form of "Range: bytes=<start>-" is also acceptable.
	// However, the remote server may still not RFC 7233 compliant.
	// Reference: https://docs.docker.com/registry/spec/api/#blob
	req.Header.Set("Range", "bytes=0-")

	resp, err := s.repo.client().Do(req)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK: // server does not support seek as `Range` was ignored.
		desc, err = generateBlobDescriptor(resp, refDigest)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		return desc, resp.Body, nil
	case http.StatusPartialContent:
		desc, err = generateBlobDescriptor(resp, refDigest)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		return desc, httputil.NewReadSeekCloser(s.repo.client(), req, resp.Body, desc.Size), nil
	case http.StatusNotFound:
		return ocispec.Descriptor{}, nil, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, nil, errutil.ParseErrorResponse(resp)
	}
}

// generateBlobDescriptor returns a descriptor generated from the response.
func generateBlobDescriptor(resp *http.Response, refDigest digest.Digest) (ocispec.Descriptor, error) {
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
	ref := s.repo.Reference
	ref.Reference = target.Digest.String()
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", target.MediaType)

	resp, err := s.repo.client().Do(req)
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
		return nil, errutil.ParseErrorResponse(resp)
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
	return s.push(ctx, expected, content, expected.Digest.String())
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
	return s.repo.delete(ctx, target, true)
}

// Resolve resolves a reference to a descriptor.
// See also `ManifestMediaTypes`.
func (s *manifestStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	req.Header.Set("Accept", manifestAcceptHeader(s.repo.ManifestMediaTypes))

	resp, err := s.repo.client().Do(req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return s.generateDescriptor(resp, ref, req.Method)
	case http.StatusNotFound:
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, errutil.ParseErrorResponse(resp)
	}
}

// FetchReference fetches the manifest identified by the reference.
// The reference can be a tag or digest.
func (s *manifestStore) FetchReference(ctx context.Context, reference string) (desc ocispec.Descriptor, rc io.ReadCloser, err error) {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.PlainHTTP, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	req.Header.Set("Accept", manifestAcceptHeader(s.repo.ManifestMediaTypes))

	resp, err := s.repo.client().Do(req)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		desc, err = s.generateDescriptor(resp, ref, req.Method)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		return desc, resp.Body, nil
	case http.StatusNotFound:
		return ocispec.Descriptor{}, nil, fmt.Errorf("%s: %w", ref.Reference, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, nil, errutil.ParseErrorResponse(resp)
	}
}

// Tag tags a manifest descriptor with a reference string.
func (s *manifestStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return err
	}

	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull, auth.ActionPush)
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	return s.push(ctx, desc, rc, ref.Reference)
}

// PushReference pushes the manifest with a reference tag.
func (s *manifestStore) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return err
	}
	return s.push(ctx, expected, content, ref.Reference)
}

// push pushes the manifest content, matching the expected descriptor.
func (s *manifestStore) push(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	ref := s.repo.Reference
	ref.Reference = reference
	// pushing usually requires both pull and push actions.
	// Reference: https://github.com/distribution/distribution/blob/v2.7.1/registry/handlers/app.go#L921-L930
	ctx = registryutil.WithScopeHint(ctx, ref, auth.ActionPull, auth.ActionPush)
	url := buildRepositoryManifestURL(s.repo.PlainHTTP, ref)
	// unwrap the content for optimizations of built-in types.
	body := ioutil.UnwrapNopCloser(content)
	if _, ok := body.(io.ReadCloser); ok {
		// undo unwrap if the nopCloser is intended.
		body = content
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	if req.GetBody != nil && req.ContentLength != expected.Size {
		// short circuit a size mismatch for built-in types.
		return fmt.Errorf("mismatch content length %d: expect %d", req.ContentLength, expected.Size)
	}
	req.ContentLength = expected.Size
	req.Header.Set("Content-Type", expected.MediaType)

	// if the underlying client is an auth client, the content might be read
	// more than once for obtaining the auth challenge and the actual request.
	// To prevent double reading, the manifest is read and stored in the memory,
	// and serve from the memory.
	client := s.repo.client()
	if _, ok := client.(*auth.Client); ok && req.GetBody == nil {
		store := cas.NewMemory()
		err := store.Push(ctx, expected, content)
		if err != nil {
			return err
		}
		req.GetBody = func() (io.ReadCloser, error) {
			return store.Fetch(ctx, expected)
		}
		req.Body, err = req.GetBody()
		if err != nil {
			return err
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return errutil.ParseErrorResponse(resp)
	}
	return verifyContentDigest(resp, expected.Digest)
}

// ParseReference parses a reference to a fully qualified reference.
func (s *manifestStore) ParseReference(reference string) (registry.Reference, error) {
	return s.repo.ParseReference(reference)
}

// generateDescriptor returns a descriptor generated from the response.
// See the truth table at the top of `repository_test.go`
func (s *manifestStore) generateDescriptor(resp *http.Response, ref registry.Reference, httpMethod string) (ocispec.Descriptor, error) {
	// 1. Validate Content-Type
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf(
			"%s %q: invalid response `Content-Type` header; %w",
			resp.Request.Method,
			resp.Request.URL,
			err,
		)
	}

	// 2. Validate Size
	if resp.ContentLength == -1 {
		return ocispec.Descriptor{}, fmt.Errorf(
			"%s %q: unknown response Content-Length",
			resp.Request.Method,
			resp.Request.URL,
		)
	}

	// 3. Validate Client Reference
	var refDigest digest.Digest
	if d, err := ref.Digest(); err == nil {
		refDigest = d
	}

	// 4. Validate Server Digest (if present)
	var serverHeaderDigest digest.Digest
	if serverHeaderDigestStr := resp.Header.Get(dockerContentDigestHeader); serverHeaderDigestStr != "" {
		if serverHeaderDigest, err = digest.Parse(serverHeaderDigestStr); err != nil {
			return ocispec.Descriptor{}, fmt.Errorf(
				"%s %q: invalid response header value `%s`: `%s`; %w",
				resp.Request.Method,
				resp.Request.URL,
				dockerContentDigestHeader,
				serverHeaderDigestStr,
				err,
			)
		}
	}

	/* 5. Now, look for specific error conditions; see truth table in method docstring */
	var contentDigest digest.Digest

	if len(serverHeaderDigest) == 0 {
		if httpMethod == http.MethodHead {
			if len(refDigest) == 0 {
				// HEAD without server `Docker-Content-Digest` header is an
				// immediate fail
				return ocispec.Descriptor{}, fmt.Errorf(
					"HTTP %s request missing required header `%s`",
					httpMethod, dockerContentDigestHeader,
				)
			}
			// Otherwise, just trust the client-supplied digest
			contentDigest = refDigest
		} else {
			// GET without server `Docker-Content-Digest` header forces the
			// expensive calculation
			var calculatedDigest digest.Digest
			if calculatedDigest, err = calculateDigestFromResponse(resp, s.repo.MaxMetadataBytes); err != nil {
				return ocispec.Descriptor{}, fmt.Errorf("failed to calculate digest on response body; %w", err)
			}
			contentDigest = calculatedDigest
		}
	} else {
		contentDigest = serverHeaderDigest
	}

	if len(refDigest) > 0 && refDigest != contentDigest {
		return ocispec.Descriptor{}, fmt.Errorf(
			"%s %q: invalid response; digest mismatch: `%s: %s` vs expected `%s`",
			resp.Request.Method, resp.Request.URL,
			dockerContentDigestHeader, contentDigest,
			refDigest,
		)
	}

	// 6. Finally, if we made it this far, then all is good; return.
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    contentDigest,
		Size:      resp.ContentLength,
	}, nil
}

// calculateDigestFromResponse calculates the actual digest of the response body
// taking care not to destroy it in the process.
func calculateDigestFromResponse(resp *http.Response, maxMetadataBytes int64) (digest.Digest, error) {
	defer resp.Body.Close()

	body := limitReader(resp.Body, maxMetadataBytes)
	content, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("%s %q: failed to read response body: %w", resp.Request.Method, resp.Request.URL, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(content))

	return digest.FromBytes(content), nil
}

// verifyContentDigest verifies "Docker-Content-Digest" header if present.
// OCI distribution-spec states the Docker-Content-Digest header is optional.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.0.1/spec.md#legacy-docker-support-http-headers
func verifyContentDigest(resp *http.Response, expected digest.Digest) error {
	digestStr := resp.Header.Get(dockerContentDigestHeader)

	if len(digestStr) == 0 {
		return nil
	}

	contentDigest, err := digest.Parse(digestStr)
	if err != nil {
		return fmt.Errorf(
			"%s %q: invalid response header: `%s: %s`",
			resp.Request.Method, resp.Request.URL,
			dockerContentDigestHeader, digestStr,
		)
	}

	if contentDigest != expected {
		return fmt.Errorf(
			"%s %q: invalid response; digest mismatch: `%s: %s` vs expected `%s`",
			resp.Request.Method, resp.Request.URL,
			dockerContentDigestHeader, contentDigest,
			expected,
		)
	}

	return nil
}

// verifyOrasApiVersion verifies "ORAS-Api-Version" header if present.
// Reference: https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md#versioning
func verifyOrasApiVersion(resp *http.Response) error {
	versionStr := resp.Header.Get("ORAS-Api-Version")
	if !referrersApiRegex.MatchString(versionStr) {
		return fmt.Errorf("%w: Unsupported ORAS-Api-Version: %q", errdef.ErrUnsupportedVersion, versionStr)
	}
	return nil
}
