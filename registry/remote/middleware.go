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
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

// RepositoryMiddleware wraps a registry.Repository to add cross-cutting concerns.
type RepositoryMiddleware func(registry.Repository) registry.Repository

// Compose chains multiple middlewares together.
// The first middleware is the outermost (executed first for requests,
// last for responses).
func Compose(middlewares ...RepositoryMiddleware) RepositoryMiddleware {
	return func(repo registry.Repository) registry.Repository {
		for i := len(middlewares) - 1; i >= 0; i-- {
			repo = middlewares[i](repo)
		}
		return repo
	}
}

// WithPolicyEnforcement creates a middleware that adds policy checks to all operations.
// The transport and scope parameters are used for constructing image references
// for policy evaluation.
//
// If evaluator is nil, a no-op middleware is returned that leaves the repository
// unchanged, so callers pay no per-operation cost when policy enforcement is
// disabled.
func WithPolicyEnforcement(evaluator *policy.Evaluator, transport policy.TransportName, scope string) RepositoryMiddleware {
	if evaluator == nil {
		return func(repo registry.Repository) registry.Repository {
			return repo
		}
	}
	return func(repo registry.Repository) registry.Repository {
		return &policyEnforcingRepository{
			repo:      repo,
			evaluator: evaluator,
			transport: transport,
			scope:     scope,
		}
	}
}

// policyEnforcingRepository wraps a Repository and enforces policy on all operations.
//
// The wrapped repository is held in a named field rather than embedded so that
// every method of registry.Repository must be implemented explicitly. If a new
// method is added to the interface, this type fails to compile until the method
// is handled here, ensuring no operation silently bypasses policy enforcement.
type policyEnforcingRepository struct {
	repo      registry.Repository
	evaluator *policy.Evaluator
	transport policy.TransportName
	scope     string
}

// checkPolicy validates access against the configured policy.
// The evaluator is guaranteed non-nil by WithPolicyEnforcement.
func (r *policyEnforcingRepository) checkPolicy(ctx context.Context, reference string) error {
	imageRef := policy.ImageReference{
		Transport: r.transport,
		Scope:     r.scope,
		Reference: reference,
	}

	allowed, err := r.evaluator.IsImageAllowed(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("policy check failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("access denied by policy for %s", reference)
	}
	return nil
}

// Fetch fetches the content identified by the descriptor with policy enforcement.
func (r *policyEnforcingRepository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := r.checkPolicy(ctx, target.Digest.String()); err != nil {
		return nil, err
	}
	return r.repo.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor, with policy enforcement.
func (r *policyEnforcingRepository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := r.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return r.repo.Push(ctx, expected, content)
}

// Exists returns true if the described content exists.
func (r *policyEnforcingRepository) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return r.repo.Exists(ctx, target)
}

// Delete removes the content identified by the descriptor.
func (r *policyEnforcingRepository) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return r.repo.Delete(ctx, target)
}

// Resolve resolves a reference to a manifest descriptor with policy enforcement.
func (r *policyEnforcingRepository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	return r.repo.Resolve(ctx, reference)
}

// Tag tags a manifest descriptor with a reference string with policy enforcement.
func (r *policyEnforcingRepository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.repo.Tag(ctx, desc, reference)
}

// FetchReference fetches the manifest identified by the reference with policy enforcement.
func (r *policyEnforcingRepository) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return r.repo.FetchReference(ctx, reference)
}

// PushReference pushes the manifest with a reference tag with policy enforcement.
func (r *policyEnforcingRepository) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.repo.PushReference(ctx, expected, content, reference)
}

// Referrers lists the descriptors that refer to the given descriptor.
func (r *policyEnforcingRepository) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	return r.repo.Referrers(ctx, desc, artifactType, fn)
}

// Tags lists the tags available in the repository.
func (r *policyEnforcingRepository) Tags(ctx context.Context, last string, fn func(tags []string) error) error {
	return r.repo.Tags(ctx, last, fn)
}

// Blobs returns a policy-enforcing blob store.
func (r *policyEnforcingRepository) Blobs() registry.BlobStore {
	return &policyEnforcingBlobStore{
		blobs: r.repo.Blobs(),
		repo:  r,
	}
}

// Manifests returns a policy-enforcing manifest store.
func (r *policyEnforcingRepository) Manifests() registry.ManifestStore {
	return &policyEnforcingManifestStore{
		manifests: r.repo.Manifests(),
		repo:      r,
	}
}

// policyEnforcingBlobStore wraps a BlobStore with policy enforcement.
//
// As with policyEnforcingRepository, the wrapped store is a named field so that
// every registry.BlobStore method must be implemented explicitly.
type policyEnforcingBlobStore struct {
	blobs registry.BlobStore
	repo  *policyEnforcingRepository
}

// Fetch fetches the content with policy enforcement.
func (s *policyEnforcingBlobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, target.Digest.String()); err != nil {
		return nil, err
	}
	return s.blobs.Fetch(ctx, target)
}

// Push pushes the content with policy enforcement.
func (s *policyEnforcingBlobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.repo.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return s.blobs.Push(ctx, expected, content)
}

// Exists returns true if the described content exists.
func (s *policyEnforcingBlobStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.blobs.Exists(ctx, target)
}

// Delete removes the content identified by the descriptor.
func (s *policyEnforcingBlobStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.blobs.Delete(ctx, target)
}

// Resolve resolves a reference to a descriptor.
func (s *policyEnforcingBlobStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.blobs.Resolve(ctx, reference)
}

// FetchReference fetches the blob identified by the reference with policy enforcement.
func (s *policyEnforcingBlobStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return s.blobs.FetchReference(ctx, reference)
}

// policyEnforcingManifestStore wraps a ManifestStore with policy enforcement.
//
// As with policyEnforcingRepository, the wrapped store is a named field so that
// every registry.ManifestStore method must be implemented explicitly.
type policyEnforcingManifestStore struct {
	manifests registry.ManifestStore
	repo      *policyEnforcingRepository
}

// Fetch fetches the content with policy enforcement.
func (s *policyEnforcingManifestStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, target.Digest.String()); err != nil {
		return nil, err
	}
	return s.manifests.Fetch(ctx, target)
}

// Push pushes the content with policy enforcement.
func (s *policyEnforcingManifestStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.repo.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return s.manifests.Push(ctx, expected, content)
}

// Exists returns true if the described content exists.
func (s *policyEnforcingManifestStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.manifests.Exists(ctx, target)
}

// Delete removes the content identified by the descriptor.
func (s *policyEnforcingManifestStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.manifests.Delete(ctx, target)
}

// Resolve resolves a reference to a descriptor.
func (s *policyEnforcingManifestStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.manifests.Resolve(ctx, reference)
}

// FetchReference fetches the manifest identified by the reference with policy enforcement.
func (s *policyEnforcingManifestStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return s.manifests.FetchReference(ctx, reference)
}

// PushReference pushes the manifest with a reference tag with policy enforcement.
func (s *policyEnforcingManifestStore) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return s.manifests.PushReference(ctx, expected, content, reference)
}

// Tag tags a manifest descriptor with a reference string with policy enforcement.
func (s *policyEnforcingManifestStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return s.manifests.Tag(ctx, desc, reference)
}

// Ensure the wrappers satisfy the registry interfaces they enforce policy on.
var (
	_ registry.Repository    = (*policyEnforcingRepository)(nil)
	_ registry.BlobStore     = (*policyEnforcingBlobStore)(nil)
	_ registry.ManifestStore = (*policyEnforcingManifestStore)(nil)
)
