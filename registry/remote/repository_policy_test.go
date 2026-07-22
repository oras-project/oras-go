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
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
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
	return &Repository{
		Reference: testReference,
		Policy:    evaluator,
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

var testDesc = ocispec.Descriptor{
	MediaType: ocispec.MediaTypeImageManifest,
	Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	Size:      1234,
}

func TestRepository_PolicyEnforcement(t *testing.T) {
	repo := &Repository{
		Reference: testReference,
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
		repo.Policy = evaluator

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
		repo.Policy = evaluator

		err = repo.checkPolicy(context.Background(), "")
		if err != nil {
			t.Errorf("checkPolicy() with accept policy should not error, got: %v", err)
		}
	})
}

func TestRepository_PolicyCheckedContext(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	// Without context key, should fail
	err := repo.checkPolicy(context.Background(), "")
	if err == nil {
		t.Error("checkPolicy() should fail without policyCheckedKey")
	}

	// With context key, should be skipped
	ctx := withPolicyChecked(context.Background())
	err = repo.checkPolicy(ctx, "")
	if err != nil {
		t.Errorf("checkPolicy() should be skipped with policyCheckedKey, got: %v", err)
	}
}

func TestRepository_PolicyScope(t *testing.T) {
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				// Allow the fully-qualified scope
				"localhost:5000/test/repo": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	// Should succeed with fully-qualified scope match
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

	original := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	cloned := original.clone()

	if cloned.Policy != original.Policy {
		t.Error("cloned repository should have the same policy evaluator")
	}
}

// Tests for all Repository methods with reject policy.

func TestRepository_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Fetch(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Fetch")
}

func TestRepository_Push_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Push(context.Background(), testDesc, strings.NewReader("test"))
	assertPolicyDenied(t, err, "Push")
}

func TestRepository_Resolve_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Resolve(context.Background(), "latest")
	assertPolicyDenied(t, err, "Resolve")
}

func TestRepository_Delete_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Delete(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Delete")
}

func TestRepository_Tag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Tag(context.Background(), testDesc, "latest")
	assertPolicyDenied(t, err, "Tag")
}

func TestRepository_Untag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Untag(context.Background(), "latest")
	assertPolicyDenied(t, err, "Untag")
}

func TestRepository_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.PushReference(context.Background(), testDesc, strings.NewReader("test"), "latest")
	assertPolicyDenied(t, err, "PushReference")
}

func TestRepository_FetchReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, _, err := repo.FetchReference(context.Background(), "latest")
	assertPolicyDenied(t, err, "FetchReference")
}

func TestRepository_Exists_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Exists(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Exists")
}

func TestRepository_Tags_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Tags(context.Background(), "", func(tags []string) error { return nil })
	assertPolicyDenied(t, err, "Tags")
}

func TestRepository_Predecessors_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Predecessors(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Predecessors")
}

func TestRepository_Referrers_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Referrers(context.Background(), testDesc, "", func(referrers []ocispec.Descriptor) error { return nil })
	assertPolicyDenied(t, err, "Referrers")
}

// Tests for sub-store policy enforcement.

func TestRepository_ManifestStore_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Manifests().PushReference(context.Background(), testDesc, strings.NewReader("test"), "latest")
	assertPolicyDenied(t, err, "Manifests().PushReference")
}

func TestRepository_ManifestStore_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Manifests().Fetch(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Manifests().Fetch")
}

func TestRepository_ManifestStore_Resolve_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Manifests().Resolve(context.Background(), "latest")
	assertPolicyDenied(t, err, "Manifests().Resolve")
}

func TestRepository_ManifestStore_Untag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Manifests().(content.Untagger).Untag(context.Background(), "latest")
	assertPolicyDenied(t, err, "Manifests().Untag")
}

func TestRepository_BlobStore_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	_, err := repo.Blobs().Fetch(context.Background(), testDesc)
	assertPolicyDenied(t, err, "Blobs().Fetch")
}

func TestRepository_BlobStore_Push_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Blobs().Push(context.Background(), testDesc, strings.NewReader("test"))
	assertPolicyDenied(t, err, "Blobs().Push")
}

func TestRepository_ScopeSpecificPolicy(t *testing.T) {
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				"": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}

	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	err = repo.checkPolicy(context.Background(), "")
	if err != nil {
		t.Errorf("checkPolicy() should succeed for allowed docker transport, got: %v", err)
	}
}
