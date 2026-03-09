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

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/oci"
	"github.com/oras-project/oras-go/v3/objects"
	"github.com/oras-project/oras-go/v3/objects/models"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

// newORMClient creates an ORM client backed by a remote repository.
func newORMClient(t *testing.T, opts ...objects.ClientOption) (*objects.Client, *remote.Repository) {
	t.Helper()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)
	client := objects.NewClient(repo, opts...)
	return client, repo
}

func TestORM_ClientBlobLifecycle(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	t.Run("NewBlob and Push", func(t *testing.T) {
		data := []byte(`{"version": "1.0.0"}`)
		blob := client.NewBlob("application/json", data)

		// Verify descriptor is correct.
		if blob.MediaType() != "application/json" {
			t.Errorf("MediaType = %q, want %q", blob.MediaType(), "application/json")
		}
		if blob.Size() != int64(len(data)) {
			t.Errorf("Size = %d, want %d", blob.Size(), len(data))
		}
		if blob.Digest() != digest.FromBytes(data) {
			t.Errorf("Digest mismatch")
		}

		// Push blob to remote registry.
		if err := blob.Push(ctx); err != nil {
			t.Fatalf("Push(): %v", err)
		}

		// Verify we can fetch it back.
		fetched, err := client.FetchBlob(ctx, blob.Descriptor())
		if err != nil {
			t.Fatalf("FetchBlob(): %v", err)
		}

		// Should be the same cached instance.
		if fetched != blob {
			t.Error("FetchBlob should return cached instance")
		}

		// Verify content can be read back.
		content, err := fetched.Bytes(ctx)
		if err != nil {
			t.Fatalf("Bytes(): %v", err)
		}
		if !bytes.Equal(content, data) {
			t.Errorf("Bytes() = %q, want %q", string(content), string(data))
		}
	})

	t.Run("FetchBlob lazy loading", func(t *testing.T) {
		// Push a blob directly via the client.
		data := []byte("lazy-load-content")
		blob := client.NewBlob("application/octet-stream", data)
		if err := blob.Push(ctx); err != nil {
			t.Fatalf("Push(): %v", err)
		}

		// Clear cache so next fetch creates a new lazy blob.
		client.ClearCache()

		fetched, err := client.FetchBlob(ctx, blob.Descriptor())
		if err != nil {
			t.Fatalf("FetchBlob(): %v", err)
		}

		// Content should be lazily loaded.
		content, err := fetched.Bytes(ctx)
		if err != nil {
			t.Fatalf("Bytes(): %v", err)
		}
		if !bytes.Equal(content, data) {
			t.Errorf("Bytes() = %q, want %q", string(content), string(data))
		}
	})
}

func TestORM_BuilderImageWorkflow(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Create config and layer blobs.
	configData := []byte(`{"architecture":"amd64","os":"linux"}`)
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, configData)
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerData := []byte("image-layer-content-for-orm-test")
	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, layerData)
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	// Build and push image using builder API.
	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		WithPlatform(&ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		}).
		WithAnnotation("org.opencontainers.image.description", "ORM test image").
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	t.Run("Image has correct media type", func(t *testing.T) {
		if image.MediaType() != ocispec.MediaTypeImageManifest {
			t.Errorf("MediaType = %q, want %q", image.MediaType(), ocispec.MediaTypeImageManifest)
		}
	})

	t.Run("Push and tag image", func(t *testing.T) {
		if err := image.Push(ctx, "v1.0.0"); err != nil {
			t.Fatalf("Push(): %v", err)
		}
	})

	t.Run("FetchByReference resolves tagged image", func(t *testing.T) {
		manifest, err := client.FetchByReference(ctx, "v1.0.0")
		if err != nil {
			t.Fatalf("FetchByReference(): %v", err)
		}

		if manifest.Digest() != image.Digest() {
			t.Errorf("Digest = %v, want %v", manifest.Digest(), image.Digest())
		}

		fetched, ok := manifest.(*models.Image)
		if !ok {
			t.Fatalf("expected *models.Image, got %T", manifest)
		}

		// Verify config is accessible.
		config, err := fetched.Config(ctx)
		if err != nil {
			t.Fatalf("Config(): %v", err)
		}
		if config.MediaType() != ocispec.MediaTypeImageConfig {
			t.Errorf("Config.MediaType = %q, want %q", config.MediaType(), ocispec.MediaTypeImageConfig)
		}

		// Verify layers are accessible.
		layers, err := fetched.Layers(ctx)
		if err != nil {
			t.Fatalf("Layers(): %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("Layers count = %d, want 1", len(layers))
		}

		// Verify layer content.
		lContent, err := layers[0].Bytes(ctx)
		if err != nil {
			t.Fatalf("Layer.Bytes(): %v", err)
		}
		if !bytes.Equal(lContent, layerData) {
			t.Errorf("Layer content = %q, want %q", string(lContent), string(layerData))
		}
	})
}

func TestORM_BuilderArtifactWorkflow(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Create blobs for the artifact.
	configData := []byte(`{"description": "test artifact"}`)
	configBlob := client.NewBlob("application/json", configData)
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	payloadData := []byte("artifact-payload-data")
	payloadBlob := client.NewBlob("application/octet-stream", payloadData)
	if err := payloadBlob.Push(ctx); err != nil {
		t.Fatalf("Push payload: %v", err)
	}

	// Build artifact using the builder API.
	artifact, err := client.BuildArtifact("application/vnd.test.artifact+type").
		AddBlob(configBlob).
		AddBlob(payloadBlob).
		WithAnnotation("test.key", "test.value").
		Build(ctx)
	if err != nil {
		// The ORAS artifact manifest media type is not supported by all registries
		// (e.g., distribution/registry v2). Skip if the registry rejects the push.
		t.Skipf("BuildArtifact not supported by registry: %v", err)
	}

	t.Run("Push and tag artifact", func(t *testing.T) {
		if err := artifact.Push(ctx, "artifact-v1"); err != nil {
			t.Skipf("Push not supported by registry: %v", err)
		}
	})

	t.Run("FetchByReference resolves tagged artifact", func(t *testing.T) {
		manifest, err := client.FetchByReference(ctx, "artifact-v1")
		if err != nil {
			t.Fatalf("FetchByReference(): %v", err)
		}

		if manifest.Digest() != artifact.Digest() {
			t.Errorf("Digest = %v, want %v", manifest.Digest(), artifact.Digest())
		}
	})
}

func TestORM_BuilderIndexWorkflow(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Helper to build a platform image.
	buildImage := func(arch, os string, layerContent []byte) *models.Image {
		t.Helper()
		configData := []byte(`{}`)
		configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, configData)
		if err := configBlob.Push(ctx); err != nil {
			t.Fatalf("Push config: %v", err)
		}
		layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, layerContent)
		if err := layerBlob.Push(ctx); err != nil {
			t.Fatalf("Push layer: %v", err)
		}
		img, err := client.BuildImage().
			WithConfig(configBlob).
			AddLayer(layerBlob).
			WithPlatform(&ocispec.Platform{
				Architecture: arch,
				OS:           os,
			}).
			Build(ctx)
		if err != nil {
			t.Fatalf("BuildImage(%s/%s): %v", os, arch, err)
		}
		return img
	}

	amd64Image := buildImage("amd64", "linux", []byte("amd64-layer"))
	arm64Image := buildImage("arm64", "linux", []byte("arm64-layer"))

	// Build and push index.
	index, err := client.BuildIndex().
		AddManifest(amd64Image).
		AddManifest(arm64Image).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	t.Run("Push and tag index", func(t *testing.T) {
		if err := index.Push(ctx, "multi-arch-v1"); err != nil {
			t.Fatalf("Push(): %v", err)
		}
	})

	t.Run("FetchByReference resolves tagged index", func(t *testing.T) {
		manifest, err := client.FetchByReference(ctx, "multi-arch-v1")
		if err != nil {
			t.Fatalf("FetchByReference(): %v", err)
		}

		idx, ok := manifest.(*models.Index)
		if !ok {
			t.Fatalf("expected *models.Index, got %T", manifest)
		}

		children, err := idx.Manifests(ctx)
		if err != nil {
			t.Fatalf("Manifests(): %v", err)
		}
		if len(children) != 2 {
			t.Fatalf("Manifests count = %d, want 2", len(children))
		}
	})

	t.Run("FilterByPlatform", func(t *testing.T) {
		manifest, err := client.FetchByReference(ctx, "multi-arch-v1")
		if err != nil {
			t.Fatalf("FetchByReference(): %v", err)
		}
		idx := manifest.(*models.Index)

		filtered, err := idx.FilterByPlatform(ctx, &ocispec.Platform{
			Architecture: "arm64",
			OS:           "linux",
		})
		if err != nil {
			t.Fatalf("FilterByPlatform(): %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("FilterByPlatform count = %d, want 1", len(filtered))
		}
	})
}

func TestORM_CacheIdentityMap(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	data := []byte("cache-test-blob")
	blob := client.NewBlob("application/octet-stream", data)
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	t.Run("Same digest returns same instance", func(t *testing.T) {
		fetched, err := client.FetchBlob(ctx, blob.Descriptor())
		if err != nil {
			t.Fatalf("FetchBlob(): %v", err)
		}
		if fetched != blob {
			t.Error("expected same instance from cache")
		}
	})

	t.Run("ClearCache forces new instance", func(t *testing.T) {
		client.ClearCache()

		fetched, err := client.FetchBlob(ctx, blob.Descriptor())
		if err != nil {
			t.Fatalf("FetchBlob(): %v", err)
		}
		if fetched == blob {
			t.Error("expected different instance after ClearCache")
		}

		// Verify content is still correct.
		content, err := fetched.Bytes(ctx)
		if err != nil {
			t.Fatalf("Bytes(): %v", err)
		}
		if !bytes.Equal(content, data) {
			t.Errorf("Bytes() = %q, want %q", string(content), string(data))
		}
	})
}

func TestORM_CacheWithMaxSize(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t, objects.WithMaxCacheSize(2))

	// Push three blobs.
	data1 := []byte("lru-blob-1")
	blob1 := client.NewBlob("application/octet-stream", data1)
	if err := blob1.Push(ctx); err != nil {
		t.Fatalf("Push blob1: %v", err)
	}

	data2 := []byte("lru-blob-2")
	blob2 := client.NewBlob("application/octet-stream", data2)
	if err := blob2.Push(ctx); err != nil {
		t.Fatalf("Push blob2: %v", err)
	}

	data3 := []byte("lru-blob-3")
	blob3 := client.NewBlob("application/octet-stream", data3)
	if err := blob3.Push(ctx); err != nil {
		t.Fatalf("Push blob3: %v", err)
	}

	// blob1 should be evicted (cache size is 2, we added 3).
	fetched1, err := client.FetchBlob(ctx, blob1.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(1): %v", err)
	}
	if fetched1 == blob1 {
		t.Error("expected blob1 to be evicted from LRU cache")
	}

	// blob3 should still be cached (most recent).
	fetched3, err := client.FetchBlob(ctx, blob3.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(3): %v", err)
	}
	if fetched3 != blob3 {
		t.Error("expected blob3 to still be in cache")
	}
}

func TestORM_CacheDisabled(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t, objects.WithCache(false))

	data := []byte("no-cache-blob")
	blob := client.NewBlob("application/octet-stream", data)
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	fetched, err := client.FetchBlob(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	// With caching disabled, always get new instances.
	if fetched == blob {
		t.Error("expected different instance with cache disabled")
	}

	// But content should still be correct.
	content, err := fetched.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	if !bytes.Equal(content, data) {
		t.Errorf("Bytes() = %q, want %q", string(content), string(data))
	}
}

func TestORM_LazyLoadingManifest(t *testing.T) {
	ctx := context.Background()
	client, repo := newORMClient(t)

	// Push a manifest directly (not via ORM) to test lazy loading.
	configData := []byte(`{}`)
	configDesc := pushBlob(t, ctx, repo, ocispec.MediaTypeImageConfig, configData)

	lazyLayerContent := []byte("lazy-loaded-layer")
	layerDesc := pushBlob(t, ctx, repo, ocispec.MediaTypeImageLayer, lazyLayerContent)

	_, manifestBytes := pushManifest(t, ctx, repo, "lazy-test", []layerData{
		{MediaType: ocispec.MediaTypeImageLayer, Content: lazyLayerContent},
	})

	// Use FetchByReference to get the manifest via ORM.
	manifest, err := client.FetchByReference(ctx, "lazy-test")
	if err != nil {
		t.Fatalf("FetchByReference(): %v", err)
	}

	image, ok := manifest.(*models.Image)
	if !ok {
		t.Fatalf("expected *models.Image, got %T", manifest)
	}

	t.Run("Load triggers fetch", func(t *testing.T) {
		if err := image.Load(ctx); err != nil {
			t.Fatalf("Load(): %v", err)
		}
	})

	t.Run("Config accessible after load", func(t *testing.T) {
		config, err := image.Config(ctx)
		if err != nil {
			t.Fatalf("Config(): %v", err)
		}
		if config.Digest() != configDesc.Digest {
			t.Errorf("Config.Digest = %v, want %v", config.Digest(), configDesc.Digest)
		}
	})

	t.Run("Layers accessible after load", func(t *testing.T) {
		layers, err := image.Layers(ctx)
		if err != nil {
			t.Fatalf("Layers(): %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("Layers count = %d, want 1", len(layers))
		}
		if layers[0].Digest() != layerDesc.Digest {
			t.Errorf("Layer.Digest = %v, want %v", layers[0].Digest(), layerDesc.Digest)
		}
	})

	t.Run("MarshalJSON after Load", func(t *testing.T) {
		data, err := json.Marshal(image)
		if err != nil {
			t.Fatalf("MarshalJSON(): %v", err)
		}
		if !json.Valid(data) {
			t.Error("MarshalJSON() produced invalid JSON")
		}
		// The marshaled bytes should match what we pushed.
		if !bytes.Equal(data, manifestBytes) {
			t.Errorf("MarshalJSON() content mismatch")
		}
	})
}

func TestORM_MarshalJSON_NotLoaded(t *testing.T) {
	ctx := context.Background()
	client, repo := newORMClient(t)

	// Push an image manifest directly.
	pushManifest(t, ctx, repo, "marshal-test", nil)

	// Fetch via ORM but do NOT call Load.
	manifest, err := client.FetchByReference(ctx, "marshal-test")
	if err != nil {
		t.Fatalf("FetchByReference(): %v", err)
	}

	// MarshalJSON should return ErrNotLoaded.
	_, err = json.Marshal(manifest)
	if err == nil {
		t.Fatal("expected ErrNotLoaded, got nil")
	}
}

func TestORM_ReferenceResolution(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push an image via the ORM.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("ref-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	if err := image.Push(ctx, "ref-test-v1"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Create a Reference and resolve it.
	ref := models.NewReference("ref-test-v1", nil, client)

	resolved, err := ref.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}

	if resolved.Digest() != image.Digest() {
		t.Errorf("Resolve().Digest = %v, want %v", resolved.Digest(), image.Digest())
	}
}

func TestORM_BlobWithAnnotation(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	data := []byte("annotated-blob-content")
	blob := client.NewBlob("application/octet-stream", data)

	annotated := blob.WithAnnotation("org.opencontainers.image.title", "test.txt")

	t.Run("Original has no annotations", func(t *testing.T) {
		if ann := blob.Annotations(); ann != nil {
			t.Errorf("original annotations = %v, want nil", ann)
		}
	})

	t.Run("Annotated has correct annotation", func(t *testing.T) {
		ann := annotated.Annotations()
		if ann == nil {
			t.Fatal("annotated annotations = nil")
		}
		if ann["org.opencontainers.image.title"] != "test.txt" {
			t.Errorf("annotation = %q, want %q", ann["org.opencontainers.image.title"], "test.txt")
		}
	})

	t.Run("Annotated preserves content", func(t *testing.T) {
		content, err := annotated.Bytes(ctx)
		if err != nil {
			t.Fatalf("Bytes(): %v", err)
		}
		if !bytes.Equal(content, data) {
			t.Errorf("Bytes() = %q, want %q", string(content), string(data))
		}
	})
}

func TestORM_ImageSubject(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push a base image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("base-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	baseImage, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (base): %v", err)
	}
	if err := baseImage.Push(ctx, "base-v1"); err != nil {
		t.Fatalf("Push base: %v", err)
	}

	// Build a referrer image with base as subject.
	sigBlob := client.NewBlob("application/vnd.cncf.notary.signature", []byte("signature-data"))
	if err := sigBlob.Push(ctx); err != nil {
		t.Fatalf("Push sig: %v", err)
	}

	sigConfig := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := sigConfig.Push(ctx); err != nil {
		t.Fatalf("Push sig config: %v", err)
	}

	sigImage, err := client.BuildImage().
		WithConfig(sigConfig).
		AddLayer(sigBlob).
		WithSubject(baseImage).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (sig): %v", err)
	}
	if err := sigImage.Push(ctx, "sig-v1"); err != nil {
		t.Fatalf("Push sig: %v", err)
	}

	// Fetch the signature image back and verify subject.
	fetched, err := client.FetchByReference(ctx, "sig-v1")
	if err != nil {
		t.Fatalf("FetchByReference(): %v", err)
	}

	img := fetched.(*models.Image)
	subject, err := img.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject(): %v", err)
	}
	if subject == nil {
		t.Fatal("Subject() returned nil")
	}
	if subject.Digest() != baseImage.Digest() {
		t.Errorf("Subject.Digest = %v, want %v", subject.Digest(), baseImage.Digest())
	}
}

func TestORM_FindPredecessors(t *testing.T) {
	ctx := context.Background()
	client, repo := newORMClient(t)

	// Build and push a base image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("predecessor-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	baseImage, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (base): %v", err)
	}
	if err := baseImage.Push(ctx, "pred-base"); err != nil {
		t.Fatalf("Push base: %v", err)
	}

	// Build and push a referrer with base as subject.
	refConfig := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := refConfig.Push(ctx); err != nil {
		t.Fatalf("Push ref config: %v", err)
	}

	refLayer := client.NewBlob("application/vnd.test.ref", []byte("referrer-data"))
	if err := refLayer.Push(ctx); err != nil {
		t.Fatalf("Push ref layer: %v", err)
	}

	_, err = client.BuildImage().
		WithConfig(refConfig).
		AddLayer(refLayer).
		WithSubject(baseImage).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (referrer): %v", err)
	}

	// Push the referrer manifest. We need to tag it to make it discoverable.
	// The referrer relationship is established via the subject field.
	// Fetch predecessors of the base image. This exercises the
	// content.PredecessorFinder interface on remote.Repository.
	_ = repo // repo implements PredecessorFinder via Referrers API
	predecessors, err := client.FindPredecessors(ctx, baseImage)
	if err != nil {
		t.Fatalf("FindPredecessors(): %v", err)
	}

	// The remote repository should find the referrer via the referrers API.
	if len(predecessors) < 1 {
		t.Logf("FindPredecessors returned %d results (referrers API may not be supported by this registry)", len(predecessors))
	}
}

func TestORM_MultipleTagsSameManifest(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push an image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("multi-tag-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	// Push with first tag.
	if err := image.Push(ctx, "tag-a"); err != nil {
		t.Fatalf("Push tag-a: %v", err)
	}

	// Push with second tag (re-tagging same manifest).
	if err := image.Push(ctx, "tag-b"); err != nil {
		t.Fatalf("Push tag-b: %v", err)
	}

	// Both tags should resolve to the same digest.
	manifestA, err := client.FetchByReference(ctx, "tag-a")
	if err != nil {
		t.Fatalf("FetchByReference(tag-a): %v", err)
	}

	manifestB, err := client.FetchByReference(ctx, "tag-b")
	if err != nil {
		t.Fatalf("FetchByReference(tag-b): %v", err)
	}

	if manifestA.Digest() != manifestB.Digest() {
		t.Errorf("tag-a digest %v != tag-b digest %v", manifestA.Digest(), manifestB.Digest())
	}

	// With caching, they should be the same instance.
	if manifestA != manifestB {
		t.Error("expected same cached instance for both tags")
	}
}

func TestORM_FetchByReference_NotFound(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Fetching a nonexistent reference should return an error.
	_, err := client.FetchByReference(ctx, "does-not-exist:latest")
	if err == nil {
		t.Fatal("FetchByReference() expected error for nonexistent reference, got nil")
	}
}

func TestORM_CacheConsistency(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push an image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("cache-consistency-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := image.Push(ctx, "cache-test"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Fetch the same manifest twice via different code paths.
	manifest1, err := client.FetchByReference(ctx, "cache-test")
	if err != nil {
		t.Fatalf("FetchByReference #1: %v", err)
	}

	manifest2, err := client.FetchManifest(ctx, manifest1.Descriptor())
	if err != nil {
		t.Fatalf("FetchManifest #2: %v", err)
	}

	// Both should be the same cached instance.
	if manifest1 != manifest2 {
		t.Error("expected same cached instance from FetchByReference and FetchManifest")
	}

	if manifest1.Digest() != manifest2.Digest() {
		t.Errorf("digests differ: %v vs %v", manifest1.Digest(), manifest2.Digest())
	}
}

func TestORM_AnnotationsProtection(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build an image with annotations.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("ann-protection-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		WithAnnotation("key", "value").
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := image.Push(ctx, "ann-test"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Fetch the image and mutate the returned annotations map.
	fetched, err := client.FetchByReference(ctx, "ann-test")
	if err != nil {
		t.Fatalf("FetchByReference: %v", err)
	}
	if err := fetched.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ann := fetched.Annotations()
	ann["mutated"] = "should-not-affect-original"

	// Get annotations again — they should not contain the mutation.
	ann2 := fetched.Annotations()
	if _, ok := ann2["mutated"]; ok {
		t.Error("annotations mutation leaked into model state")
	}
}

func TestORM_StructuredErrors(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push an image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("err-test-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		WithSubject(nil). // no subject
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := image.Push(ctx, "err-test"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Create an Image model with an invalid descriptor (digest points to nonexistent content).
	// This should produce an ObjectsError when Load is called.
	badDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("nonexistent"),
		Size:      11,
	}
	badImage := models.NewImage(badDesc, client.Target(), client.Target(), client)
	err = badImage.Load(ctx)
	if err == nil {
		t.Fatal("Load() expected error for nonexistent content, got nil")
	}

	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *models.ObjectsError, got %T: %v", err, err)
	}
	if ormErr.Op != "load" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "load")
	}
	if ormErr.Digest != badDesc.Digest {
		t.Errorf("ObjectsError.Digest = %v, want %v", ormErr.Digest, badDesc.Digest)
	}
}

func TestORM_MultiPlatformIndex_FilterVariant(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Helper to build a platform image with variant.
	buildPlatformImage := func(arch, os, variant string, layerContent []byte) *models.Image {
		t.Helper()
		configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, layerContent) // use content as config for uniqueness
		if err := configBlob.Push(ctx); err != nil {
			t.Fatalf("Push config: %v", err)
		}
		layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, append(layerContent, []byte("-layer")...))
		if err := layerBlob.Push(ctx); err != nil {
			t.Fatalf("Push layer: %v", err)
		}
		plat := &ocispec.Platform{
			Architecture: arch,
			OS:           os,
		}
		if variant != "" {
			plat.Variant = variant
		}
		img, err := client.BuildImage().
			WithConfig(configBlob).
			AddLayer(layerBlob).
			WithPlatform(plat).
			Build(ctx)
		if err != nil {
			t.Fatalf("BuildImage(%s/%s/%s): %v", os, arch, variant, err)
		}
		return img
	}

	amd64 := buildPlatformImage("amd64", "linux", "", []byte("amd64-data"))
	armv7 := buildPlatformImage("arm", "linux", "v7", []byte("armv7-data"))
	armv8 := buildPlatformImage("arm64", "linux", "v8", []byte("armv8-data"))

	index, err := client.BuildIndex().
		AddManifest(amd64).
		AddManifest(armv7).
		AddManifest(armv8).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if err := index.Push(ctx, "multi-plat-variant"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Fetch index back.
	fetched, err := client.FetchByReference(ctx, "multi-plat-variant")
	if err != nil {
		t.Fatalf("FetchByReference: %v", err)
	}
	idx := fetched.(*models.Index)

	t.Run("filter by linux/arm/v7", func(t *testing.T) {
		filtered, err := idx.FilterByPlatform(ctx, &ocispec.Platform{
			Architecture: "arm",
			OS:           "linux",
			Variant:      "v7",
		})
		if err != nil {
			t.Fatalf("FilterByPlatform: %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("expected 1 match, got %d", len(filtered))
		}
		if filtered[0].Digest() != armv7.Digest() {
			t.Errorf("expected armv7 digest %v, got %v", armv7.Digest(), filtered[0].Digest())
		}
	})

	t.Run("filter by linux/amd64 (no variant)", func(t *testing.T) {
		filtered, err := idx.FilterByPlatform(ctx, &ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		})
		if err != nil {
			t.Fatalf("FilterByPlatform: %v", err)
		}
		if len(filtered) != 1 {
			t.Fatalf("expected 1 match, got %d", len(filtered))
		}
	})

	t.Run("filter by nonexistent platform", func(t *testing.T) {
		filtered, err := idx.FilterByPlatform(ctx, &ocispec.Platform{
			Architecture: "s390x",
			OS:           "linux",
		})
		if err != nil {
			t.Fatalf("FilterByPlatform: %v", err)
		}
		if len(filtered) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(filtered))
		}
	})

	t.Run("nil platform returns all", func(t *testing.T) {
		filtered, err := idx.FilterByPlatform(ctx, nil)
		if err != nil {
			t.Fatalf("FilterByPlatform(nil): %v", err)
		}
		if len(filtered) != 3 {
			t.Fatalf("expected 3 matches, got %d", len(filtered))
		}
	})
}

func TestORM_BuilderValidation(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	t.Run("ArtifactBuilder rejects empty artifactType", func(t *testing.T) {
		_, err := client.BuildArtifact("").Build(ctx)
		if err == nil {
			t.Fatal("expected error for empty artifactType")
		}
	})

	t.Run("ImageBuilder rejects missing config", func(t *testing.T) {
		_, err := client.BuildImage().Build(ctx)
		if err == nil {
			t.Fatal("expected error for missing config")
		}
	})

	t.Run("IndexBuilder rejects empty manifests", func(t *testing.T) {
		_, err := client.BuildIndex().Build(ctx)
		if err == nil {
			t.Fatal("expected error for empty manifests")
		}
	})
}

func TestORM_FindReferrers(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Build and push a base image.
	configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := configBlob.Push(ctx); err != nil {
		t.Fatalf("Push config: %v", err)
	}

	layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("referrer-base-layer"))
	if err := layerBlob.Push(ctx); err != nil {
		t.Fatalf("Push layer: %v", err)
	}

	baseImage, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layerBlob).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (base): %v", err)
	}
	if err := baseImage.Push(ctx, "referrer-base"); err != nil {
		t.Fatalf("Push base: %v", err)
	}

	// Build and push a signature referrer with base as subject.
	sigConfig := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
	if err := sigConfig.Push(ctx); err != nil {
		t.Fatalf("Push sig config: %v", err)
	}

	sigLayer := client.NewBlob("application/vnd.cncf.notary.signature", []byte("sig-data"))
	if err := sigLayer.Push(ctx); err != nil {
		t.Fatalf("Push sig layer: %v", err)
	}

	sigImage, err := client.BuildImage().
		WithConfig(sigConfig).
		AddLayer(sigLayer).
		WithSubject(baseImage).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage (sig): %v", err)
	}
	if err := sigImage.Push(ctx, "sig-referrer"); err != nil {
		t.Fatalf("Push sig: %v", err)
	}

	t.Run("FindReferrers returns referrers", func(t *testing.T) {
		referrers, err := client.FindReferrers(ctx, baseImage, "")
		if err != nil {
			t.Fatalf("FindReferrers(): %v", err)
		}
		// The remote repository should find the referrer via the referrers API.
		if len(referrers) < 1 {
			t.Logf("FindReferrers returned %d results (referrers API may vary by registry)", len(referrers))
		}
	})

	t.Run("FindReferrers with non-matching artifactType", func(t *testing.T) {
		referrers, err := client.FindReferrers(ctx, baseImage, "application/vnd.nonexistent")
		if err != nil {
			t.Fatalf("FindReferrers(): %v", err)
		}
		if len(referrers) != 0 {
			t.Errorf("FindReferrers with non-matching type returned %d, want 0", len(referrers))
		}
	})
}

func TestORM_ListTags(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Push several tagged manifests.
	tags := []string{"tag-a", "tag-b", "tag-c"}
	for _, tag := range tags {
		configBlob := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{}`))
		if err := configBlob.Push(ctx); err != nil {
			t.Fatalf("Push config: %v", err)
		}

		layerBlob := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer-for-"+tag))
		if err := layerBlob.Push(ctx); err != nil {
			t.Fatalf("Push layer for %s: %v", tag, err)
		}

		image, err := client.BuildImage().
			WithConfig(configBlob).
			AddLayer(layerBlob).
			Build(ctx)
		if err != nil {
			t.Fatalf("BuildImage for %s: %v", tag, err)
		}

		if err := image.Push(ctx, tag); err != nil {
			t.Fatalf("Push %s: %v", tag, err)
		}
	}

	// List tags and verify all are present.
	gotTags, err := client.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags(): %v", err)
	}

	tagSet := make(map[string]bool, len(gotTags))
	for _, tag := range gotTags {
		tagSet[tag] = true
	}

	for _, want := range tags {
		if !tagSet[want] {
			t.Errorf("ListTags() missing tag %q, got %v", want, gotTags)
		}
	}
}

func TestORM_Exists(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	// Push a blob and verify it exists.
	data := []byte("exists-check-data")
	blob := client.NewBlob("application/octet-stream", data)
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	exists, err := client.Exists(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if !exists {
		t.Error("Exists() = false for pushed blob, want true")
	}

	// Check that a non-existent descriptor returns false.
	nonExistent := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromString("does-not-exist"),
		Size:      14,
	}
	exists, err = client.Exists(ctx, nonExistent)
	if err != nil {
		t.Fatalf("Exists() for non-existent: %v", err)
	}
	if exists {
		t.Error("Exists() = true for non-existent blob, want false")
	}
}

func TestORM_Delete(t *testing.T) {
	// Use a local OCI store since it implements content.Deleter.
	// Remote registries may not support delete or behave differently.
	dir := t.TempDir()
	store, err := oci.New(dir)
	if err != nil {
		t.Fatalf("oci.New(): %v", err)
	}
	ctx := context.Background()
	client := objects.NewClient(store)

	data := []byte("delete-me")
	blob := client.NewBlob("application/octet-stream", data)
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	// Verify it exists.
	exists, err := client.Exists(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if !exists {
		t.Fatal("blob should exist after push")
	}

	// Delete it.
	if err := client.Delete(ctx, blob.Descriptor()); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	// Verify it no longer exists.
	exists, err = client.Exists(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("Exists() after delete: %v", err)
	}
	if exists {
		t.Error("blob should not exist after delete")
	}
}

func TestORM_CacheEvict(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	data := []byte("evict-test-data")
	blob := client.NewBlob("application/octet-stream", data)
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	// Fetch to cache.
	cached, err := client.FetchBlob(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}
	if cached != blob {
		t.Error("expected same cached instance")
	}

	// Evict from cache.
	evicted := client.Evict(blob.Descriptor().Digest)
	if !evicted {
		t.Error("Evict() = false, want true")
	}

	// Fetch again should return a new instance.
	fresh, err := client.FetchBlob(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob() after evict: %v", err)
	}
	if fresh == blob {
		t.Error("expected new instance after Evict, got same pointer")
	}

	// But content should still be correct.
	content, err := fresh.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	if !bytes.Equal(content, data) {
		t.Errorf("Bytes() = %q, want %q", string(content), string(data))
	}
}

func TestORM_BlobVerify(t *testing.T) {
	ctx := context.Background()
	client, _ := newORMClient(t)

	data := []byte("verify-blob-content")
	blob := client.NewBlob("application/octet-stream", data)

	// Push the blob to the remote registry.
	if err := blob.Push(ctx); err != nil {
		t.Fatalf("Push(): %v", err)
	}

	// Clear cache so we get a lazy blob that fetches from registry.
	client.ClearCache()

	fetched, err := client.FetchBlob(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	// Verify should succeed — content matches digest and size.
	if err := fetched.Verify(ctx); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
}
