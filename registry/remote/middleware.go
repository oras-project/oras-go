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
func WithPolicyEnforcement(evaluator *policy.Evaluator, transport policy.TransportName, scope string) RepositoryMiddleware {
	return func(repo registry.Repository) registry.Repository {
		return &policyEnforcingRepository{
			Repository: repo,
			evaluator:  evaluator,
			transport:  transport,
			scope:      scope,
		}
	}
}

// policyEnforcingRepository wraps a Repository and enforces policy on all operations.
type policyEnforcingRepository struct {
	registry.Repository
	evaluator *policy.Evaluator
	transport policy.TransportName
	scope     string
}

// checkPolicy validates access against the configured policy.
func (r *policyEnforcingRepository) checkPolicy(ctx context.Context, reference string) error {
	if r.evaluator == nil {
		return nil
	}

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
	return r.Repository.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor, with policy enforcement.
func (r *policyEnforcingRepository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := r.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return r.Repository.Push(ctx, expected, content)
}

// Resolve resolves a reference to a manifest descriptor with policy enforcement.
func (r *policyEnforcingRepository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	return r.Repository.Resolve(ctx, reference)
}

// Tag tags a manifest descriptor with a reference string with policy enforcement.
func (r *policyEnforcingRepository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.Repository.Tag(ctx, desc, reference)
}

// FetchReference fetches the manifest identified by the reference with policy enforcement.
func (r *policyEnforcingRepository) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return r.Repository.FetchReference(ctx, reference)
}

// PushReference pushes the manifest with a reference tag with policy enforcement.
func (r *policyEnforcingRepository) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return r.Repository.PushReference(ctx, expected, content, reference)
}

// Blobs returns a policy-enforcing blob store.
func (r *policyEnforcingRepository) Blobs() registry.BlobStore {
	return &policyEnforcingBlobStore{
		BlobStore: r.Repository.Blobs(),
		repo:      r,
	}
}

// Manifests returns a policy-enforcing manifest store.
func (r *policyEnforcingRepository) Manifests() registry.ManifestStore {
	return &policyEnforcingManifestStore{
		ManifestStore: r.Repository.Manifests(),
		repo:          r,
	}
}

// policyEnforcingBlobStore wraps a BlobStore with policy enforcement.
type policyEnforcingBlobStore struct {
	registry.BlobStore
	repo *policyEnforcingRepository
}

// Fetch fetches the content with policy enforcement.
func (s *policyEnforcingBlobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, target.Digest.String()); err != nil {
		return nil, err
	}
	return s.BlobStore.Fetch(ctx, target)
}

// Push pushes the content with policy enforcement.
func (s *policyEnforcingBlobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.repo.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return s.BlobStore.Push(ctx, expected, content)
}

// FetchReference fetches the blob identified by the reference with policy enforcement.
func (s *policyEnforcingBlobStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return s.BlobStore.FetchReference(ctx, reference)
}

// policyEnforcingManifestStore wraps a ManifestStore with policy enforcement.
type policyEnforcingManifestStore struct {
	registry.ManifestStore
	repo *policyEnforcingRepository
}

// Fetch fetches the content with policy enforcement.
func (s *policyEnforcingManifestStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, target.Digest.String()); err != nil {
		return nil, err
	}
	return s.ManifestStore.Fetch(ctx, target)
}

// Push pushes the content with policy enforcement.
func (s *policyEnforcingManifestStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.repo.checkPolicy(ctx, expected.Digest.String()); err != nil {
		return err
	}
	return s.ManifestStore.Push(ctx, expected, content)
}

// FetchReference fetches the manifest identified by the reference with policy enforcement.
func (s *policyEnforcingManifestStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return s.ManifestStore.FetchReference(ctx, reference)
}

// PushReference pushes the manifest with a reference tag with policy enforcement.
func (s *policyEnforcingManifestStore) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return s.ManifestStore.PushReference(ctx, expected, content, reference)
}

// Tag tags a manifest descriptor with a reference string with policy enforcement.
func (s *policyEnforcingManifestStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if err := s.repo.checkPolicy(ctx, reference); err != nil {
		return err
	}
	return s.ManifestStore.Tag(ctx, desc, reference)
}

