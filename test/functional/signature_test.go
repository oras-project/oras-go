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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// writeGPGKeyFile serializes the public key of the given OpenPGP entity to a
// temporary file and returns the file path.
func writeGPGKeyFile(t *testing.T, entity *openpgp.Entity) string {
	t.Helper()
	var buf bytes.Buffer
	if err := entity.Serialize(&buf); err != nil {
		t.Fatalf("failed to serialize GPG public key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "key.gpg")
	if err := os.WriteFile(keyPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write GPG key file: %v", err)
	}
	return keyPath
}

// signImage creates a simple signing payload for the given descriptor and tag,
// signs it with the provided OpenPGP entity, and stores the signature in the
// lookaside store. scope must be the full registry+repository path
// (e.g. "localhost:5000/repo/name") — it is used directly as both the
// docker-reference base and the lookaside storage namespace.
func signImage(t *testing.T, ctx context.Context, store *signature.LookasideStore, scope string, desc ocispec.Descriptor, tag string, entity *openpgp.Entity) {
	t.Helper()
	dockerRef := scope + ":" + tag
	payload := signature.NewSimpleSigningPayload(desc.Digest, dockerRef)
	payloadBytes, err := payload.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal signing payload: %v", err)
	}
	sigData, err := signature.CreateOpenPGPSignature(payloadBytes, entity)
	if err != nil {
		t.Fatalf("failed to create OpenPGP signature: %v", err)
	}
	if err := store.PutSignature(ctx, scope, desc.Digest, sigData); err != nil {
		t.Fatalf("failed to store signature: %v", err)
	}
}

// TestSignature_GPG_SignedImageAllowed pushes an image, generates a GPG key,
// signs the image, stores the signature in a file-based lookaside store, and
// asserts that the policy evaluator allows the image.
func TestSignature_GPG_SignedImageAllowed(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)
	tag := "v1"
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)

	desc, _ := pushManifest(t, ctx, repo, tag, nil)

	// Generate a GPG key pair.
	entity, err := openpgp.NewEntity("Test User", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity: %v", err)
	}
	keyPath := writeGPGKeyFile(t, entity)

	// Set up file-based lookaside store.
	sigDir := t.TempDir()
	lookasideURL := "file://" + sigDir
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)

	// Sign the image (scope is the full host/repo path).
	signImage(t, ctx, store, scope, desc, tag, entity)

	// Build policy with PRSignedBy default requirement.
	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyPath,
	})

	// Create verifier and evaluator.
	verifier := signature.NewSignedByVerifier(store)
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(verifier))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}

	imageRef := scope + "@" + desc.Digest.String()
	allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     scope,
		Reference: imageRef,
	})
	if err != nil {
		t.Fatalf("IsImageAllowed returned error: %v", err)
	}
	if !allowed {
		t.Fatal("expected signed image to be allowed, but it was rejected")
	}
}

// TestSignature_GPG_UnsignedImageRejected pushes an image without any
// signature and asserts that the policy evaluator rejects it.
func TestSignature_GPG_UnsignedImageRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)
	tag := "v1"
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)

	desc, _ := pushManifest(t, ctx, repo, tag, nil)

	// Generate a GPG key pair (for the policy, but no signature is stored).
	entity, err := openpgp.NewEntity("Test User", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity: %v", err)
	}
	keyPath := writeGPGKeyFile(t, entity)

	// Empty lookaside store with no signatures.
	sigDir := t.TempDir()
	lookasideURL := "file://" + sigDir
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)

	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyPath,
	})

	verifier := signature.NewSignedByVerifier(store)
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(verifier))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}

	imageRef := scope + "@" + desc.Digest.String()
	allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     scope,
		Reference: imageRef,
	})
	if err != nil {
		t.Fatalf("IsImageAllowed returned error: %v", err)
	}
	if allowed {
		t.Fatal("expected unsigned image to be rejected, but it was allowed")
	}
}

// TestSignature_GPG_WrongKeyRejected signs an image with key A but configures
// the policy to expect key B, and asserts that the evaluator rejects the image.
func TestSignature_GPG_WrongKeyRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)
	tag := "v1"
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)

	desc, _ := pushManifest(t, ctx, repo, tag, nil)

	// Generate signing key (key A).
	entityA, err := openpgp.NewEntity("Signer A", "", "a@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity A: %v", err)
	}

	// Generate policy key (key B) -- different from the signing key.
	entityB, err := openpgp.NewEntity("Signer B", "", "b@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity B: %v", err)
	}
	keyPathB := writeGPGKeyFile(t, entityB)

	// Sign with key A.
	sigDir := t.TempDir()
	lookasideURL := "file://" + sigDir
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, desc, tag, entityA)

	// Policy expects key B.
	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyPathB,
	})

	verifier := signature.NewSignedByVerifier(store)
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(verifier))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}

	imageRef := scope + "@" + desc.Digest.String()
	allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     scope,
		Reference: imageRef,
	})
	if err != nil {
		t.Fatalf("IsImageAllowed returned error: %v", err)
	}
	if allowed {
		t.Fatal("expected image signed with wrong key to be rejected, but it was allowed")
	}
}
