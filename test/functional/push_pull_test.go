//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
)

func TestPushAndPullBlob(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	content, desc := generateContent(t, 256)

	// Push blob.
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Push blob failed: %v", err)
	}

	// Fetch blob by digest.
	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("Fetch blob failed: %v", err)
	}
	defer rc.Close()

	fetched, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Reading fetched blob failed: %v", err)
	}
	if !bytes.Equal(content, fetched) {
		t.Fatalf("Fetched content does not match pushed content")
	}
}

func TestPushAndPullManifest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "v1.0.0"

	// Push a blob layer.
	layerContent, layerDesc := generateContent(t, 128)
	if err := repo.Push(ctx, layerDesc, bytes.NewReader(layerContent)); err != nil {
		t.Fatalf("Push layer failed: %v", err)
	}

	// Pack and push manifest.
	manifestDesc := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{layerDesc})

	// Fetch by tag.
	fetchedDesc, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		t.Fatalf("FetchReference failed: %v", err)
	}
	defer rc.Close()

	if fetchedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch: got %s, want %s", fetchedDesc.Digest, manifestDesc.Digest)
	}

	// Verify content is valid JSON manifest.
	manifestBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Reading manifest failed: %v", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshalling manifest failed: %v", err)
	}
	if len(manifest.Layers) != 1 {
		t.Fatalf("Expected 1 layer, got %d", len(manifest.Layers))
	}
	if manifest.Layers[0].Digest != layerDesc.Digest {
		t.Fatalf("Layer digest mismatch")
	}
}

func TestPushReference(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "push-ref-tag"
	content, desc := generateContent(t, 64)

	// Push the blob first.
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Push blob failed: %v", err)
	}

	// Create and push a manifest with PushReference.
	manifestDesc := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{desc})

	// Resolve the tag.
	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Resolved digest mismatch: got %s, want %s", resolvedDesc.Digest, manifestDesc.Digest)
	}
}

func TestPullByDigest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "digest-test"
	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Fetch by digest string.
	fetchedDesc, rc, err := repo.FetchReference(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatalf("FetchReference by digest failed: %v", err)
	}
	defer rc.Close()

	if fetchedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch")
	}
}

func TestPullByTag(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "tag-test"
	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Fetch by tag.
	fetchedDesc, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		t.Fatalf("FetchReference by tag failed: %v", err)
	}
	defer rc.Close()

	if fetchedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch: got %s, want %s", fetchedDesc.Digest, manifestDesc.Digest)
	}
}

func TestFetchManifestLayers(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	// Push multiple layers.
	var layers []ocispec.Descriptor
	layerContents := make(map[digest.Digest][]byte)
	for i := 0; i < 3; i++ {
		content, desc := generateContent(t, 100+i*50)
		if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
			t.Fatalf("Push layer %d failed: %v", i, err)
		}
		layers = append(layers, desc)
		layerContents[desc.Digest] = content
	}

	tag := "multi-layer"
	packAndPush(t, ctx, repo, tag, layers)

	// Fetch each layer individually and verify.
	for _, layerDesc := range layers {
		rc, err := repo.Fetch(ctx, layerDesc)
		if err != nil {
			t.Fatalf("Fetch layer %s failed: %v", layerDesc.Digest, err)
		}
		fetched, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Reading layer failed: %v", err)
		}
		expected := layerContents[layerDesc.Digest]
		if !bytes.Equal(expected, fetched) {
			t.Fatalf("Layer content mismatch for %s", layerDesc.Digest)
		}
	}
}

func TestPushBytes(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	// Use oras.PushBytes to push content to the memory store first,
	// then use the repo directly.
	store := memory.New()
	content := []byte("hello, oras push bytes")
	desc, err := oras.PushBytes(ctx, store, "text/plain", content)
	if err != nil {
		t.Fatalf("PushBytes to memory failed: %v", err)
	}

	// Push same content to remote.
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Push to remote failed: %v", err)
	}

	// Verify it exists.
	exists, err := repo.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatal("Pushed blob does not exist in remote")
	}
}
