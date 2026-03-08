//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"os"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/content/oci"
)

func TestCopyBetweenRepositories(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	dstRepo := newRepo(t, uniqueRepoName(t))

	tag := "copy-test"

	// Push content to source.
	layerContent, layerDesc := generateContent(t, 128)
	if err := srcRepo.Push(ctx, layerDesc, bytes.NewReader(layerContent)); err != nil {
		t.Fatalf("Push layer to src failed: %v", err)
	}
	manifestDesc := packAndPush(t, ctx, srcRepo, tag, []ocispec.Descriptor{layerDesc})

	// Copy from src to dst.
	copiedDesc, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
	}
	if copiedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Copied digest mismatch: got %s, want %s", copiedDesc.Digest, manifestDesc.Digest)
	}

	// Verify content exists in dst.
	resolvedDesc, err := dstRepo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve in dst failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Resolved digest mismatch in dst")
	}
}

func TestCopyGraphMemoryToRemote(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	dstRepo := newRepo(t, uniqueRepoName(t))

	tag := "mem-to-remote"

	// Pack a manifest in memory store.
	desc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, "application/vnd.test.artifact", oras.PackManifestOptions{})
	if err != nil {
		t.Fatalf("PackManifest failed: %v", err)
	}
	if err := store.Tag(ctx, desc, tag); err != nil {
		t.Fatalf("Tag in memory store failed: %v", err)
	}

	// Copy from memory to remote.
	copiedDesc, err := oras.Copy(ctx, store, tag, dstRepo, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy memory to remote failed: %v", err)
	}
	if copiedDesc.Digest != desc.Digest {
		t.Fatalf("Digest mismatch after copy")
	}

	// Verify in remote.
	resolvedDesc, err := dstRepo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve in remote failed: %v", err)
	}
	if resolvedDesc.Digest != desc.Digest {
		t.Fatalf("Resolved digest mismatch")
	}
}

func TestCopyFromRemoteToOCILayout(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))

	tag := "to-oci-layout"
	manifestDesc := packAndPush(t, ctx, srcRepo, tag, nil)

	// Create a temporary OCI layout store.
	tmpDir, err := os.MkdirTemp("", "oras-functional-oci-*")
	if err != nil {
		t.Fatalf("Creating temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ociStore, err := oci.New(tmpDir)
	if err != nil {
		t.Fatalf("Creating OCI store failed: %v", err)
	}

	// Copy from remote to OCI layout.
	copiedDesc, err := oras.Copy(ctx, srcRepo, tag, ociStore, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy to OCI layout failed: %v", err)
	}
	if copiedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch")
	}

	// Verify in OCI layout.
	resolvedDesc, err := ociStore.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve in OCI layout failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Resolved digest mismatch in OCI layout")
	}
}

func TestCopyFromOCILayoutToRemote(t *testing.T) {
	ctx := context.Background()
	dstRepo := newRepo(t, uniqueRepoName(t))

	tag := "from-oci-layout"

	// Create a temporary OCI layout store with content.
	tmpDir, err := os.MkdirTemp("", "oras-functional-oci-src-*")
	if err != nil {
		t.Fatalf("Creating temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ociStore, err := oci.New(tmpDir)
	if err != nil {
		t.Fatalf("Creating OCI store failed: %v", err)
	}

	desc, err := oras.PackManifest(ctx, ociStore, oras.PackManifestVersion1_1, "application/vnd.test.artifact", oras.PackManifestOptions{})
	if err != nil {
		t.Fatalf("PackManifest failed: %v", err)
	}
	if err := ociStore.Tag(ctx, desc, tag); err != nil {
		t.Fatalf("Tag in OCI store failed: %v", err)
	}

	// Copy from OCI layout to remote.
	copiedDesc, err := oras.Copy(ctx, ociStore, tag, dstRepo, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy from OCI layout to remote failed: %v", err)
	}
	if copiedDesc.Digest != desc.Digest {
		t.Fatalf("Digest mismatch")
	}

	// Verify in remote.
	resolvedDesc, err := dstRepo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve in remote failed: %v", err)
	}
	if resolvedDesc.Digest != desc.Digest {
		t.Fatalf("Resolved digest mismatch")
	}
}

func TestCopySkipsExistingContent(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	dstRepo := newRepo(t, uniqueRepoName(t))

	tag := "skip-existing"
	manifestDesc := packAndPush(t, ctx, srcRepo, tag, nil)

	// First copy: srcRepo → dstRepo.
	_, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("First copy failed: %v", err)
	}

	// Second pass using CopyGraph directly (not oras.Copy) so that the
	// ReferencePusher optimisation does not suppress OnCopySkipped for the root.
	// All content already exists in dstRepo, so every node should be skipped.
	var skippedCount atomic.Int32
	opts := oras.CopyGraphOptions{
		OnCopySkipped: func(_ context.Context, _ ocispec.Descriptor) error {
			skippedCount.Add(1)
			return nil
		},
	}

	if err := oras.CopyGraph(ctx, srcRepo, dstRepo, manifestDesc, opts); err != nil {
		t.Fatalf("Second CopyGraph failed: %v", err)
	}

	// At least the root manifest should have been skipped.
	if skippedCount.Load() == 0 {
		t.Fatal("Expected OnCopySkipped to be called at least once")
	}
}

func TestCopyWithPrePostHooks(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	dstRepo := newRepo(t, uniqueRepoName(t))

	tag := "hooks-test"
	packAndPush(t, ctx, srcRepo, tag, nil)

	var preCopyCount, postCopyCount atomic.Int32

	opts := oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			PreCopy: func(_ context.Context, _ ocispec.Descriptor) error {
				preCopyCount.Add(1)
				return nil
			},
			PostCopy: func(_ context.Context, _ ocispec.Descriptor) error {
				postCopyCount.Add(1)
				return nil
			},
		},
	}

	_, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, opts)
	if err != nil {
		t.Fatalf("Copy with hooks failed: %v", err)
	}

	if preCopyCount.Load() == 0 {
		t.Fatal("PreCopy was never called")
	}
	if postCopyCount.Load() == 0 {
		t.Fatal("PostCopy was never called")
	}
}
