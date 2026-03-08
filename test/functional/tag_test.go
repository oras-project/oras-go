//go:build k8sfunctional

package functional

import (
	"context"
	"sort"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
)

func TestTagManifest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "original"
	newTag := "alias"

	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Tag with a new name.
	if err := repo.Tag(ctx, manifestDesc, newTag); err != nil {
		t.Fatalf("Tag failed: %v", err)
	}

	// Resolve the new tag.
	resolvedDesc, err := repo.Resolve(ctx, newTag)
	if err != nil {
		t.Fatalf("Resolve new tag failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch: got %s, want %s", resolvedDesc.Digest, manifestDesc.Digest)
	}
}

func TestTagOverwrite(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "latest"

	// Push two blobs with different content so the manifests have distinct digests.
	contentA, layerA := generateContent(t, 32)
	pushBlob(t, ctx, repo, contentA)

	contentB, layerB := generateContent(t, 64)
	pushBlob(t, ctx, repo, contentB)

	// Push first manifest containing layerA.
	descA := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{layerA})

	// Push second manifest containing layerB — overwrites the tag.
	descB := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{layerB})

	if descA.Digest == descB.Digest {
		t.Fatalf("Test setup error: both manifests have the same digest")
	}

	// Resolve the tag -- should be the second manifest.
	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolvedDesc.Digest != descB.Digest {
		t.Fatalf("Tag was not overwritten: got %s, want %s", resolvedDesc.Digest, descB.Digest)
	}
}

func TestListTags(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tags := []string{"v1", "v2", "v3"}
	for _, tag := range tags {
		packAndPush(t, ctx, repo, tag, nil)
	}

	// List tags.
	var listedTags []string
	if err := repo.Tags(ctx, "", func(fetchedTags []string) error {
		listedTags = append(listedTags, fetchedTags...)
		return nil
	}); err != nil {
		t.Fatalf("Tags listing failed: %v", err)
	}

	sort.Strings(listedTags)
	sort.Strings(tags)

	if len(listedTags) < len(tags) {
		t.Fatalf("Expected at least %d tags, got %d", len(tags), len(listedTags))
	}

	tagSet := make(map[string]bool)
	for _, tag := range listedTags {
		tagSet[tag] = true
	}
	for _, expected := range tags {
		if !tagSet[expected] {
			t.Fatalf("Expected tag %q not found in listed tags", expected)
		}
	}
}

func TestResolveByDigest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "digest-resolve"
	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Resolve by digest.
	resolvedDesc, err := repo.Resolve(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatalf("Resolve by digest failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch")
	}
}

func TestOrasTagHelper(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	srcTag := "src-tag"
	dstTag := "dst-tag"

	packAndPush(t, ctx, repo, srcTag, nil)

	// Use oras.Tag helper.
	desc, err := oras.Tag(ctx, repo, srcTag, dstTag)
	if err != nil {
		t.Fatalf("oras.Tag failed: %v", err)
	}

	// Verify.
	resolvedDesc, err := repo.Resolve(ctx, dstTag)
	if err != nil {
		t.Fatalf("Resolve dst tag failed: %v", err)
	}
	if resolvedDesc.Digest != desc.Digest {
		t.Fatalf("Digest mismatch")
	}
}
