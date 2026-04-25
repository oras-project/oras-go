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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/cas"
	"github.com/oras-project/oras-go/v3/internal/httputil"
	"github.com/oras-project/oras-go/v3/internal/ioutil"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/errutil"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

const (
	// headerDockerContentDigest is the "Docker-Content-Digest" header.
	// If present on the response, it contains the canonical digest of the
	// uploaded blob.
	//
	// References:
	//   - https://distribution.github.io/distribution/spec/api/#digest-header
	//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pull
	headerDockerContentDigest = "Docker-Content-Digest"

	// headerOCIFiltersApplied is the "OCI-Filters-Applied" header.
	// If present on the response, it contains a comma-separated list of the
	// applied filters.
	//
	// Reference:
	//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
	headerOCIFiltersApplied = "OCI-Filters-Applied"

	// headerOCISubject is the "OCI-Subject" header.
	// If present on the response, it contains the digest of the subject,
	// indicating that Referrers API is supported by the registry.
	headerOCISubject = "OCI-Subject"
)

// filterTypeArtifactType is the "artifactType" filter applied on the list of
// referrers.
//
// References:
//   - Latest spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
//   - Compatible spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc1/spec.md#listing-referrers
const filterTypeArtifactType = "artifactType"

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
	// Registry is the parent registry. Must not be nil.
	// Registry holds shared configuration like Client, PlainHTTP, Policy, etc.
	Registry *Registry

	// RepositoryName is the name within the registry (e.g., "library/alpine").
	RepositoryName string

	// ManifestMediaTypes overrides Registry default if set.
	// Used in `Accept` header for resolving manifests from references.
	// It is also used in identifying manifests and blobs from descriptors.
	// If an empty list is present, default manifest media types are used.
	ManifestMediaTypes []string

	// TagListPageSize overrides Registry default if set.
	// Specifies the page size when invoking the tag list API.
	// If zero, the page size is determined by the remote registry.
	// Reference: https://distribution.github.io/distribution/spec/api/#tags
	TagListPageSize int

	// ReferrerListPageSize overrides Registry default if set.
	// Specifies the page size when invoking the Referrers API.
	// If zero, the page size is determined by the remote registry.
	//
	// NOTE: Pagination for the Referrers API is not defined in the distribution
	// spec, so not all registries support it. ReferrerListPageSize may be
	// ignored if pagination is unsupported by the remote registry.
	//
	// Reference: https://github.com/oras-project/oras-go/issues/841
	ReferrerListPageSize int

	// TagListMaxPages overrides Registry default if set.
	// Limits the total number of pages fetched during tag listing.
	// Zero means unlimited (use Registry.TagListMaxPages if set).
	TagListMaxPages int

	// ReferrerListMaxPages overrides Registry default if set.
	// Limits the total number of pages fetched during referrer listing.
	// Zero means unlimited (use Registry.ReferrerListMaxPages if set).
	ReferrerListMaxPages int

	// SkipReferrersGC overrides Registry default if set.
	// Specifies whether to delete the dangling referrers
	// index when referrers tag schema is utilized.
	//  - If false, the old referrers index will be deleted after the new one
	//    is successfully uploaded.
	//  - If true, the old referrers index is kept.
	// By default, it is disabled (set to false). See also:
	//  - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#referrers-tag-schema
	//  - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-manifests-with-subject
	//  - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#deleting-manifests
	SkipReferrersGC bool

	// mirrors is an ordered list of mirror repositories to try before the
	// primary. Only read operations (Resolve, Fetch, FetchReference, Exists)
	// fall back through mirrors.
	mirrors []mirrorRepository

	// NOTE: Must keep fields in sync with clone().

	// referrersCapability tracks if the repository supports Referrers API.
	// Lazily initialized via atomic CAS on first access; safe for concurrent use.
	referrersCapability atomic.Pointer[ReferrerCapability]

	// referrersPingLock locks the pingReferrers() method and allows only
	// one go-routine to send the request.
	referrersPingLock sync.Mutex

	// referrersMergePool provides a way to manage concurrent updates to a
	// referrers index tagged by referrers tag schema.
	// Lazily initialized via atomic CAS on first access; safe for concurrent use.
	referrersMergePool atomic.Pointer[ReferrerMergePool]
}

// NewRepository creates a client to the remote repository identified by a
// reference. A default Registry is created internally.
// Example: localhost:5000/hello-world
func NewRepository(reference string) (*Repository, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		return nil, err
	}

	// Create a default Registry
	reg := &Registry{
		Reference: registry.Reference{
			Registry: ref.Registry,
		},
	}

	return &Repository{
		Registry:       reg,
		RepositoryName: ref.Repository,
	}, nil
}

// Reference returns the full registry.Reference for this repository.
func (r *Repository) Reference() registry.Reference {
	return registry.Reference{
		Registry:   r.Registry.Reference.Registry,
		Repository: r.RepositoryName,
	}
}

// clone makes a copy of the Repository being careful not to copy non-copyable fields (sync.Mutex and syncutil.Pool types)
func (r *Repository) clone() *Repository {
	return &Repository{
		Registry:             r.Registry,
		RepositoryName:       r.RepositoryName,
		ManifestMediaTypes:   slices.Clone(r.ManifestMediaTypes),
		TagListPageSize:      r.TagListPageSize,
		ReferrerListPageSize: r.ReferrerListPageSize,
		TagListMaxPages:      r.TagListMaxPages,
		ReferrerListMaxPages: r.ReferrerListMaxPages,
		SkipReferrersGC:      r.SkipReferrersGC,
		mirrors:              r.mirrors,
	}
}

// client returns an HTTP client used to access the remote repository.
// A default HTTP client is returned if the client is not configured.
func (r *Repository) client() Client {
	if r.Registry == nil || r.Registry.Client == nil {
		return auth.DefaultClient
	}
	return r.Registry.Client
}

// plainHTTP returns whether plain HTTP should be used.
func (r *Repository) plainHTTP() bool {
	if r.Registry == nil {
		return false
	}
	return r.Registry.PlainHTTP
}

// maxMetadataBytes returns the maximum metadata bytes limit.
func (r *Repository) maxMetadataBytes() int64 {
	if r.Registry == nil {
		return 0
	}
	return r.Registry.MaxMetadataBytes
}

// handleWarning returns the warning handler function.
func (r *Repository) handleWarning() func(warning Warning) {
	if r.Registry == nil {
		return nil
	}
	return r.Registry.HandleWarning
}

// policy returns the policy evaluator.
func (r *Repository) policy() *policy.Evaluator {
	if r.Registry == nil {
		return nil
	}
	return r.Registry.Policy
}

// tagListPageSize returns the effective tag list page size.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) tagListPageSize() int {
	if r.TagListPageSize > 0 {
		return r.TagListPageSize
	}
	if r.Registry != nil {
		return r.Registry.TagListPageSize
	}
	return 0
}

// referrerListPageSize returns the effective referrer list page size.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) referrerListPageSize() int {
	if r.ReferrerListPageSize > 0 {
		return r.ReferrerListPageSize
	}
	if r.Registry != nil {
		return r.Registry.ReferrerListPageSize
	}
	return 0
}

// tagListMaxPages returns the effective maximum number of tag list pages.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) tagListMaxPages() int {
	if r.TagListMaxPages > 0 {
		return r.TagListMaxPages
	}
	if r.Registry != nil {
		return r.Registry.TagListMaxPages
	}
	return 0
}

// referrerListMaxPages returns the effective maximum number of referrer list pages.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) referrerListMaxPages() int {
	if r.ReferrerListMaxPages > 0 {
		return r.ReferrerListMaxPages
	}
	if r.Registry != nil {
		return r.Registry.ReferrerListMaxPages
	}
	return 0
}

// manifestMediaTypes returns the effective manifest media types.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) manifestMediaTypes() []string {
	if len(r.ManifestMediaTypes) > 0 {
		return r.ManifestMediaTypes
	}
	if r.Registry != nil {
		return r.Registry.ManifestMediaTypes
	}
	return nil
}

// skipReferrersGC returns the effective skip referrers GC setting.
// Repository-level setting takes precedence over Registry default.
func (r *Repository) skipReferrersGC() bool {
	if r.SkipReferrersGC {
		return true
	}
	if r.Registry != nil {
		return r.Registry.SkipReferrersGC
	}
	return false
}

// SetReferrersCapability indicates the Referrers API capability of the remote
// repository. true: capable; false: not capable.
//
// SetReferrersCapability has no effect if the capability has already been set
// to the same value. If the capability has been set to a conflicting value it
// is silently ignored; the first value set wins.
//   - When the capability is set to true, the Referrers() function will always
//     request the Referrers API. Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
//   - When the capability is set to false, the Referrers() function will always
//     request the Referrers Tag. Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#referrers-tag-schema
//   - When the capability is not set, the Referrers() function will automatically
//     determine which API to use.
func (r *Repository) SetReferrersCapability(capable bool) {
	cap := r.getReferrersCapability()
	if capable {
		cap.TrySetSupported()
	} else {
		cap.TrySetUnsupported()
	}
}

// getReferrersCapability returns the referrers capability, initializing it if needed.
// Safe for concurrent use: uses atomic CAS to ensure only one instance is created.
func (r *Repository) getReferrersCapability() *ReferrerCapability {
	if cap := r.referrersCapability.Load(); cap != nil {
		return cap
	}
	r.referrersCapability.CompareAndSwap(nil, NewReferrerCapability())
	return r.referrersCapability.Load()
}

// loadReferrersState atomically loads r.referrersState.
func (r *Repository) loadReferrersState() referrersState {
	return r.getReferrersCapability().State()
}

// getReferrersMergePool returns the referrers merge pool, initializing it if needed.
// Safe for concurrent use: uses atomic CAS to ensure only one instance is created.
func (r *Repository) getReferrersMergePool() *ReferrerMergePool {
	if pool := r.referrersMergePool.Load(); pool != nil {
		return pool
	}
	r.referrersMergePool.CompareAndSwap(nil, NewReferrerMergePool())
	return r.referrersMergePool.Load()
}

// do sends an HTTP request and returns an HTTP response using the HTTP client
// returned by r.client().
func (r *Repository) do(req *http.Request) (*http.Response, error) {
	handler := r.handleWarning()
	if handler == nil {
		return r.client().Do(req)
	}

	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	handleWarningHeaders(resp.Header.Values(headerWarning), handler)
	return resp, nil
}

// blobStore detects the blob store for the given descriptor.
func (r *Repository) blobStore(desc ocispec.Descriptor) registry.BlobStore {
	if isManifest(r.manifestMediaTypes(), desc) {
		return r.Manifests()
	}
	return r.Blobs()
}

// policyCheckedKey is a context key that marks policy as already checked
// in the current call chain, preventing redundant evaluations when
// Repository methods delegate to sub-stores.
type policyCheckedKey struct{}

// withPolicyChecked returns a context that marks policy as already checked.
func withPolicyChecked(ctx context.Context) context.Context {
	return context.WithValue(ctx, policyCheckedKey{}, true)
}

// checkPolicy validates the repository access against the configured policy.
// If no policy is configured (Policy is nil), this is a no-op.
//
// Policy is enforced on all registry operations including:
//   - Content operations: Fetch, Push, Resolve, FetchReference, PushReference
//   - Metadata operations: Exists, Tags, Referrers, Predecessors
//   - Mutating operations: Delete, Tag, Mount
//
// For signedBy/sigstoreSigned requirements, signature verification is
// performed regardless of operation type. If this is too restrictive for
// administrative operations, configure a separate policy scope.
func (r *Repository) checkPolicy(ctx context.Context, reference string) error {
	if ctx.Value(policyCheckedKey{}) != nil {
		return nil
	}

	pol := r.policy()
	if pol == nil {
		return nil
	}

	repoRef := r.Reference()
	ref := repoRef.String()
	if reference != "" {
		ref = reference
	}

	imageRef := policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     repoRef.Registry + "/" + repoRef.Repository,
		Reference: ref,
	}

	allowed, err := pol.IsImageAllowed(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("policy check failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("access denied by policy for %s", ref)
	}
	return nil
}

// Fetch fetches the content identified by the descriptor.
// If mirrors are configured, they are tried in order before the primary.
func (r *Repository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return nil, err
	}
	ctx = withPolicyChecked(ctx)
	if len(r.mirrors) > 0 {
		return withMirrorFallbackFetch(ctx, r.mirrors, r, target,
			func(ctx context.Context, repo *Repository, t ocispec.Descriptor) (io.ReadCloser, error) {
				return repo.blobStore(t).Fetch(ctx, t)
			})
	}
	return r.blobStore(target).Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (r *Repository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return err
	}
	return r.blobStore(expected).Push(withPolicyChecked(ctx), expected, content)
}

// Mount makes the blob with the given digest in fromRepo
// available in the repository signified by the receiver.
//
// This avoids the need to pull content down from fromRepo only to push it to r.
//
// If the registry does not implement mounting, getContent will be used to get the
// content to push. If getContent is nil, the content will be pulled from the source
// repository. If getContent returns an error, it will be wrapped inside the error
// returned from Mount.
func (r *Repository) Mount(ctx context.Context, desc ocispec.Descriptor, fromRepo string, getContent func() (io.ReadCloser, error)) error {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return err
	}
	mounter, ok := r.Blobs().(registry.Mounter)
	if !ok {
		return errors.New("blob store does not support mounting")
	}
	return mounter.Mount(withPolicyChecked(ctx), desc, fromRepo, getContent)
}

// Exists returns true if the described content exists.
// If mirrors are configured, they are tried in order before the primary.
func (r *Repository) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return false, err
	}
	ctx = withPolicyChecked(ctx)
	if len(r.mirrors) > 0 {
		return withMirrorFallbackExists(ctx, r.mirrors, r, target,
			func(ctx context.Context, repo *Repository, t ocispec.Descriptor) (bool, error) {
				return repo.blobStore(t).Exists(ctx, t)
			})
	}
	return r.blobStore(target).Exists(ctx, target)
}

// Delete removes the content identified by the descriptor.
func (r *Repository) Delete(ctx context.Context, target ocispec.Descriptor) error {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return err
	}
	return r.blobStore(target).Delete(withPolicyChecked(ctx), target)
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
// If mirrors are configured, they are tried in order before the primary.
// See also `ManifestMediaTypes`.
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = withPolicyChecked(ctx)
	if len(r.mirrors) > 0 {
		return withMirrorFallbackResolve(ctx, r.mirrors, r, reference,
			func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, error) {
				return repo.Manifests().Resolve(ctx, ref)
			})
	}
	return r.Manifests().Resolve(ctx, reference)
}

// Tag tags a manifest descriptor with a reference string.
func (r *Repository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.Manifests().Tag(withPolicyChecked(ctx), desc, reference)
}

// PushReference pushes the manifest with a reference tag.
func (r *Repository) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.Manifests().PushReference(withPolicyChecked(ctx), expected, content, reference)
}

// FetchReference fetches the manifest identified by the reference.
// The reference can be a tag or digest.
// If mirrors are configured, they are tried in order before the primary.
func (r *Repository) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	ctx = withPolicyChecked(ctx)
	if len(r.mirrors) > 0 {
		return withMirrorFallbackFetchReference(ctx, r.mirrors, r, reference,
			func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, io.ReadCloser, error) {
				return repo.Manifests().FetchReference(ctx, ref)
			})
	}
	return r.Manifests().FetchReference(ctx, reference)
}

// ParseReference resolves a tag or a digest reference to a fully qualified
// reference from a base reference r.Reference().
// Tag, digest, or fully qualified references are accepted as input.
//
// If reference is a fully qualified reference, then ParseReference parses it
// and returns the parsed reference. If the parsed reference does not share
// the same base reference with the Repository r, ParseReference returns a
// wrapped error ErrInvalidReference.
func (r *Repository) ParseReference(reference string) (registry.Reference, error) {
	repoRef := r.Reference()
	ref, err := registry.ParseReference(reference)
	if err != nil {
		ref = registry.Reference{
			Registry:   repoRef.Registry,
			Repository: repoRef.Repository,
			Reference:  reference,
		}

		// reference is not a FQDN
		if index := strings.IndexByte(reference, '@'); index != -1 {
			// `@` implies *digest*, so drop the *tag* (irrespective of what it is).
			ref.Reference = reference[index+1:]
			err = ref.ValidateReferenceAsDigest()
		} else {
			err = ref.ValidateReference()
		}

		if err != nil {
			return registry.Reference{}, err
		}
	} else if ref.Registry != repoRef.Registry || ref.Repository != repoRef.Repository {
		return registry.Reference{}, fmt.Errorf(
			"%w: mismatch between received %q and expected %q",
			errdef.ErrInvalidReference, ref, repoRef,
		)
	}

	if len(ref.Reference) == 0 {
		return registry.Reference{}, errdef.ErrInvalidReference
	}

	return ref, nil
}

// Tags lists the tags available in the repository.
// See also `TagListPageSize`.
// If `last` is NOT empty, the entries in the response start after the
// tag specified by `last`. Otherwise, the response starts from the top
// of the Tags list.
//
// References:
//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#content-discovery
//   - https://distribution.github.io/distribution/spec/api/#tags
func (r *Repository) Tags(ctx context.Context, last string, fn func(tags []string) error) error {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return err
	}
	repoRef := r.Reference()
	ctx = auth.AppendRepositoryScope(ctx, repoRef, auth.ActionPull)
	url := buildRepositoryTagListURL(r.plainHTTP(), repoRef)
	var err error
	maxPages := r.tagListMaxPages()
	for page := 0; err == nil; page++ {
		if maxPages > 0 && page >= maxPages {
			return fmt.Errorf("tag listing exceeded %d pages: %w", maxPages, errdef.ErrTooManyPages)
		}
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
	pageSize := r.tagListPageSize()
	if pageSize > 0 || last != "" {
		q := req.URL.Query()
		if pageSize > 0 {
			q.Set("n", strconv.Itoa(pageSize))
		}
		if last != "" {
			q.Set("last", last)
		}
		req.URL.RawQuery = q.Encode()
	}
	resp, err := r.do(req)
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
	lr := limitReader(resp.Body, r.maxMetadataBytes())
	if err := json.NewDecoder(lr).Decode(&page); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	if err := fn(page.Tags); err != nil {
		return "", err
	}

	return parseLink(resp)
}

// Predecessors returns the descriptors of image or artifact manifests directly
// referencing the given manifest descriptor.
// Predecessors internally leverages Referrers.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
func (r *Repository) Predecessors(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return nil, err
	}
	ctx = withPolicyChecked(ctx)
	var res []ocispec.Descriptor
	if err := r.Referrers(ctx, desc, "", func(referrers []ocispec.Descriptor) error {
		res = append(res, referrers...)
		return nil
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// Referrers lists the descriptors of image or artifact manifests directly
// referencing the given manifest descriptor.
//
// fn is called for each page of the referrers result.
// If artifactType is not empty, only referrers of the same artifact type are
// fed to fn.
//
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
func (r *Repository) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	if err := r.checkPolicy(ctx, ""); err != nil {
		return err
	}
	state := r.loadReferrersState()
	if state == referrersStateUnsupported {
		// The repository is known to not support Referrers API, fallback to
		// referrers tag schema.
		return r.referrersByTagSchema(ctx, desc, artifactType, fn)
	}

	err := r.referrersByAPI(ctx, desc, artifactType, fn)
	if state == referrersStateSupported {
		// The repository is known to support Referrers API, no fallback.
		return err
	}

	// The referrers state is unknown.
	if err != nil {
		if errors.Is(err, errdef.ErrUnsupported) {
			// Referrers API is not supported, fallback to referrers tag schema.
			r.SetReferrersCapability(false)
			return r.referrersByTagSchema(ctx, desc, artifactType, fn)
		}
		return err
	}

	r.SetReferrersCapability(true)
	return nil
}

// referrersByAPI lists the descriptors of manifests directly referencing
// the given manifest descriptor by requesting Referrers API.
// fn is called for the referrers result. If artifactType is not empty,
// only referrers of the same artifact type are fed to fn.
func (r *Repository) referrersByAPI(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	repoRef := r.Reference()
	ref := repoRef
	ref.Reference = desc.Digest.String()
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)

	url := buildReferrersURL(r.plainHTTP(), ref, artifactType)
	var err error
	maxPages := r.referrerListMaxPages()
	for page := 0; err == nil; page++ {
		if maxPages > 0 && page >= maxPages {
			return fmt.Errorf("referrer listing exceeded %d pages: %w", maxPages, errdef.ErrTooManyPages)
		}
		url, err = r.referrersPageByAPI(ctx, artifactType, fn, url)
	}
	if err == errNoLink {
		return nil
	}
	return err
}

// referrersPageByAPI lists a single page of the descriptors of manifests
// directly referencing the given manifest descriptor. fn is called for
// a page of referrersPageByAPI result.
// If artifactType is not empty, only referrersPageByAPI of the same
// artifact type are fed to fn.
// referrersPageByAPI returns the link url for the next page.
func (r *Repository) referrersPageByAPI(ctx context.Context, artifactType string, fn func(referrers []ocispec.Descriptor) error, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	pageSize := r.referrerListPageSize()
	if pageSize > 0 {
		q := req.URL.Query()
		q.Set("n", strconv.Itoa(pageSize))
		req.URL.RawQuery = q.Encode()
	}

	resp, err := r.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		if errResp := errutil.ParseErrorResponse(resp); errutil.IsErrorCode(errResp, errcode.ErrorCodeNameUnknown) {
			// The repository is not found, Referrers API status is unknown
			return "", errResp
		}
		// Referrers API is not supported.
		return "", fmt.Errorf("failed to query referrers API: %w", errdef.ErrUnsupported)
	default:
		return "", errutil.ParseErrorResponse(resp)
	}

	// also check the content type
	if ct := resp.Header.Get("Content-Type"); ct != ocispec.MediaTypeImageIndex {
		return "", fmt.Errorf("unknown content returned (%s), expecting image index: %w", ct, errdef.ErrUnsupported)
	}

	var index ocispec.Index
	lr := limitReader(resp.Body, r.maxMetadataBytes())
	if err := json.NewDecoder(lr).Decode(&index); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}

	referrers := index.Manifests
	if artifactType != "" {
		// check both filters header and filters annotations for compatibility
		// latest spec for filters header: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#listing-referrers
		// older spec for filters annotations: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc1/spec.md#listing-referrers
		filtersHeader := resp.Header.Get(headerOCIFiltersApplied)
		filtersAnnotation := index.Annotations[spec.AnnotationReferrersFiltersApplied]
		if !isReferrersFilterApplied(filtersHeader, filterTypeArtifactType) &&
			!isReferrersFilterApplied(filtersAnnotation, filterTypeArtifactType) {
			// perform client side filtering if the filter is not applied on the server side
			referrers = filterReferrers(referrers, artifactType)
		}
	}
	if len(referrers) > 0 {
		if err := fn(referrers); err != nil {
			return "", err
		}
	}
	return parseLink(resp)
}

// referrersByTagSchema lists the descriptors of manifests directly
// referencing the given manifest descriptor by requesting referrers tag.
// fn is called for the referrers result. If artifactType is not empty,
// only referrers of the same artifact type are fed to fn.
// reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#backwards-compatibility
func (r *Repository) referrersByTagSchema(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	referrersTag, err := buildReferrersTag(desc)
	if err != nil {
		return err
	}
	_, referrers, err := r.referrersFromIndex(ctx, referrersTag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			// no referrers to the manifest
			return nil
		}
		return err
	}

	filtered := filterReferrers(referrers, artifactType)
	if len(filtered) == 0 {
		return nil
	}
	return fn(filtered)
}

// referrersFromIndex queries the referrers index using the the given referrers
// tag. If Succeeded, returns the descriptor of referrers index and the
// referrers list.
func (r *Repository) referrersFromIndex(ctx context.Context, referrersTag string) (ocispec.Descriptor, []ocispec.Descriptor, error) {
	desc, rc, err := r.FetchReference(ctx, referrersTag)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer rc.Close()

	if err := limitSize(desc, r.maxMetadataBytes()); err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to read referrers index from referrers tag %s: %w", referrersTag, err)
	}
	var index ocispec.Index
	if err := decodeJSON(rc, desc, &index); err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to decode referrers index from referrers tag %s: %w", referrersTag, err)
	}

	return desc, index.Manifests, nil
}

// pingReferrers returns true if the Referrers API is available for r.
func (r *Repository) pingReferrers(ctx context.Context) (bool, error) {
	switch r.loadReferrersState() {
	case referrersStateSupported:
		return true, nil
	case referrersStateUnsupported:
		return false, nil
	}

	// referrers state is unknown
	// limit the rate of pinging referrers API
	r.referrersPingLock.Lock()
	defer r.referrersPingLock.Unlock()

	switch r.loadReferrersState() {
	case referrersStateSupported:
		return true, nil
	case referrersStateUnsupported:
		return false, nil
	}

	repoRef := r.Reference()
	ref := repoRef
	ref.Reference = zeroDigest
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)

	url := buildReferrersURL(r.plainHTTP(), ref, "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := r.do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		supported := resp.Header.Get("Content-Type") == ocispec.MediaTypeImageIndex
		r.SetReferrersCapability(supported)
		return supported, nil
	case http.StatusNotFound:
		if err := errutil.ParseErrorResponse(resp); errutil.IsErrorCode(err, errcode.ErrorCodeNameUnknown) {
			// repository not found
			return false, err
		}
		r.SetReferrersCapability(false)
		return false, nil
	default:
		return false, errutil.ParseErrorResponse(resp)
	}
}

// delete removes the content identified by the descriptor in the entity "blobs"
// or "manifests".
func (r *Repository) delete(ctx context.Context, target ocispec.Descriptor, isManifest bool) error {
	repoRef := r.Reference()
	ref := repoRef
	ref.Reference = target.Digest.String()
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionDelete)
	buildURL := buildRepositoryBlobURL
	if isManifest {
		buildURL = buildRepositoryManifestURL
	}
	url := buildURL(r.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := r.do(req)
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return nil, err
	}
	repoRef := s.repo.Reference()
	ref := repoRef
	ref.Reference = target.Digest.String()
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.repo.do(req)
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
		if size := resp.ContentLength; size != -1 && size != target.Size {
			return nil, fmt.Errorf("%s %q: mismatch Content-Length", resp.Request.Method, resp.Request.URL)
		}
		if err := verifyContentDigest(resp, target.Digest); err != nil {
			return nil, err
		}

		// check server range request capability.
		// Docker spec allows range header form of "Range: bytes=<start>-<end>".
		// However, the remote server may still not RFC 7233 compliant.
		// Reference: https://distribution.github.io/distribution/spec/api/#blob
		if rangeUnit := resp.Header.Get("Accept-Ranges"); rangeUnit == "bytes" {
			return httputil.NewReadSeekCloser(s.repo.client(), req, resp.Body, target.Size), nil
		}
		return resp.Body, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", target.Digest, errdef.ErrNotFound)
	default:
		return nil, errutil.ParseErrorResponse(resp)
	}
}

// Mount mounts the given descriptor from fromRepo into s.
func (s *blobStore) Mount(ctx context.Context, desc ocispec.Descriptor, fromRepo string, getContent func() (io.ReadCloser, error)) error {
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return err
	}
	// pushing usually requires both pull and push actions.
	// Reference: https://github.com/distribution/distribution/blob/v2.7.1/registry/handlers/app.go#L921-L930
	repoRef := s.repo.Reference()
	ctx = auth.AppendRepositoryScope(ctx, repoRef, auth.ActionPull, auth.ActionPush)

	// We also need pull access to the source repo.
	fromRef := repoRef
	fromRef.Repository = fromRepo
	ctx = auth.AppendRepositoryScope(ctx, fromRef, auth.ActionPull)

	url := buildRepositoryBlobMountURL(s.repo.plainHTTP(), repoRef, desc.Digest, fromRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.repo.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusCreated {
		defer resp.Body.Close()
		// Check the server seems to be behaving.
		return verifyContentDigest(resp, desc.Digest)
	}
	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		return errutil.ParseErrorResponse(resp)
	}
	resp.Body.Close()
	// From the [spec]:
	//
	// "If a registry does not support cross-repository mounting
	// or is unable to mount the requested blob,
	// it SHOULD return a 202.
	// This indicates that the upload session has begun
	// and that the client MAY proceed with the upload."
	//
	// So we need to get the content from somewhere in order to
	// push it. If the caller has provided a getContent function, we
	// can use that, otherwise pull the content from the source repository.
	//
	// [spec]: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#mounting-a-blob-from-another-repository

	var r io.ReadCloser
	if getContent != nil {
		r, err = getContent()
	} else {
		r, err = s.sibling(fromRepo).Fetch(ctx, desc)
	}
	if err != nil {
		return fmt.Errorf("cannot read source blob: %w", err)
	}
	defer r.Close()
	return s.completePushAfterInitialPost(ctx, req, resp, desc, r)
}

// sibling returns a blob store for another repository in the same
// registry.
func (s *blobStore) sibling(otherRepoName string) *blobStore {
	otherRepo := s.repo.clone()
	otherRepo.RepositoryName = otherRepoName
	return &blobStore{
		repo: otherRepo,
	}
}

// Push pushes the content, matching the expected descriptor.
// Existing content is not checked by Push() to minimize the number of out-going
// requests.
// Push is done by conventional 2-step monolithic upload instead of a single
// `POST` request for better overall performance. It also allows early fail on
// authentication errors.
//
// References:
//   - https://distribution.github.io/distribution/spec/api/#pushing-an-image
//   - https://distribution.github.io/distribution/spec/api/#initiate-blob-upload
//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-a-blob-monolithically
func (s *blobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return err
	}
	// start an upload
	// pushing usually requires both pull and push actions.
	// Reference: https://github.com/distribution/distribution/blob/v2.7.1/registry/handlers/app.go#L921-L930
	repoRef := s.repo.Reference()
	ctx = auth.AppendRepositoryScope(ctx, repoRef, auth.ActionPull, auth.ActionPush)
	url := buildRepositoryBlobUploadURL(s.repo.plainHTTP(), repoRef)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}

	resp, err := s.repo.do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		return errutil.ParseErrorResponse(resp)
	}
	resp.Body.Close()
	return s.completePushAfterInitialPost(ctx, req, resp, expected, content)
}

// completePushAfterInitialPost implements step 2 of the push protocol. This can be invoked either by
// Push or by Mount when the receiving repository does not implement the
// mount endpoint.
func (s *blobStore) completePushAfterInitialPost(ctx context.Context, req *http.Request, resp *http.Response, expected ocispec.Descriptor, content io.Reader) error {
	reqHostname := req.URL.Hostname()
	reqPort := req.URL.Port()
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
	url := location.String()
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
	resp, err = s.repo.do(req)
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return false, err
	}
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return err
	}
	return s.repo.delete(ctx, target, false)
}

// Resolve resolves a reference to a descriptor.
func (s *blobStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return ocispec.Descriptor{}, err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	refDigest, err := ref.GetDigest()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	resp, err := s.repo.do(req)
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	refDigest, err := ref.GetDigest()
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryBlobURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	resp, err := s.repo.do(req)
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
		if resp.ContentLength == -1 {
			desc, err = s.Resolve(ctx, reference)
		} else {
			desc, err = generateBlobDescriptor(resp, refDigest)
		}
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}

		// check server range request capability.
		// Docker spec allows range header form of "Range: bytes=<start>-<end>".
		// However, the remote server may still not RFC 7233 compliant.
		// Reference: https://distribution.github.io/distribution/spec/api/#blob
		if rangeUnit := resp.Header.Get("Accept-Ranges"); rangeUnit == "bytes" {
			return desc, httputil.NewReadSeekCloser(s.repo.client(), req, resp.Body, desc.Size), nil
		}
		return desc, resp.Body, nil
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return nil, err
	}
	repoRef := s.repo.Reference()
	ref := repoRef
	ref.Reference = target.Digest.String()
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", target.MediaType)

	resp, err := s.repo.do(req)
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
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return err
	}
	return s.pushWithIndexing(ctx, expected, content, expected.Digest.String())
}

// Exists returns true if the described content exists.
func (s *manifestStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return false, err
	}
	_, err := s.Resolve(ctx, target.Digest.String())
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes the manifest content identified by the descriptor.
func (s *manifestStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	if err := s.repo.checkPolicy(ctx, ""); err != nil {
		return err
	}
	return s.deleteWithIndexing(ctx, target)
}

// deleteWithIndexing removes the manifest content identified by the descriptor,
// and indexes referrers for the manifest when needed.
func (s *manifestStore) deleteWithIndexing(ctx context.Context, target ocispec.Descriptor) error {
	switch target.MediaType {
	case spec.MediaTypeArtifactManifest, ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
		if state := s.repo.loadReferrersState(); state == referrersStateSupported {
			// referrers API is available, no client-side indexing needed
			return s.repo.delete(ctx, target, true)
		}

		if err := limitSize(target, s.repo.maxMetadataBytes()); err != nil {
			return err
		}
		ctx = auth.AppendRepositoryScope(ctx, s.repo.Reference(), auth.ActionPull, auth.ActionDelete)
		manifestJSON, err := content.FetchAll(ctx, s, target)
		if err != nil {
			return err
		}
		if err := s.indexReferrersForDelete(ctx, target, manifestJSON); err != nil {
			return err
		}
	}

	return s.repo.delete(ctx, target, true)
}

// indexReferrersForDelete indexes referrers for manifests with a subject field
// on manifest delete.
//
// References:
//   - Latest spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#deleting-manifests
//   - Compatible spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc1/spec.md#deleting-manifests
func (s *manifestStore) indexReferrersForDelete(ctx context.Context, desc ocispec.Descriptor, manifestJSON []byte) error {
	var manifest struct {
		Subject *ocispec.Descriptor `json:"subject"`
	}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return fmt.Errorf("failed to decode manifest: %s: %s: %w", desc.Digest, desc.MediaType, err)
	}
	if manifest.Subject == nil {
		// no subject, no indexing needed
		return nil
	}

	subject := *manifest.Subject
	ok, err := s.repo.pingReferrers(ctx)
	if err != nil {
		return err
	}
	if ok {
		// referrers API is available, no client-side indexing needed
		return nil
	}
	return s.updateReferrersIndex(ctx, subject, referrerChange{desc, referrerOperationRemove})
}

// Resolve resolves a reference to a descriptor.
// See also `ManifestMediaTypes`.
func (s *manifestStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	req.Header.Set("Accept", manifestAcceptHeader(s.repo.manifestMediaTypes()))

	resp, err := s.repo.do(req)
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
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull)
	url := buildRepositoryManifestURL(s.repo.plainHTTP(), ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	req.Header.Set("Accept", manifestAcceptHeader(s.repo.manifestMediaTypes()))

	resp, err := s.repo.do(req)
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
		if resp.ContentLength == -1 {
			desc, err = s.Resolve(ctx, reference)
		} else {
			desc, err = s.generateDescriptor(resp, ref, req.Method)
		}
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		return desc, resp.Body, nil
	case http.StatusNotFound:
		return ocispec.Descriptor{}, nil, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	default:
		return ocispec.Descriptor{}, nil, errutil.ParseErrorResponse(resp)
	}
}

// Tag tags a manifest descriptor with a reference string.
func (s *manifestStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return err
	}

	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull, auth.ActionPush)
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	return s.push(ctx, desc, rc, ref.Reference)
}

// PushReference pushes the manifest with a reference tag.
func (s *manifestStore) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	ref, err := s.repo.ParseReference(reference)
	if err != nil {
		return err
	}
	return s.pushWithIndexing(ctx, expected, content, ref.Reference)
}

// push pushes the manifest content, matching the expected descriptor.
func (s *manifestStore) push(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	repoRef := s.repo.Reference()
	ref := repoRef
	ref.Reference = reference
	// pushing usually requires both pull and push actions.
	// Reference: https://github.com/distribution/distribution/blob/v2.7.1/registry/handlers/app.go#L921-L930
	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull, auth.ActionPush)
	url := buildRepositoryManifestURL(s.repo.plainHTTP(), ref)
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
	resp, err := s.repo.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return errutil.ParseErrorResponse(resp)
	}
	s.checkOCISubjectHeader(resp)
	return verifyContentDigest(resp, expected.Digest)
}

// checkOCISubjectHeader checks the "OCI-Subject" header in the response and
// sets referrers capability accordingly.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-manifests-with-subject
func (s *manifestStore) checkOCISubjectHeader(resp *http.Response) {
	// If the "OCI-Subject" header is set, it indicates that the registry
	// supports the Referrers API and has processed the subject of the manifest.
	if subjectHeader := resp.Header.Get(headerOCISubject); subjectHeader != "" {
		s.repo.SetReferrersCapability(true)
	}

	// If the "OCI-Subject" header is NOT set, it means that either the manifest
	// has no subject OR the referrers API is NOT supported by the registry.
	//
	// Since we don't know whether the pushed manifest has a subject or not,
	// we do not set the referrers capability to false at here.
}

// pushWithIndexing pushes the manifest content matching the expected descriptor,
// and indexes referrers for the manifest when needed.
func (s *manifestStore) pushWithIndexing(ctx context.Context, expected ocispec.Descriptor, r io.Reader, reference string) error {
	switch expected.MediaType {
	case spec.MediaTypeArtifactManifest, ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
		if state := s.repo.loadReferrersState(); state == referrersStateSupported {
			// referrers API is available, no client-side indexing needed
			return s.push(ctx, expected, r, reference)
		}

		if err := limitSize(expected, s.repo.maxMetadataBytes()); err != nil {
			return err
		}
		manifestJSON, err := content.ReadAll(r, expected)
		if err != nil {
			return err
		}
		if err := s.push(ctx, expected, bytes.NewReader(manifestJSON), reference); err != nil {
			return err
		}
		// check referrers API availability again after push
		if state := s.repo.loadReferrersState(); state == referrersStateSupported {
			// the subject has been processed the registry, no client-side
			// indexing needed
			return nil
		}
		return s.indexReferrersForPush(ctx, expected, manifestJSON)
	default:
		return s.push(ctx, expected, r, reference)
	}
}

// indexReferrersForPush indexes referrers for manifests with a subject field
// on manifest push.
//
// References:
//   - Latest spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-manifests-with-subject
//   - Compatible spec: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc1/spec.md#pushing-manifests-with-subject
func (s *manifestStore) indexReferrersForPush(ctx context.Context, desc ocispec.Descriptor, manifestJSON []byte) error {
	var subject ocispec.Descriptor
	switch desc.MediaType {
	case spec.MediaTypeArtifactManifest:
		var manifest spec.Artifact
		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			return fmt.Errorf("failed to decode manifest: %s: %s: %w", desc.Digest, desc.MediaType, err)
		}
		if manifest.Subject == nil {
			// no subject, no indexing needed
			return nil
		}
		subject = *manifest.Subject
		desc.ArtifactType = manifest.ArtifactType
		desc.Annotations = manifest.Annotations
	case ocispec.MediaTypeImageManifest:
		var manifest ocispec.Manifest
		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			return fmt.Errorf("failed to decode manifest: %s: %s: %w", desc.Digest, desc.MediaType, err)
		}
		if manifest.Subject == nil {
			// no subject, no indexing needed
			return nil
		}
		subject = *manifest.Subject
		desc.ArtifactType = manifest.ArtifactType
		if desc.ArtifactType == "" {
			desc.ArtifactType = manifest.Config.MediaType
		}
		desc.Annotations = manifest.Annotations
	case ocispec.MediaTypeImageIndex:
		var manifest ocispec.Index
		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			return fmt.Errorf("failed to decode manifest: %s: %s: %w", desc.Digest, desc.MediaType, err)
		}
		if manifest.Subject == nil {
			// no subject, no indexing needed
			return nil
		}
		subject = *manifest.Subject
		desc.ArtifactType = manifest.ArtifactType
		desc.Annotations = manifest.Annotations
	default:
		return nil
	}

	// if the manifest has a subject but the remote registry does not process it,
	// it means that the Referrers API is not supported by the registry.
	s.repo.SetReferrersCapability(false)
	return s.updateReferrersIndex(ctx, subject, referrerChange{desc, referrerOperationAdd})
}

// updateReferrersIndex updates the referrers index for desc referencing subject
// on manifest push and manifest delete.
// References:
//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-manifests-with-subject
//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#deleting-manifests
func (s *manifestStore) updateReferrersIndex(ctx context.Context, subject ocispec.Descriptor, change referrerChange) (err error) {
	referrersTag, err := buildReferrersTag(subject)
	if err != nil {
		return err
	}

	var oldIndexDesc *ocispec.Descriptor
	var oldReferrers []ocispec.Descriptor
	prepare := func() error {
		// 1. pull the original referrers list using the referrers tag schema
		indexDesc, referrers, err := s.repo.referrersFromIndex(ctx, referrersTag)
		if err != nil {
			if errors.Is(err, errdef.ErrNotFound) {
				// valid case: no old referrers index
				return nil
			}
			return err
		}
		oldIndexDesc = &indexDesc
		oldReferrers = referrers
		return nil
	}
	update := func(referrerChanges []referrerChange) error {
		// 2. apply the referrer changes on the referrers list
		updatedReferrers, err := applyReferrerChanges(oldReferrers, referrerChanges)
		if err != nil {
			if err == errNoReferrerUpdate {
				return nil
			}
			return err
		}

		// 3. push the updated referrers list using referrers tag schema
		if len(updatedReferrers) > 0 || s.repo.skipReferrersGC() {
			// push a new index in either case:
			// 1. the referrers list has been updated with a non-zero size
			// 2. OR the updated referrers list is empty but referrers GC
			//    is skipped, in this case an empty index should still be pushed
			//    as the old index won't get deleted
			newIndexDesc, newIndex, err := generateIndex(updatedReferrers)
			if err != nil {
				return fmt.Errorf("failed to generate referrers index for referrers tag %s: %w", referrersTag, err)
			}
			if err := s.push(ctx, newIndexDesc, bytes.NewReader(newIndex), referrersTag); err != nil {
				return fmt.Errorf("failed to push referrers index tagged by %s: %w", referrersTag, err)
			}
		}

		// 4. delete the dangling original referrers index, if applicable
		if s.repo.skipReferrersGC() || oldIndexDesc == nil {
			return nil
		}
		if err := s.repo.delete(ctx, *oldIndexDesc, true); err != nil {
			return &ReferrersError{
				Op:      opDeleteReferrersIndex,
				Err:     fmt.Errorf("failed to delete dangling referrers index %s for referrers tag %s: %w", oldIndexDesc.Digest.String(), referrersTag, err),
				Subject: subject,
			}
		}
		return nil
	}

	merge, done := s.repo.getReferrersMergePool().Get(referrersTag)
	defer done()
	return merge.Do(change, prepare, update)
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
	if d, err := ref.GetDigest(); err == nil {
		refDigest = d
	}

	// 4. Validate Server Digest (if present)
	var serverHeaderDigest digest.Digest
	if serverHeaderDigestStr := resp.Header.Get(headerDockerContentDigest); serverHeaderDigestStr != "" {
		if serverHeaderDigest, err = digest.Parse(serverHeaderDigestStr); err != nil {
			return ocispec.Descriptor{}, fmt.Errorf(
				"%s %q: invalid response header value: `%s: %s`; %w",
				resp.Request.Method,
				resp.Request.URL,
				headerDockerContentDigest,
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
					"HTTP %s request missing required header %q",
					httpMethod, headerDockerContentDigest,
				)
			}
			// Otherwise, just trust the client-supplied digest
			contentDigest = refDigest
		} else {
			// GET without server `Docker-Content-Digest` header forces the
			// expensive calculation
			var calculatedDigest digest.Digest
			if calculatedDigest, err = calculateDigestFromResponse(resp, s.repo.maxMetadataBytes()); err != nil {
				return ocispec.Descriptor{}, fmt.Errorf("failed to calculate digest on response body; %w", err)
			}
			contentDigest = calculatedDigest
		}
	} else {
		contentDigest = serverHeaderDigest
	}

	if len(refDigest) > 0 && refDigest != contentDigest {
		return ocispec.Descriptor{}, fmt.Errorf(
			"%s %q: invalid response; digest mismatch in %s: received %q when expecting %q",
			resp.Request.Method, resp.Request.URL,
			headerDockerContentDigest, contentDigest,
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
	digestStr := resp.Header.Get(headerDockerContentDigest)

	if len(digestStr) == 0 {
		return nil
	}

	contentDigest, err := digest.Parse(digestStr)
	if err != nil {
		return fmt.Errorf(
			"%s %q: invalid response header: `%s: %s`",
			resp.Request.Method, resp.Request.URL,
			headerDockerContentDigest, digestStr,
		)
	}

	if contentDigest != expected {
		return fmt.Errorf(
			"%s %q: invalid response; digest mismatch in %s: received %q when expecting %q",
			resp.Request.Method, resp.Request.URL,
			headerDockerContentDigest, contentDigest,
			expected,
		)
	}

	return nil
}

// generateIndex generates an image index containing the given manifests list.
func generateIndex(manifests []ocispec.Descriptor) (ocispec.Descriptor, []byte, error) {
	if manifests == nil {
		manifests = []ocispec.Descriptor{} // make it an empty array to prevent potential server-side bugs
	}
	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	indexDesc := content.NewDescriptorFromBytes(index.MediaType, indexJSON)
	return indexDesc, indexJSON, nil
}
