//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestBasicAuthPushPull(t *testing.T) {
	ctx := context.Background()
	repo := newAuthRepo(t, uniqueRepoName(t), "testuser", "testpass")

	// Push a blob.
	content, desc := generateContent(t, 64)
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Authenticated push failed: %v", err)
	}

	// Push a manifest.
	tag := "auth-test"
	manifestDesc := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{desc})

	// Pull the manifest.
	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Authenticated resolve failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch")
	}
}

func TestUnauthenticatedPushFails(t *testing.T) {
	ctx := context.Background()

	// Create a repo without credentials pointing to the auth endpoint.
	repo := newAuthRepo(t, uniqueRepoName(t), "", "")

	content, desc := generateContent(t, 64)
	err := repo.Push(ctx, desc, bytes.NewReader(content))
	if err == nil {
		t.Fatal("Expected unauthenticated push to fail, but it succeeded")
	}
}

func TestWrongCredentialsFail(t *testing.T) {
	ctx := context.Background()

	// Create a repo with wrong credentials.
	repo := newAuthRepo(t, uniqueRepoName(t), "testuser", "wrongpassword")

	content, desc := generateContent(t, 64)
	err := repo.Push(ctx, desc, bytes.NewReader(content))
	if err == nil {
		t.Fatal("Expected push with wrong credentials to fail, but it succeeded")
	}
}
