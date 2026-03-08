//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestPushAndListReferrers(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	// Push a base manifest.
	baseTag := "base"
	baseDesc := packAndPush(t, ctx, repo, baseTag, nil)

	// Push a referrer manifest with subject pointing to base.
	artifactType := "application/vnd.test.referrer"
	referrerDesc := pushReferrer(t, ctx, repo, baseDesc, artifactType)

	// List referrers.
	var referrers []ocispec.Descriptor
	if err := repo.Referrers(ctx, baseDesc, "", func(refs []ocispec.Descriptor) error {
		referrers = append(referrers, refs...)
		return nil
	}); err != nil {
		t.Fatalf("Referrers listing failed: %v", err)
	}

	if len(referrers) == 0 {
		t.Fatal("Expected at least one referrer")
	}

	found := false
	for _, ref := range referrers {
		if ref.Digest == referrerDesc.Digest {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Pushed referrer not found in referrers list")
	}
}

func TestReferrersWithArtifactType(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	// Push a base manifest.
	baseTag := "base-filter"
	baseDesc := packAndPush(t, ctx, repo, baseTag, nil)

	// Push two referrers with different artifact types.
	typeA := "application/vnd.test.type-a"
	typeB := "application/vnd.test.type-b"
	pushReferrer(t, ctx, repo, baseDesc, typeA)
	referrerB := pushReferrer(t, ctx, repo, baseDesc, typeB)

	// Filter by artifact type B.
	var referrers []ocispec.Descriptor
	if err := repo.Referrers(ctx, baseDesc, typeB, func(refs []ocispec.Descriptor) error {
		referrers = append(referrers, refs...)
		return nil
	}); err != nil {
		t.Fatalf("Referrers with filter failed: %v", err)
	}

	// Should contain referrer B. Note: server-side filtering may or may not be applied,
	// so we just verify referrer B is present.
	found := false
	for _, ref := range referrers {
		if ref.Digest == referrerB.Digest {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Referrer B not found when filtering by its artifact type")
	}
}

func TestCopyWithReferrers(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	dstRepo := newRepo(t, uniqueRepoName(t))

	// Push a base manifest and a referrer to source.
	baseTag := "base-copy-ref"

	// Use memory store to build the graph, then copy to src.
	store := memory.New()
	baseDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, "application/vnd.test.base", oras.PackManifestOptions{})
	if err != nil {
		t.Fatalf("PackManifest base failed: %v", err)
	}
	if err := store.Tag(ctx, baseDesc, baseTag); err != nil {
		t.Fatalf("Tag base failed: %v", err)
	}

	// Copy base to src remote.
	if _, err := oras.Copy(ctx, store, baseTag, srcRepo, baseTag, oras.DefaultCopyOptions); err != nil {
		t.Fatalf("Copy base to src failed: %v", err)
	}

	// Push a referrer to src remote.
	pushReferrer(t, ctx, srcRepo, baseDesc, "application/vnd.test.referrer-copy")

	// Use ExtendedCopy to copy with referrers from src to dst.
	copiedDesc, err := oras.ExtendedCopy(ctx, srcRepo, baseTag, dstRepo, baseTag, oras.DefaultExtendedCopyOptions)
	if err != nil {
		t.Fatalf("ExtendedCopy failed: %v", err)
	}
	if copiedDesc.Digest != baseDesc.Digest {
		t.Fatalf("ExtendedCopy digest mismatch")
	}

	// Verify referrers exist in dst.
	var referrers []ocispec.Descriptor
	if err := dstRepo.Referrers(ctx, baseDesc, "", func(refs []ocispec.Descriptor) error {
		referrers = append(referrers, refs...)
		return nil
	}); err != nil {
		t.Fatalf("Referrers in dst failed: %v", err)
	}

	if len(referrers) == 0 {
		t.Fatal("Expected referrers to be copied to destination")
	}
}

// pushReferrer creates and pushes a manifest that references the given subject.
func pushReferrer(t *testing.T, ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor, artifactType string) ocispec.Descriptor {
	t.Helper()

	// Create an empty config.
	configBytes := ocispec.DescriptorEmptyJSON.Data
	configDesc := ocispec.DescriptorEmptyJSON

	// Push config (ignore already-exists errors).
	if err := repo.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil {
		t.Logf("Push config (may already exist): %v", err)
	}

	// Create empty layer.
	layerDesc := ocispec.DescriptorEmptyJSON

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{layerDesc},
		Subject:      &subject,
		ArtifactType: artifactType,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal referrer manifest failed: %v", err)
	}

	manifestDesc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		Digest:       digest.FromBytes(manifestBytes),
		Size:         int64(len(manifestBytes)),
		ArtifactType: artifactType,
	}

	// Push manifest by digest (no tag).
	if err := repo.Push(ctx, manifestDesc, io.Reader(bytes.NewReader(manifestBytes))); err != nil {
		t.Fatalf("Push referrer manifest failed: %v", err)
	}

	return manifestDesc
}
