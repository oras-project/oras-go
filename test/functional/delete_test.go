//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"testing"
)

func TestDeleteBlob(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	content, desc := generateContent(t, 128)

	// Push blob.
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Push blob failed: %v", err)
	}

	// Verify it exists.
	exists, err := repo.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatal("Blob should exist after push")
	}

	// Delete blob.
	if err := repo.Delete(ctx, desc); err != nil {
		t.Fatalf("Delete blob failed: %v", err)
	}

	// Verify it no longer exists.
	exists, err = repo.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists check after delete failed: %v", err)
	}
	if exists {
		t.Fatal("Blob should not exist after delete")
	}
}

func TestDeleteManifest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "delete-me"
	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Verify it resolves.
	_, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve before delete failed: %v", err)
	}

	// Delete manifest.
	if err := repo.Delete(ctx, manifestDesc); err != nil {
		t.Fatalf("Delete manifest failed: %v", err)
	}

	// Verify resolve fails.
	_, err = repo.Resolve(ctx, tag)
	if err == nil {
		t.Fatal("Resolve should fail after manifest delete")
	}
}
