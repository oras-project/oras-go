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
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

func TestCompose(t *testing.T) {
	var order []string

	middleware1 := func(repo registry.Repository) registry.Repository {
		order = append(order, "m1-start")
		return &orderTrackingRepository{
			Repository: repo,
			name:       "m1",
			order:      &order,
		}
	}

	middleware2 := func(repo registry.Repository) registry.Repository {
		order = append(order, "m2-start")
		return &orderTrackingRepository{
			Repository: repo,
			name:       "m2",
			order:      &order,
		}
	}

	composed := Compose(middleware1, middleware2)

	baseRepo := &mockRepository{}
	wrapped := composed(baseRepo)

	// Trigger an operation to test the order
	ctx := context.Background()
	desc := ocispec.Descriptor{Digest: "sha256:test"}
	wrapped.Fetch(ctx, desc)

	// Verify order: middleware1 wraps middleware2 wraps base
	// So m1 should be outermost
	expected := []string{"m2-start", "m1-start", "m1-fetch", "m2-fetch"}
	if len(order) != len(expected) {
		t.Fatalf("order = %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %s, want %s", i, order[i], v)
		}
	}
}

func TestCompose_Empty(t *testing.T) {
	composed := Compose()

	baseRepo := &mockRepository{}
	wrapped := composed(baseRepo)

	if wrapped != baseRepo {
		t.Error("Compose() with no middlewares should return original repository")
	}
}

func TestWithPolicyEnforcement_NoEvaluator(t *testing.T) {
	baseRepo := &mockRepository{}
	middleware := WithPolicyEnforcement(nil, policy.TransportNameDocker, "test/repo")
	wrapped := middleware(baseRepo)

	// With nil evaluator, all operations should pass through
	ctx := context.Background()
	desc := ocispec.Descriptor{Digest: "sha256:test"}

	_, err := wrapped.Fetch(ctx, desc)
	if err != nil {
		t.Errorf("Fetch() error = %v, want nil", err)
	}
}

func TestWithPolicyEnforcement_PolicyDenied(t *testing.T) {
	// Create a policy that rejects everything
	pol := &policy.Policy{
		Default: []policy.PolicyRequirement{
			&policy.Reject{},
		},
		Transports: make(map[policy.TransportName]policy.TransportScopes),
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	baseRepo := &mockRepository{}
	middleware := WithPolicyEnforcement(evaluator, policy.TransportNameDocker, "test/repo")
	wrapped := middleware(baseRepo)

	ctx := context.Background()
	desc := ocispec.Descriptor{Digest: "sha256:test"}

	_, err = wrapped.Fetch(ctx, desc)
	if err == nil {
		t.Error("Fetch() should return error when policy denies access")
	}
	if !strings.Contains(err.Error(), "denied") && !strings.Contains(err.Error(), "policy") {
		t.Errorf("error should mention policy denial: %v", err)
	}
}

func TestWithPolicyEnforcement_PolicyAllowed(t *testing.T) {
	// Create a policy that allows everything
	pol := &policy.Policy{
		Default: []policy.PolicyRequirement{
			&policy.InsecureAcceptAnything{},
		},
		Transports: make(map[policy.TransportName]policy.TransportScopes),
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	baseRepo := &mockRepository{}
	middleware := WithPolicyEnforcement(evaluator, policy.TransportNameDocker, "test/repo")
	wrapped := middleware(baseRepo)

	ctx := context.Background()

	// Test Fetch
	desc := ocispec.Descriptor{Digest: "sha256:test"}
	_, err = wrapped.Fetch(ctx, desc)
	if err != nil {
		t.Errorf("Fetch() error = %v, want nil", err)
	}

	// Test Push
	err = wrapped.Push(ctx, desc, strings.NewReader("content"))
	if err != nil {
		t.Errorf("Push() error = %v, want nil", err)
	}

	// Test Resolve
	_, err = wrapped.Resolve(ctx, "latest")
	if err != nil {
		t.Errorf("Resolve() error = %v, want nil", err)
	}

	// Test Tag
	err = wrapped.Tag(ctx, desc, "latest")
	if err != nil {
		t.Errorf("Tag() error = %v, want nil", err)
	}

	// Test FetchReference
	_, _, err = wrapped.FetchReference(ctx, "latest")
	if err != nil {
		t.Errorf("FetchReference() error = %v, want nil", err)
	}

	// Test PushReference
	err = wrapped.PushReference(ctx, desc, strings.NewReader("content"), "latest")
	if err != nil {
		t.Errorf("PushReference() error = %v, want nil", err)
	}
}

func TestWithPolicyEnforcement_Blobs(t *testing.T) {
	pol := &policy.Policy{
		Default: []policy.PolicyRequirement{
			&policy.InsecureAcceptAnything{},
		},
		Transports: make(map[policy.TransportName]policy.TransportScopes),
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	baseRepo := &mockRepository{}
	middleware := WithPolicyEnforcement(evaluator, policy.TransportNameDocker, "test/repo")
	wrapped := middleware(baseRepo)

	ctx := context.Background()
	desc := ocispec.Descriptor{Digest: "sha256:test"}

	blobs := wrapped.Blobs()

	// Test Fetch
	_, err = blobs.Fetch(ctx, desc)
	if err != nil {
		t.Errorf("Blobs().Fetch() error = %v, want nil", err)
	}

	// Test Push
	err = blobs.Push(ctx, desc, strings.NewReader("content"))
	if err != nil {
		t.Errorf("Blobs().Push() error = %v, want nil", err)
	}

	// Test FetchReference
	_, _, err = blobs.FetchReference(ctx, "sha256:test")
	if err != nil {
		t.Errorf("Blobs().FetchReference() error = %v, want nil", err)
	}
}

func TestWithPolicyEnforcement_Manifests(t *testing.T) {
	pol := &policy.Policy{
		Default: []policy.PolicyRequirement{
			&policy.InsecureAcceptAnything{},
		},
		Transports: make(map[policy.TransportName]policy.TransportScopes),
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	baseRepo := &mockRepository{}
	middleware := WithPolicyEnforcement(evaluator, policy.TransportNameDocker, "test/repo")
	wrapped := middleware(baseRepo)

	ctx := context.Background()
	desc := ocispec.Descriptor{Digest: "sha256:test"}

	manifests := wrapped.Manifests()

	// Test Fetch
	_, err = manifests.Fetch(ctx, desc)
	if err != nil {
		t.Errorf("Manifests().Fetch() error = %v, want nil", err)
	}

	// Test Push
	err = manifests.Push(ctx, desc, strings.NewReader("content"))
	if err != nil {
		t.Errorf("Manifests().Push() error = %v, want nil", err)
	}

	// Test FetchReference
	_, _, err = manifests.FetchReference(ctx, "latest")
	if err != nil {
		t.Errorf("Manifests().FetchReference() error = %v, want nil", err)
	}

	// Test PushReference
	err = manifests.PushReference(ctx, desc, strings.NewReader("content"), "latest")
	if err != nil {
		t.Errorf("Manifests().PushReference() error = %v, want nil", err)
	}

	// Test Tag
	err = manifests.Tag(ctx, desc, "latest")
	if err != nil {
		t.Errorf("Manifests().Tag() error = %v, want nil", err)
	}
}

func TestWithWarningHandler(t *testing.T) {
	var receivedWarnings []Warning

	handler := func(w Warning) {
		receivedWarnings = append(receivedWarnings, w)
	}

	baseRepo := &mockRepository{}
	middleware := WithWarningHandler(handler)
	wrapped := middleware(baseRepo)

	// Verify the wrapped repository returns proper blob and manifest stores
	blobs := wrapped.Blobs()
	if blobs == nil {
		t.Error("Blobs() should not return nil")
	}

	manifests := wrapped.Manifests()
	if manifests == nil {
		t.Error("Manifests() should not return nil")
	}
}

// orderTrackingRepository tracks the order of middleware execution.
type orderTrackingRepository struct {
	registry.Repository
	name  string
	order *[]string
}

func (r *orderTrackingRepository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	*r.order = append(*r.order, r.name+"-fetch")
	return r.Repository.Fetch(ctx, target)
}

// mockRepository implements registry.Repository for testing.
type mockRepository struct {
	fetchErr         error
	pushErr          error
	existsResult     bool
	existsErr        error
	deleteErr        error
	resolveDesc      ocispec.Descriptor
	resolveErr       error
	tagErr           error
	pushReferenceErr error
	fetchReferenceRC io.ReadCloser
	fetchReferenceErr error
	referrersResult  []ocispec.Descriptor
	referrersErr     error
	tagsResult       []string
	tagsErr          error
	predecessorsResult []ocispec.Descriptor
	predecessorsErr  error
}

func (r *mockRepository) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if r.fetchErr != nil {
		return nil, r.fetchErr
	}
	return io.NopCloser(bytes.NewReader([]byte("content"))), nil
}

func (r *mockRepository) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return r.pushErr
}

func (r *mockRepository) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return r.existsResult, r.existsErr
}

func (r *mockRepository) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return r.deleteErr
}

func (r *mockRepository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return r.resolveDesc, r.resolveErr
}

func (r *mockRepository) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	return r.tagErr
}

func (r *mockRepository) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	return r.pushReferenceErr
}

func (r *mockRepository) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	if r.fetchReferenceErr != nil {
		return ocispec.Descriptor{}, nil, r.fetchReferenceErr
	}
	rc := r.fetchReferenceRC
	if rc == nil {
		rc = io.NopCloser(bytes.NewReader([]byte("content")))
	}
	return ocispec.Descriptor{Digest: digest.FromString("test")}, rc, nil
}

func (r *mockRepository) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func(referrers []ocispec.Descriptor) error) error {
	if r.referrersErr != nil {
		return r.referrersErr
	}
	if len(r.referrersResult) > 0 {
		return fn(r.referrersResult)
	}
	return nil
}

func (r *mockRepository) Tags(ctx context.Context, last string, fn func(tags []string) error) error {
	if r.tagsErr != nil {
		return r.tagsErr
	}
	if len(r.tagsResult) > 0 {
		return fn(r.tagsResult)
	}
	return nil
}

func (r *mockRepository) Predecessors(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return r.predecessorsResult, r.predecessorsErr
}

func (r *mockRepository) Blobs() registry.BlobStore {
	return &mockBlobStore{repo: r}
}

func (r *mockRepository) Manifests() registry.ManifestStore {
	return &mockManifestStore{repo: r}
}

// mockBlobStore implements registry.BlobStore for testing.
type mockBlobStore struct {
	repo *mockRepository
}

func (s *mockBlobStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.repo.Fetch(ctx, target)
}

func (s *mockBlobStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return s.repo.Push(ctx, expected, content)
}

func (s *mockBlobStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.repo.Exists(ctx, target)
}

func (s *mockBlobStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.repo.Delete(ctx, target)
}

func (s *mockBlobStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.repo.Resolve(ctx, reference)
}

func (s *mockBlobStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	return s.repo.FetchReference(ctx, reference)
}

// mockManifestStore implements registry.ManifestStore for testing.
type mockManifestStore struct {
	repo *mockRepository
}

func (s *mockManifestStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.repo.Fetch(ctx, target)
}

func (s *mockManifestStore) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return s.repo.Push(ctx, expected, content)
}

func (s *mockManifestStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.repo.Exists(ctx, target)
}

func (s *mockManifestStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	return s.repo.Delete(ctx, target)
}

func (s *mockManifestStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.repo.Resolve(ctx, reference)
}

func (s *mockManifestStore) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	return s.repo.FetchReference(ctx, reference)
}

func (s *mockManifestStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	return s.repo.Tag(ctx, desc, reference)
}

func (s *mockManifestStore) PushReference(ctx context.Context, expected ocispec.Descriptor, content io.Reader, reference string) error {
	return s.repo.PushReference(ctx, expected, content, reference)
}

// Ensure mock types implement the expected interfaces.
var (
	_ registry.Repository   = (*mockRepository)(nil)
	_ registry.BlobStore    = (*mockBlobStore)(nil)
	_ registry.ManifestStore = (*mockManifestStore)(nil)
)

// Suppress unused variable warning
var _ = errors.New
