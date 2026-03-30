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
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

var testReference = registry.Reference{
	Registry:   "localhost:5000",
	Repository: "test/repo",
}

func newRejectPolicyRepo(t *testing.T) *Repository {
	t.Helper()
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	return &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}
}

func assertPolicyDenied(t *testing.T, err error, operation string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s should fail due to reject policy", operation)
		return
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("%s error should mention access denied, got: %v", operation, err)
	}
}

func TestRepository_PolicyEnforcement(t *testing.T) {
	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// Test without policy - should work
	t.Run("no policy", func(t *testing.T) {
		err := repo.checkPolicy(context.Background(), "")
		if err != nil {
			t.Errorf("checkPolicy() without policy should not error, got: %v", err)
		}
	})

	// Test with reject policy
	t.Run("reject policy", func(t *testing.T) {
		pol := &policy.Policy{
			Default: policy.PolicyRequirements{&policy.Reject{}},
		}
		evaluator, err := policy.NewEvaluator(pol)
		if err != nil {
			t.Fatalf("failed to create evaluator: %v", err)
		}
		reg.Policy = evaluator

		err = repo.checkPolicy(context.Background(), "")
		if err == nil {
			t.Error("checkPolicy() with reject policy should error, got nil")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error should mention access denied, got: %v", err)
		}
	})

	// Test with accept policy
	t.Run("accept policy", func(t *testing.T) {
		pol := &policy.Policy{
			Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
		}
		evaluator, err := policy.NewEvaluator(pol)
		if err != nil {
			t.Fatalf("failed to create evaluator: %v", err)
		}
		reg.Policy = evaluator

		err = repo.checkPolicy(context.Background(), "")
		if err != nil {
			t.Errorf("checkPolicy() with accept policy should not error, got: %v", err)
		}
	})
}

func TestRepository_PolicyCheckedContext(t *testing.T) {
	// Verify that policyCheckedKey in context skips the check
	repo := newRejectPolicyRepo(t)

	// Without the key, policy should reject
	err := repo.checkPolicy(context.Background(), "")
	if err == nil {
		t.Error("checkPolicy() should fail without policyCheckedKey")
	}

	// With the key set, policy should be skipped
	ctx := withPolicyChecked(context.Background())
	err = repo.checkPolicy(ctx, "")
	if err != nil {
		t.Errorf("checkPolicy() should be skipped with policyCheckedKey, got: %v", err)
	}
}

func TestRepository_PolicyScope(t *testing.T) {
	// Verify that the scope is fully qualified (registry/repository)
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				testReference.Registry + "/" + testReference.Repository: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// The fully-qualified scope should match, allowing access
	err = repo.checkPolicy(context.Background(), "")
	if err != nil {
		t.Errorf("checkPolicy() should succeed with fully-qualified scope match, got: %v", err)
	}
}

func TestRepository_Clone_Policy(t *testing.T) {
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	original := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	cloned := original.clone()

	if cloned.Registry.Policy != original.Registry.Policy {
		t.Error("cloned repository should have the same policy evaluator via Registry")
	}
}

func TestRepository_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Fetch(context.Background(), desc)
	assertPolicyDenied(t, err, "Fetch()")
}

func TestRepository_Push_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Push(context.Background(), desc, strings.NewReader("test content"))
	assertPolicyDenied(t, err, "Push()")
}

func TestRepository_Resolve_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	_, err := repo.Resolve(context.Background(), "latest")
	assertPolicyDenied(t, err, "Resolve()")
}

func TestRepository_Delete_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Delete(context.Background(), desc)
	assertPolicyDenied(t, err, "Delete()")
}

func TestRepository_Tag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Tag(context.Background(), desc, "v1.0")
	assertPolicyDenied(t, err, "Tag()")
}

func TestRepository_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.PushReference(context.Background(), desc, strings.NewReader("test content"), "v1.0")
	assertPolicyDenied(t, err, "PushReference()")
}

func TestRepository_FetchReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	_, _, err := repo.FetchReference(context.Background(), "latest")
	assertPolicyDenied(t, err, "FetchReference()")
}

func TestRepository_Exists_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Exists(context.Background(), desc)
	assertPolicyDenied(t, err, "Exists()")
}

func TestRepository_Tags_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	err := repo.Tags(context.Background(), "", func(tags []string) error {
		return nil
	})
	assertPolicyDenied(t, err, "Tags()")
}

func TestRepository_Referrers_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Referrers(context.Background(), desc, "", func(referrers []ocispec.Descriptor) error {
		return nil
	})
	assertPolicyDenied(t, err, "Referrers()")
}

func TestRepository_Predecessors_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Predecessors(context.Background(), desc)
	assertPolicyDenied(t, err, "Predecessors()")
}

func TestRepository_Mount_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Mount(context.Background(), desc, "source/repo", nil)
	assertPolicyDenied(t, err, "Mount()")
}

func TestRepository_ManifestStore_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Manifests().PushReference(context.Background(), desc, strings.NewReader("test content"), "v1.0")
	assertPolicyDenied(t, err, "Manifests().PushReference()")
}

func TestRepository_BlobStore_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Blobs().Fetch(context.Background(), desc)
	assertPolicyDenied(t, err, "Blobs().Fetch()")
}

func TestRepository_ScopeSpecificPolicy(t *testing.T) {
	// Test that scope-specific policies work correctly
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				// Allow all docker repositories
				"": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}

	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// Since the policy allows docker transport, checkPolicy should succeed
	err = repo.checkPolicy(context.Background(), "")
	if err != nil {
		t.Errorf("checkPolicy() should succeed for allowed docker transport, got: %v", err)
	}
}

// mockReadCloser is a simple mock for testing
type mockReadCloser struct {
	io.Reader
}

func (m *mockReadCloser) Close() error {
	return nil
}
