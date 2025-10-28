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
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configuration"
)

var testReference = registry.Reference{
	Registry:   "localhost:5000",
	Repository: "test/repo",
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
		pol := &configuration.Policy{
			Default: configuration.PolicyRequirements{&configuration.Reject{}},
		}
		evaluator, err := configuration.NewEvaluator(pol)
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
		pol := &configuration.Policy{
			Default: configuration.PolicyRequirements{&configuration.InsecureAcceptAnything{}},
		}
		evaluator, err := configuration.NewEvaluator(pol)
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

func TestRepository_Clone_Policy(t *testing.T) {
	pol := &configuration.Policy{
		Default: configuration.PolicyRequirements{&configuration.InsecureAcceptAnything{}},
	}
	evaluator, err := configuration.NewEvaluator(pol)
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

func TestRepository_Fetch_PolicyCheck(t *testing.T) {
	// This test verifies that Fetch calls checkPolicy
	// We use a reject policy to ensure Fetch fails before attempting network calls
	pol := &configuration.Policy{
		Default: configuration.PolicyRequirements{&configuration.Reject{}},
	}
	evaluator, err := configuration.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err = repo.Fetch(context.Background(), desc)
	if err == nil {
		t.Error("Fetch() should fail due to reject policy")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("Fetch() error should mention access denied, got: %v", err)
	}
}

func TestRepository_Push_PolicyCheck(t *testing.T) {
	// This test verifies that Push calls checkPolicy
	pol := &configuration.Policy{
		Default: configuration.PolicyRequirements{&configuration.Reject{}},
	}
	evaluator, err := configuration.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	content := strings.NewReader("test content")

	err = repo.Push(context.Background(), desc, content)
	if err == nil {
		t.Error("Push() should fail due to reject policy")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("Push() error should mention access denied, got: %v", err)
	}
}

func TestRepository_Resolve_PolicyCheck(t *testing.T) {
	// This test verifies that Resolve calls checkPolicy
	pol := &configuration.Policy{
		Default: configuration.PolicyRequirements{&configuration.Reject{}},
	}
	evaluator, err := configuration.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
	}

	_, err = repo.Resolve(context.Background(), "latest")
	if err == nil {
		t.Error("Resolve() should fail due to reject policy")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("Resolve() error should mention access denied, got: %v", err)
	}
}

func TestRepository_ScopeSpecificPolicy(t *testing.T) {
	// Test that scope-specific policies work correctly
	pol := &configuration.Policy{
		Default: configuration.PolicyRequirements{&configuration.Reject{}},
		Transports: map[configuration.TransportName]configuration.TransportScopes{
			configuration.TransportDocker: {
				// Allow all docker repositories
				"": configuration.PolicyRequirements{&configuration.InsecureAcceptAnything{}},
			},
		},
	}

	evaluator, err := configuration.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	repo := &Repository{
		Reference: testReference,
		Policy:    evaluator,
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
