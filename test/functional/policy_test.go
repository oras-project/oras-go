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

//go:build functional

package functional_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

// TestPolicy_Fetch_Reject verifies that Fetch is blocked by a reject policy.
func TestPolicy_Fetch_Reject(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	// Push content without any policy so the blob exists in the registry.
	repo := newRepository(t, repoName)
	data := []byte("reject policy fetch test")
	desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

	// Apply a reject-all policy and try to fetch.
	repoWithPolicy := newRepository(t, repoName)
	evaluator, err := policy.NewEvaluator(policy.NewRejectAllPolicy())
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	repoWithPolicy.Registry.Policy = evaluator

	_, err = repoWithPolicy.Fetch(ctx, desc)
	if err == nil {
		t.Fatal("Fetch should fail with reject policy, got nil error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied' in error, got: %v", err)
	}
}

// TestPolicy_Push_Reject verifies that Push is blocked by a reject policy.
func TestPolicy_Push_Reject(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	repo := newRepository(t, repoName)
	evaluator, err := policy.NewEvaluator(policy.NewRejectAllPolicy())
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	repo.Registry.Policy = evaluator

	data := []byte("should not be pushed")
	desc := content.NewDescriptorFromBytes("application/octet-stream", data)

	err = repo.Push(ctx, desc, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Push should fail with reject policy, got nil error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied' in error, got: %v", err)
	}
}

// TestPolicy_Resolve_Reject verifies that Resolve is blocked by a reject policy.
func TestPolicy_Resolve_Reject(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	repo := newRepository(t, repoName)
	evaluator, err := policy.NewEvaluator(policy.NewRejectAllPolicy())
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	repo.Registry.Policy = evaluator

	_, err = repo.Resolve(ctx, "latest")
	if err == nil {
		t.Fatal("Resolve should fail with reject policy, got nil error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied' in error, got: %v", err)
	}
}

// TestPolicy_Accept verifies that push and fetch succeed with an accept-all policy.
func TestPolicy_Accept(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	repo := newRepository(t, repoName)
	evaluator, err := policy.NewEvaluator(policy.NewInsecureAcceptAnythingPolicy())
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	repo.Registry.Policy = evaluator

	data := []byte("accept policy test content")
	desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

	fetchAndVerify(t, ctx, repo, desc, data)
}

// TestPolicy_ScopeSpecific_AcceptOverridesDefaultReject verifies that a
// scope-specific accept requirement overrides the global reject default.
// checkPolicy uses repoRef.Repository as the policy scope.
func TestPolicy_ScopeSpecific_AcceptOverridesDefaultReject(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	// Push content without policy so the blob exists.
	repo := newRepository(t, repoName)
	data := []byte("scope-specific accept policy test")
	desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

	// Global default = reject, but allow this specific repository scope.
	// checkPolicy builds the scope as "registry/repository", so include the host.
	scope := registryHost + "/" + repoName
	pol := policy.NewPolicy().
		SetDefault(&policy.Reject{}).
		SetTransportScope(policy.TransportNameDocker, scope, &policy.InsecureAcceptAnything{})
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	repoWithPolicy := newRepository(t, repoName)
	repoWithPolicy.Registry.Policy = evaluator

	fetchAndVerify(t, ctx, repoWithPolicy, desc, data)
}

// TestPolicy_ScopeSpecific_RejectOverridesDefaultAccept verifies that a
// scope-specific reject requirement overrides the global accept default.
func TestPolicy_ScopeSpecific_RejectOverridesDefaultAccept(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)

	// Push content without policy so the blob exists.
	repo := newRepository(t, repoName)
	data := []byte("scope-specific reject policy test")
	desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

	// Global default = accept, but reject this specific repository scope.
	// checkPolicy builds the scope as "registry/repository", so include the host.
	scope := registryHost + "/" + repoName
	pol := policy.NewPolicy().
		SetDefault(&policy.InsecureAcceptAnything{}).
		SetTransportScope(policy.TransportNameDocker, scope, &policy.Reject{})
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	repoWithPolicy := newRepository(t, repoName)
	repoWithPolicy.Registry.Policy = evaluator

	_, err = repoWithPolicy.Fetch(ctx, desc)
	if err == nil {
		t.Fatal("Fetch should fail with scope-specific reject, got nil error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied' in error, got: %v", err)
	}
}
