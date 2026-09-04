//go:build functional

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

package functional_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// TestConfig_PolicyEvaluatorFromFile saves a reject-all policy to a file,
// loads it via LoadConfigsWithOptions, and verifies that the PolicyConfig is
// loaded and the evaluator rejects all images.
func TestConfig_PolicyEvaluatorFromFile(t *testing.T) {
	ctx := context.Background()

	// Save a reject-all policy to a temp file.
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	pol := policy.NewRejectAllPolicy()
	if err := pol.Save(policyPath); err != nil {
		t.Fatalf("failed to save reject-all policy: %v", err)
	}

	// Load configs with the policy path.
	cfgs, err := config.LoadConfigsWithOptions(config.LoadConfigsOptions{
		PolicyConfigPath: policyPath,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions failed: %v", err)
	}
	if cfgs.PolicyConfig == nil {
		t.Fatal("expected PolicyConfig to be loaded, got nil")
	}

	// Create evaluator from loaded config.
	evaluator, err := cfgs.PolicyEvaluator()
	if err != nil {
		t.Fatalf("PolicyEvaluator failed: %v", err)
	}
	if evaluator == nil {
		t.Fatal("expected non-nil evaluator")
	}

	// Verify the evaluator rejects all images.
	allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     "example.com/repo",
		Reference: "example.com/repo@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err != nil {
		t.Fatalf("IsImageAllowed returned error: %v", err)
	}
	if allowed {
		t.Fatal("expected reject-all policy to reject image, but it was allowed")
	}
}

// TestConfig_FullSignaturePipelineFromConfig exercises the full signature
// verification pipeline loaded from config files:
//   - Push an image to the registry
//   - Generate a GPG key and sign the image
//   - Write policy.json (PRSignedBy) and registries.d YAML
//   - Load configs via LoadConfigsWithOptions
//   - Create verifier from RegistriesDConfig
//   - Create evaluator from PolicyConfig with the verifier
//   - Assert the signed image is allowed
//   - Assert an image with a different scope (no stored signature) is rejected
func TestConfig_FullSignaturePipelineFromConfig(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)
	tag := "v1"

	// fullScope is the complete registry+repository path used as the lookaside
	// namespace and policy scope (e.g. "localhost:5000/functional/repo-xxx").
	fullScope := fmt.Sprintf("%s/%s", registryHost, repoName)

	desc, _ := pushManifest(t, ctx, repo, tag, nil)

	// Generate GPG key pair.
	entity, err := openpgp.NewEntity("Config Test User", "", "config-test@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity: %v", err)
	}
	keyPath := writeGPGKeyFile(t, entity)

	// Set up file-based lookaside store and sign the image.
	sigDir := t.TempDir()
	lookasideURL := "file://" + sigDir
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, fullScope, desc, tag, entity)

	// Write policy.json with PRSignedBy default.
	policyDir := t.TempDir()
	policyPath := filepath.Join(policyDir, "policy.json")
	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyPath,
	})
	if err := pol.Save(policyPath); err != nil {
		t.Fatalf("failed to save policy.json: %v", err)
	}

	// Write registries.d YAML to a temp directory.
	// The YAML configures the lookaside URL for registryHost.
	registriesDDir := t.TempDir()
	yamlContent := fmt.Sprintf(`docker:
  %s:
    lookaside: "%s"
`, registryHost, lookasideURL)
	yamlPath := filepath.Join(registriesDDir, "default.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write registries.d YAML: %v", err)
	}

	// Load configs with both paths.
	cfgs, err := config.LoadConfigsWithOptions(config.LoadConfigsOptions{
		PolicyConfigPath: policyPath,
		RegistriesDPath:  registriesDDir,
	})
	if err != nil {
		t.Fatalf("LoadConfigsWithOptions failed: %v", err)
	}
	if cfgs.PolicyConfig == nil {
		t.Fatal("expected PolicyConfig to be loaded, got nil")
	}
	if cfgs.RegistriesDConfig == nil {
		t.Fatal("expected RegistriesDConfig to be loaded, got nil")
	}

	// Create verifier from registries.d config using the full scope.
	// The YAML key is registryHost ("localhost:5000"); fullScope starts with that
	// prefix so longest-prefix matching finds the configured lookaside URL.
	verifier := signature.NewSignedByVerifierFromConfig(cfgs.RegistriesDConfig, fullScope)
	if verifier == nil {
		t.Fatal("NewSignedByVerifierFromConfig returned nil; expected a verifier for the configured scope")
	}

	// Create evaluator from policy config with the verifier.
	evaluator, err := cfgs.PolicyEvaluator(policy.WithSignedByVerifier(verifier))
	if err != nil {
		t.Fatalf("PolicyEvaluator failed: %v", err)
	}
	if evaluator == nil {
		t.Fatal("expected non-nil evaluator")
	}

	// Assert signed image is allowed.
	imageRef := fullScope + "@" + desc.Digest.String()
	allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     fullScope,
		Reference: imageRef,
	})
	if err != nil {
		t.Fatalf("IsImageAllowed (signed) returned error: %v", err)
	}
	if !allowed {
		t.Fatal("expected signed image to be allowed, but it was rejected")
	}

	// Assert image with a different scope (no stored signature) is rejected.
	differentScope := "example.com/nonexistent/repo"
	differentRef := differentScope + "@" + desc.Digest.String()
	allowed, err = evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     differentScope,
		Reference: differentRef,
	})
	if err != nil {
		t.Fatalf("IsImageAllowed (different scope) returned error: %v", err)
	}
	if allowed {
		t.Fatal("expected image with no stored signature (different scope) to be rejected, but it was allowed")
	}
}
