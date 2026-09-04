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
	"encoding/json"
	"errors"
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
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
	configData, err := json.Marshal(ocispec.Image{Platform: p})
	if err != nil {
		t.Fatalf("failed to marshal config for %s/%s: %v", p.OS, p.Architecture, err)
	}
	configDesc := pushBlob(t, ctx, repo, ocispec.MediaTypeImageConfig, configData)
	layerDesc := pushBlob(t, ctx, repo, ocispec.MediaTypeImageLayer, []byte("layer for "+p.OS+"/"+p.Architecture))

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest for %s/%s: %v", p.OS, p.Architecture, err)
	}
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestData)
	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestData), manifestDesc.Digest.String()); err != nil {
		t.Fatalf("failed to push manifest for %s/%s: %v", p.OS, p.Architecture, err)
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
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}
	indexDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, indexData)
	if err := repo.PushReference(ctx, indexDesc, bytes.NewReader(indexData), tag); err != nil {
		t.Fatalf("failed to push index with tag %s: %v", tag, err)
	}
	return indexDesc, indexData
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

// TestIndexPushPullRoundTrip pushes a two-platform index and reads it back,
// checking the registry preserved the entries and their platforms.
func TestIndexPushPullRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t, newRepoName(t))
	tag := "multi-arch"

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, repo, tag)

	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve index failed: %v", err)
	}
	if resolvedDesc.Digest != indexDesc.Digest {
		t.Fatalf("resolved digest = %s, want %s", resolvedDesc.Digest, indexDesc.Digest)
	}
	if resolvedDesc.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("resolved media type = %s, want %s", resolvedDesc.MediaType, ocispec.MediaTypeImageIndex)
	}

	_, indexData, err := oras.FetchBytes(ctx, repo, tag, oras.DefaultFetchBytesOptions)
	if err != nil {
		t.Fatalf("FetchBytes index failed: %v", err)
	}

	var index ocispec.Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("unmarshal index failed: %v", err)
	}
	if len(index.Manifests) != 2 {
		t.Fatalf("index has %d manifests, want 2", len(index.Manifests))
	}
	for i, want := range []ocispec.Descriptor{amd64Desc, arm64Desc} {
		got := index.Manifests[i]
		if got.Digest != want.Digest {
			t.Errorf("manifest %d digest = %s, want %s", i, got.Digest, want.Digest)
		}
		if got.Platform == nil {
			t.Errorf("manifest %d has no platform, want %v", i, *want.Platform)
			continue
		}
		if got.Platform.OS != want.Platform.OS || got.Platform.Architecture != want.Platform.Architecture {
			t.Errorf("manifest %d platform = %s/%s, want %s/%s", i,
				got.Platform.OS, got.Platform.Architecture,
				want.Platform.OS, want.Platform.Architecture)
		}
	}
}

// TestCopyIndexToMemory copies a whole manifest list into a memory store and
// checks the full graph -- index, both children, and their blobs -- arrived.
func TestCopyIndexToMemory(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t, newRepoName(t))
	tag := "multi-arch"

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, repo, tag)

	dst := memory.New()
	copiedDesc, err := oras.Copy(ctx, repo, tag, dst, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy index to memory failed: %v", err)
	}
	if copiedDesc.Digest != indexDesc.Digest {
		t.Fatalf("copied digest = %s, want %s", copiedDesc.Digest, indexDesc.Digest)
	}

	for _, child := range []ocispec.Descriptor{amd64Desc, arm64Desc} {
		exists, err := dst.Exists(ctx, child)
		if err != nil {
			t.Fatalf("Exists check for child %s failed: %v", child.Digest, err)
		}
		if !exists {
			t.Errorf("child manifest %s (%s/%s) missing from destination",
				child.Digest, child.Platform.OS, child.Platform.Architecture)
			continue
		}

		blobs, err := content.Successors(ctx, dst, child)
		if err != nil {
			t.Fatalf("Successors of child %s failed: %v", child.Digest, err)
		}
		if len(blobs) == 0 {
			t.Errorf("child manifest %s has no successors in destination", child.Digest)
		}
		for _, blob := range blobs {
			exists, err := dst.Exists(ctx, blob)
			if err != nil {
				t.Fatalf("Exists check for blob %s failed: %v", blob.Digest, err)
			}
			if !exists {
				t.Errorf("blob %s of child %s missing from destination", blob.Digest, child.Digest)
			}
		}
	}
}

// TestCopyIndexWithTargetPlatform pins the platform-selection contract: the
// copy root becomes the matching child manifest, the sibling is left behind,
// and a platform absent from the index is a not-found error.
func TestCopyIndexWithTargetPlatform(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t, newRepoName(t))
	tag := "multi-arch"

	_, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, repo, tag)

	t.Run("SelectsMatchingManifest", func(t *testing.T) {
		dst := memory.New()
		opts := oras.CopyOptions{}
		opts.WithTargetPlatform(&platformLinuxARM64)

		copiedDesc, err := oras.Copy(ctx, repo, tag, dst, tag, opts)
		if err != nil {
			t.Fatalf("Copy with target platform failed: %v", err)
		}
		if copiedDesc.Digest != arm64Desc.Digest {
			t.Fatalf("copied digest = %s, want the arm64 manifest %s", copiedDesc.Digest, arm64Desc.Digest)
		}

		exists, err := dst.Exists(ctx, amd64Desc)
		if err != nil {
			t.Fatalf("Exists check for the unselected manifest failed: %v", err)
		}
		if exists {
			t.Errorf("amd64 manifest %s was copied despite an arm64 target platform", amd64Desc.Digest)
		}
	})

	t.Run("NoMatchingManifest", func(t *testing.T) {
		dst := memory.New()
		opts := oras.CopyOptions{}
		opts.WithTargetPlatform(&platformWindows)

		_, err := oras.Copy(ctx, repo, tag, dst, tag, opts)
		if err == nil {
			t.Fatal("Copy should fail when no manifest matches the target platform")
		}
		if !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Copy error = %v, want %v", err, errdef.ErrNotFound)
		}
	})
}

// TestResolveIndexWithTargetPlatform checks that resolving a manifest list with
// a target platform yields the matching child rather than the list itself.
func TestResolveIndexWithTargetPlatform(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t, newRepoName(t))
	tag := "multi-arch"

	indexDesc, amd64Desc, _ := pushMultiPlatformIndex(t, ctx, repo, tag)

	opts := oras.DefaultResolveOptions
	opts.TargetPlatform = &platformLinuxAMD64

	desc, err := oras.Resolve(ctx, repo, tag, opts)
	if err != nil {
		t.Fatalf("Resolve with target platform failed: %v", err)
	}
	if desc.Digest == indexDesc.Digest {
		t.Fatal("Resolve returned the index itself, want the matching child manifest")
	}
	if desc.Digest != amd64Desc.Digest {
		t.Fatalf("resolved digest = %s, want the amd64 manifest %s", desc.Digest, amd64Desc.Digest)
	}

	opts.TargetPlatform = &platformWindows
	if _, err := oras.Resolve(ctx, repo, tag, opts); !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Resolve for an absent platform error = %v, want %v", err, errdef.ErrNotFound)
	}
}
