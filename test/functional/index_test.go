//go:build k8sfunctional

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

package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

// Platforms used to build the test manifest lists.
var (
	platformLinuxAMD64 = ocispec.Platform{OS: "linux", Architecture: "amd64"}
	platformLinuxARM64 = ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	platformWindows    = ocispec.Platform{OS: "windows", Architecture: "amd64"}
)

// pushPlatformManifest pushes a config, a layer and an image manifest for the
// given platform, addressed by digest rather than by tag. The returned
// descriptor carries the platform, as an index entry must: platform selection
// reads it from the index, not from the child manifest's config.
func pushPlatformManifest(t *testing.T, ctx context.Context, repo *remote.Repository, p ocispec.Platform) ocispec.Descriptor {
	t.Helper()

	// The config embeds the platform, which also makes each child manifest's
	// digest distinct from its siblings'.
	configBytes, err := json.Marshal(ocispec.Image{Platform: p})
	if err != nil {
		t.Fatalf("Failed to marshal config for %s/%s: %v", p.OS, p.Architecture, err)
	}
	configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, configBytes)
	if err := repo.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil {
		t.Fatalf("Failed to push config for %s/%s: %v", p.OS, p.Architecture, err)
	}

	layerBytes := []byte("layer for " + p.OS + "/" + p.Architecture)
	layerDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, layerBytes)
	if err := repo.Push(ctx, layerDesc, bytes.NewReader(layerBytes)); err != nil {
		t.Fatalf("Failed to push layer for %s/%s: %v", p.OS, p.Architecture, err)
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Failed to marshal manifest for %s/%s: %v", p.OS, p.Architecture, err)
	}
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBytes)
	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), manifestDesc.Digest.String()); err != nil {
		t.Fatalf("Failed to push manifest for %s/%s: %v", p.OS, p.Architecture, err)
	}

	manifestDesc.Platform = &p
	return manifestDesc
}

// pushIndex builds an image index over the given manifests and pushes it under
// tag, returning its descriptor and raw bytes.
func pushIndex(t *testing.T, ctx context.Context, repo *remote.Repository, tag string, manifests []ocispec.Descriptor) (ocispec.Descriptor, []byte) {
	t.Helper()

	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("Failed to marshal index: %v", err)
	}
	indexDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, indexBytes)
	if err := repo.PushReference(ctx, indexDesc, bytes.NewReader(indexBytes), tag); err != nil {
		t.Fatalf("Failed to push index with tag %s: %v", tag, err)
	}
	return indexDesc, indexBytes
}

// pushMultiPlatformIndex pushes a two-platform index (linux/amd64 and
// linux/arm64) under tag and returns the index descriptor along with the two
// child manifest descriptors.
func pushMultiPlatformIndex(t *testing.T, ctx context.Context, repo *remote.Repository, tag string) (index, amd64, arm64 ocispec.Descriptor) {
	t.Helper()
	amd64 = pushPlatformManifest(t, ctx, repo, platformLinuxAMD64)
	arm64 = pushPlatformManifest(t, ctx, repo, platformLinuxARM64)
	index, _ = pushIndex(t, ctx, repo, tag, []ocispec.Descriptor{amd64, arm64})
	return index, amd64, arm64
}

func TestIndexPushAndResolve(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))
	tag := "multi-arch"

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, repo, tag)

	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve index failed: %v", err)
	}
	if resolvedDesc.Digest != indexDesc.Digest {
		t.Fatalf("Resolved digest = %s, want %s", resolvedDesc.Digest, indexDesc.Digest)
	}
	if resolvedDesc.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("Resolved media type = %s, want %s", resolvedDesc.MediaType, ocispec.MediaTypeImageIndex)
	}

	// Fetch the index back and check the registry preserved the entries and
	// their platforms verbatim.
	rc, err := repo.Fetch(ctx, resolvedDesc)
	if err != nil {
		t.Fatalf("Fetch index failed: %v", err)
	}
	defer rc.Close()
	fetched, err := content.ReadAll(rc, resolvedDesc)
	if err != nil {
		t.Fatalf("Read index failed: %v", err)
	}

	var index ocispec.Index
	if err := json.Unmarshal(fetched, &index); err != nil {
		t.Fatalf("Unmarshal index failed: %v", err)
	}
	if len(index.Manifests) != 2 {
		t.Fatalf("Index has %d manifests, want 2", len(index.Manifests))
	}
	for i, want := range []ocispec.Descriptor{amd64Desc, arm64Desc} {
		got := index.Manifests[i]
		if got.Digest != want.Digest {
			t.Errorf("Manifest %d digest = %s, want %s", i, got.Digest, want.Digest)
		}
		if got.Platform == nil {
			t.Errorf("Manifest %d has no platform, want %v", i, *want.Platform)
			continue
		}
		if got.Platform.OS != want.Platform.OS || got.Platform.Architecture != want.Platform.Architecture {
			t.Errorf("Manifest %d platform = %s/%s, want %s/%s", i,
				got.Platform.OS, got.Platform.Architecture,
				want.Platform.OS, want.Platform.Architecture)
		}
	}
}

func TestIndexSuccessors(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, repo, "successors")

	successors, err := content.Successors(ctx, repo, indexDesc)
	if err != nil {
		t.Fatalf("Successors of index failed: %v", err)
	}
	if len(successors) != 2 {
		t.Fatalf("Index has %d successors, want 2", len(successors))
	}

	got := map[string]bool{}
	for _, s := range successors {
		got[s.Digest.String()] = true
	}
	for _, want := range []ocispec.Descriptor{amd64Desc, arm64Desc} {
		if !got[want.Digest.String()] {
			t.Errorf("Successors missing child manifest %s", want.Digest)
		}
	}
}

func TestCopyIndexBetweenRepositories(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	dstRepo := newRepo(t, uniqueRepoName(t))
	tag := "multi-arch"

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, srcRepo, tag)

	copiedDesc, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy index failed: %v", err)
	}
	if copiedDesc.Digest != indexDesc.Digest {
		t.Fatalf("Copied digest = %s, want %s", copiedDesc.Digest, indexDesc.Digest)
	}

	resolvedDesc, err := dstRepo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve index in dst failed: %v", err)
	}
	if resolvedDesc.Digest != indexDesc.Digest {
		t.Fatalf("Resolved digest in dst = %s, want %s", resolvedDesc.Digest, indexDesc.Digest)
	}

	// The whole graph must have come across, not just the index: both child
	// manifests and every blob they reference.
	for _, child := range []ocispec.Descriptor{amd64Desc, arm64Desc} {
		exists, err := dstRepo.Exists(ctx, child)
		if err != nil {
			t.Fatalf("Exists check for child %s failed: %v", child.Digest, err)
		}
		if !exists {
			t.Errorf("Child manifest %s (%s/%s) missing from dst", child.Digest, child.Platform.OS, child.Platform.Architecture)
			continue
		}

		blobs, err := content.Successors(ctx, dstRepo, child)
		if err != nil {
			t.Fatalf("Successors of child %s in dst failed: %v", child.Digest, err)
		}
		if len(blobs) == 0 {
			t.Errorf("Child manifest %s has no successors in dst", child.Digest)
		}
		for _, blob := range blobs {
			exists, err := dstRepo.Exists(ctx, blob)
			if err != nil {
				t.Fatalf("Exists check for blob %s failed: %v", blob.Digest, err)
			}
			if !exists {
				t.Errorf("Blob %s of child %s missing from dst", blob.Digest, child.Digest)
			}
		}
	}
}

func TestCopyIndexWithTargetPlatform(t *testing.T) {
	ctx := context.Background()
	srcRepo := newRepo(t, uniqueRepoName(t))
	tag := "multi-arch"

	_, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, srcRepo, tag)

	t.Run("SelectsMatchingManifest", func(t *testing.T) {
		dstRepo := newRepo(t, uniqueRepoName(t))

		opts := oras.CopyOptions{}
		opts.WithTargetPlatform(&platformLinuxARM64)

		copiedDesc, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, opts)
		if err != nil {
			t.Fatalf("Copy with target platform failed: %v", err)
		}
		// The copy root must be the arm64 child, not the index.
		if copiedDesc.Digest != arm64Desc.Digest {
			t.Fatalf("Copied digest = %s, want the arm64 manifest %s", copiedDesc.Digest, arm64Desc.Digest)
		}

		resolvedDesc, err := dstRepo.Resolve(ctx, tag)
		if err != nil {
			t.Fatalf("Resolve in dst failed: %v", err)
		}
		if resolvedDesc.Digest != arm64Desc.Digest {
			t.Fatalf("Resolved digest in dst = %s, want %s", resolvedDesc.Digest, arm64Desc.Digest)
		}
		if resolvedDesc.MediaType != ocispec.MediaTypeImageManifest {
			t.Errorf("Resolved media type = %s, want %s", resolvedDesc.MediaType, ocispec.MediaTypeImageManifest)
		}

		// The unselected platform must not have been dragged along.
		exists, err := dstRepo.Exists(ctx, amd64Desc)
		if err != nil {
			t.Fatalf("Exists check for the unselected manifest failed: %v", err)
		}
		if exists {
			t.Errorf("amd64 manifest %s was copied despite an arm64 target platform", amd64Desc.Digest)
		}
	})

	t.Run("NoMatchingManifest", func(t *testing.T) {
		dstRepo := newRepo(t, uniqueRepoName(t))

		opts := oras.CopyOptions{}
		opts.WithTargetPlatform(&platformWindows)

		_, err := oras.Copy(ctx, srcRepo, tag, dstRepo, tag, opts)
		if err == nil {
			t.Fatal("Copy should fail when no manifest matches the target platform")
		}
		if !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Copy error = %v, want %v", err, errdef.ErrNotFound)
		}
	})
}

func TestResolveIndexWithTargetPlatform(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))
	tag := "multi-arch"

	_, amd64Desc, _ := pushMultiPlatformIndex(t, ctx, repo, tag)

	opts := oras.DefaultResolveOptions
	opts.TargetPlatform = &platformLinuxAMD64

	desc, err := oras.Resolve(ctx, repo, tag, opts)
	if err != nil {
		t.Fatalf("Resolve with target platform failed: %v", err)
	}
	if desc.Digest != amd64Desc.Digest {
		t.Fatalf("Resolved digest = %s, want the amd64 manifest %s", desc.Digest, amd64Desc.Digest)
	}

	// A platform absent from the index must resolve to nothing, not to the
	// index itself or to an arbitrary child.
	opts.TargetPlatform = &platformWindows
	if _, err := oras.Resolve(ctx, repo, tag, opts); !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Resolve for an absent platform error = %v, want %v", err, errdef.ErrNotFound)
	}
}
