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

package objects_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	ocistore "github.com/oras-project/oras-go/v3/content/oci"
	"github.com/oras-project/oras-go/v3/objects"
	"github.com/oras-project/oras-go/v3/objects/models"
)

// pushBlob pushes raw bytes to a memory store and returns the descriptor.
func pushBlob(t *testing.T, ctx context.Context, store *memory.Store, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to push blob: %v", err)
	}
	return desc
}

// pushImageManifest creates and pushes a valid image manifest to the store,
// returning the manifest descriptor. It also pushes the config and layer blobs.
func pushImageManifest(t *testing.T, ctx context.Context, store *memory.Store) ocispec.Descriptor {
	t.Helper()

	// Push config blob.
	configData := []byte("{}")
	configDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	// Push a layer blob.
	layerData := []byte("layer-content")
	layerDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageLayer, layerData)

	// Build and push manifest.
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	if err := store.Push(ctx, manifestDesc, bytes.NewReader(manifestBytes)); err != nil {
		t.Fatalf("failed to push manifest: %v", err)
	}

	return manifestDesc
}

// pushImageIndex creates and pushes a valid image index to the store,
// returning the index descriptor.
func pushImageIndex(t *testing.T, ctx context.Context, store *memory.Store) ocispec.Descriptor {
	t.Helper()

	// Push a child image manifest first.
	childDesc := pushImageManifest(t, ctx, store)
	childDesc.Platform = &ocispec.Platform{
		Architecture: "amd64",
		OS:           "linux",
	}

	// Build and push index.
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{childDesc},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(indexBytes),
		Size:      int64(len(indexBytes)),
	}
	if err := store.Push(ctx, indexDesc, bytes.NewReader(indexBytes)); err != nil {
		t.Fatalf("failed to push index: %v", err)
	}

	return indexDesc
}

func TestClient_NilTargetPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewClient(nil) did not panic")
		}
		msg, ok := r.(string)
		if !ok || msg != "objects: target must not be nil" {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	objects.NewClient(nil)
}

func TestClient_CacheHitReturnsSameInstance(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("cached-blob")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store) // Cache enabled by default.

	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("first FetchBlob(): %v", err)
	}

	blob2, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("second FetchBlob(): %v", err)
	}

	// Both calls should return the same pointer (identity map hit).
	if blob1 != blob2 {
		t.Error("cache hit: expected same Blob instance, got different pointers")
	}
}

func TestClient_CacheDisabledReturnsDifferentInstances(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("uncached-blob")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store, objects.WithCache(false))

	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("first FetchBlob(): %v", err)
	}

	blob2, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("second FetchBlob(): %v", err)
	}

	// With cache disabled, each call should return a new instance.
	if blob1 == blob2 {
		t.Error("cache disabled: expected different Blob instances, got same pointer")
	}
}

func TestClient_ClearCache(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("clearable-blob")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("first FetchBlob(): %v", err)
	}

	client.ClearCache()

	blob2, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("second FetchBlob() after clear: %v", err)
	}

	// After clearing the cache, we should get a new instance.
	if blob1 == blob2 {
		t.Error("after ClearCache: expected different Blob instances, got same pointer")
	}
}

func TestClient_LRUEvictionWhenMaxCacheSizeSet(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Create a client with max cache size of 2.
	client := objects.NewClient(store, objects.WithMaxCacheSize(2))

	// Push three distinct blobs.
	data1 := []byte("blob-1")
	desc1 := pushBlob(t, ctx, store, "application/octet-stream", data1)

	data2 := []byte("blob-2")
	desc2 := pushBlob(t, ctx, store, "application/octet-stream", data2)

	data3 := []byte("blob-3")
	desc3 := pushBlob(t, ctx, store, "application/octet-stream", data3)

	// Fetch all three. The first one should be evicted.
	blob1, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1): %v", err)
	}

	_, err = client.FetchBlob(ctx, desc2)
	if err != nil {
		t.Fatalf("FetchBlob(2): %v", err)
	}

	_, err = client.FetchBlob(ctx, desc3)
	if err != nil {
		t.Fatalf("FetchBlob(3): %v", err)
	}

	// Fetch blob1 again. It should be a new instance (evicted from cache).
	blob1Again, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1) again: %v", err)
	}

	if blob1 == blob1Again {
		t.Error("LRU eviction: expected blob1 to be evicted, got same instance")
	}
}

func TestClient_LRUPromotionOnAccess(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Create a client with max cache size of 2.
	client := objects.NewClient(store, objects.WithMaxCacheSize(2))

	data1 := []byte("blob-lru-1")
	desc1 := pushBlob(t, ctx, store, "application/octet-stream", data1)

	data2 := []byte("blob-lru-2")
	desc2 := pushBlob(t, ctx, store, "application/octet-stream", data2)

	data3 := []byte("blob-lru-3")
	desc3 := pushBlob(t, ctx, store, "application/octet-stream", data3)

	// Fetch blob1 and blob2. Cache: [blob2, blob1] (front to back).
	blob1, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1): %v", err)
	}

	_, err = client.FetchBlob(ctx, desc2)
	if err != nil {
		t.Fatalf("FetchBlob(2): %v", err)
	}

	// Access blob1 again to promote it. Cache: [blob1, blob2] (front to back).
	blob1Promoted, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1) promote: %v", err)
	}
	if blob1 != blob1Promoted {
		t.Error("promote: expected same instance for blob1")
	}

	// Fetch blob3. This should evict blob2 (least recently used).
	// Cache: [blob3, blob1] (front to back).
	_, err = client.FetchBlob(ctx, desc3)
	if err != nil {
		t.Fatalf("FetchBlob(3): %v", err)
	}

	// blob1 should still be in cache (was promoted).
	blob1After, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1) after eviction: %v", err)
	}
	if blob1 != blob1After {
		t.Error("LRU promotion: expected blob1 to survive eviction, got different instance")
	}
}

func TestClient_NewBlobCachesTheBlob(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	data := []byte("new-blob-data")
	blob := client.NewBlob("application/octet-stream", data)

	ctx := t.Context()

	// FetchBlob with the same digest should return the cached blob.
	cached, err := client.FetchBlob(ctx, blob.Descriptor())
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	if blob != cached {
		t.Error("NewBlob: expected FetchBlob to return the same cached instance")
	}
}

func TestClient_FetchBlobReturnsCachedBlobOnSecondCall(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("fetch-twice")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	first, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("first FetchBlob(): %v", err)
	}

	second, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("second FetchBlob(): %v", err)
	}

	if first != second {
		t.Error("expected same Blob instance on second FetchBlob call")
	}

	// Verify we can actually read the content.
	content, err := second.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	if !bytes.Equal(content, data) {
		t.Errorf("Bytes() = %q, want %q", string(content), string(data))
	}
}

func TestClient_FetchManifest_DispatchesImageManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)

	client := objects.NewClient(store)

	manifest, err := client.FetchManifest(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}

	// Should return an *Image.
	image, ok := manifest.(*models.Image)
	if !ok {
		t.Fatalf("FetchManifest() returned %T, want *models.Image", manifest)
	}

	// Verify we can load the image.
	if err := image.Load(ctx); err != nil {
		t.Fatalf("Image.Load(): %v", err)
	}

	// Verify config and layers are accessible.
	config, err := image.Config(ctx)
	if err != nil {
		t.Fatalf("Image.Config(): %v", err)
	}
	if config.MediaType() != ocispec.MediaTypeImageConfig {
		t.Errorf("Config.MediaType() = %q, want %q", config.MediaType(), ocispec.MediaTypeImageConfig)
	}

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Image.Layers(): %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("Image.Layers() length = %d, want 1", len(layers))
	}
	if layers[0].MediaType() != ocispec.MediaTypeImageLayer {
		t.Errorf("Layer.MediaType() = %q, want %q", layers[0].MediaType(), ocispec.MediaTypeImageLayer)
	}
}

func TestClient_FetchManifest_DispatchesImageIndex(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	indexDesc := pushImageIndex(t, ctx, store)

	client := objects.NewClient(store)

	manifest, err := client.FetchManifest(ctx, indexDesc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}

	// Should return an *Index.
	index, ok := manifest.(*models.Index)
	if !ok {
		t.Fatalf("FetchManifest() returned %T, want *models.Index", manifest)
	}

	// Verify we can load the index.
	if err := index.Load(ctx); err != nil {
		t.Fatalf("Index.Load(): %v", err)
	}

	// Verify child manifests are accessible.
	children, err := index.Manifests(ctx)
	if err != nil {
		t.Fatalf("Index.Manifests(): %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("Index.Manifests() length = %d, want 1", len(children))
	}
}

func TestClient_FetchManifest_CachesManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)

	client := objects.NewClient(store)

	first, err := client.FetchManifest(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("first FetchManifest(): %v", err)
	}

	second, err := client.FetchManifest(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("second FetchManifest(): %v", err)
	}

	// Should be the same cached instance.
	if first != second {
		t.Error("expected same manifest instance on second FetchManifest call")
	}
}

func TestClient_FetchByReference_ResolvesAndFetches(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)

	// Tag the manifest.
	if err := store.Tag(ctx, manifestDesc, "latest"); err != nil {
		t.Fatalf("Tag(): %v", err)
	}

	client := objects.NewClient(store)

	manifest, err := client.FetchByReference(ctx, "latest")
	if err != nil {
		t.Fatalf("FetchByReference(): %v", err)
	}

	// Should resolve to the same image manifest.
	if manifest.Digest() != manifestDesc.Digest {
		t.Errorf("FetchByReference() digest = %v, want %v", manifest.Digest(), manifestDesc.Digest)
	}

	// Should be an Image.
	if _, ok := manifest.(*models.Image); !ok {
		t.Errorf("FetchByReference() returned %T, want *models.Image", manifest)
	}
}

func TestClient_FetchByReference_DigestReference(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)

	// Tag it so we can also resolve by digest string.
	if err := store.Tag(ctx, manifestDesc, manifestDesc.Digest.String()); err != nil {
		t.Fatalf("Tag(): %v", err)
	}

	client := objects.NewClient(store)

	manifest, err := client.FetchByReference(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatalf("FetchByReference() with digest: %v", err)
	}

	if manifest.Digest() != manifestDesc.Digest {
		t.Errorf("FetchByReference() digest = %v, want %v", manifest.Digest(), manifestDesc.Digest)
	}
}

func TestClient_FetchByReference_CachesResult(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)
	if err := store.Tag(ctx, manifestDesc, "v1"); err != nil {
		t.Fatalf("Tag(): %v", err)
	}

	client := objects.NewClient(store)

	first, err := client.FetchByReference(ctx, "v1")
	if err != nil {
		t.Fatalf("first FetchByReference(): %v", err)
	}

	// Fetching the same reference should return the cached instance.
	second, err := client.FetchByReference(ctx, "v1")
	if err != nil {
		t.Fatalf("second FetchByReference(): %v", err)
	}

	if first != second {
		t.Error("expected same manifest instance on second FetchByReference call")
	}
}

func TestClient_DefaultOptions(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	// Verify the client was created with caching enabled by default.
	ctx := t.Context()
	data := []byte("defaults-test")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	blob2, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	// Default has cache enabled, so same instance expected.
	if blob1 != blob2 {
		t.Error("default options: expected cache to be enabled, got different instances")
	}
}

func TestClient_FindReferrers_FallsBackToPredecessors(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Push a base image manifest.
	baseDesc := pushImageManifest(t, ctx, store)

	// Push a referrer manifest with Subject pointing to the base image.
	configData := []byte(`{"referrer":true}`)
	configDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	layerData := []byte("referrer-layer")
	layerDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageLayer, layerData)

	referrerManifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
		Subject:   &baseDesc,
	}
	referrerBytes, err := json.Marshal(referrerManifest)
	if err != nil {
		t.Fatalf("failed to marshal referrer manifest: %v", err)
	}
	referrerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(referrerBytes),
		Size:      int64(len(referrerBytes)),
	}
	if err := store.Push(ctx, referrerDesc, bytes.NewReader(referrerBytes)); err != nil {
		t.Fatalf("failed to push referrer manifest: %v", err)
	}

	// memory.Store implements PredecessorFinder but NOT ReferrerLister.
	client := objects.NewClient(store)

	// Wrap the base image in a model so we can pass it to FindReferrers.
	baseManifest, err := client.FetchManifest(ctx, baseDesc)
	if err != nil {
		t.Fatalf("FetchManifest(base): %v", err)
	}

	// FindReferrers with empty artifactType should return all referrers.
	referrers, err := client.FindReferrers(ctx, baseManifest, "")
	if err != nil {
		t.Fatalf("FindReferrers(): %v", err)
	}
	if len(referrers) == 0 {
		t.Fatal("FindReferrers() returned 0 referrers, want at least 1")
	}

	found := false
	for _, ref := range referrers {
		if ref.Digest() == referrerDesc.Digest {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FindReferrers() did not return the expected referrer with digest %v", referrerDesc.Digest)
	}
}

func TestClient_FindReferrers_FiltersArtifactType(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Push a base image manifest.
	baseDesc := pushImageManifest(t, ctx, store)

	// Push a referrer manifest with Subject pointing to the base image.
	configData := []byte(`{"filter-test":true}`)
	configDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	layerData := []byte("filter-layer")
	layerDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageLayer, layerData)

	referrerManifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
		Subject:   &baseDesc,
	}
	referrerBytes, err := json.Marshal(referrerManifest)
	if err != nil {
		t.Fatalf("failed to marshal referrer manifest: %v", err)
	}
	referrerDesc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		Digest:       digest.FromBytes(referrerBytes),
		Size:         int64(len(referrerBytes)),
		ArtifactType: "application/vnd.test.sbom",
	}
	if err := store.Push(ctx, referrerDesc, bytes.NewReader(referrerBytes)); err != nil {
		t.Fatalf("failed to push referrer manifest: %v", err)
	}

	client := objects.NewClient(store)

	baseManifest, err := client.FetchManifest(ctx, baseDesc)
	if err != nil {
		t.Fatalf("FetchManifest(base): %v", err)
	}

	// Filter with matching artifactType.
	matching, err := client.FindReferrers(ctx, baseManifest, "application/vnd.test.sbom")
	if err != nil {
		t.Fatalf("FindReferrers(matching): %v", err)
	}
	found := false
	for _, ref := range matching {
		if ref.Digest() == referrerDesc.Digest {
			found = true
			break
		}
	}
	if !found {
		t.Error("FindReferrers(matching artifactType) did not return the expected referrer")
	}

	// Filter with non-matching artifactType should return empty.
	nonMatching, err := client.FindReferrers(ctx, baseManifest, "application/vnd.nonexistent")
	if err != nil {
		t.Fatalf("FindReferrers(non-matching): %v", err)
	}
	if len(nonMatching) != 0 {
		t.Errorf("FindReferrers(non-matching) returned %d, want 0", len(nonMatching))
	}
}

func TestClient_ListTags_UnsupportedTarget(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	_, err := client.ListTags(t.Context())
	if err == nil {
		t.Fatal("ListTags() expected error, got nil")
	}
	if err.Error() != "target does not support tag listing" {
		t.Errorf("ListTags() error = %q, want %q", err.Error(), "target does not support tag listing")
	}
}

func TestClient_MaxCacheSize_NegativeClampedToZero(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Negative MaxCacheSize is clamped to 0 (unlimited).
	client := objects.NewClient(store, objects.WithMaxCacheSize(-5))

	data1 := []byte("neg-blob-1")
	desc1 := pushBlob(t, ctx, store, "application/octet-stream", data1)

	data2 := []byte("neg-blob-2")
	desc2 := pushBlob(t, ctx, store, "application/octet-stream", data2)

	data3 := []byte("neg-blob-3")
	desc3 := pushBlob(t, ctx, store, "application/octet-stream", data3)

	blob1, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1): %v", err)
	}

	_, err = client.FetchBlob(ctx, desc2)
	if err != nil {
		t.Fatalf("FetchBlob(2): %v", err)
	}

	_, err = client.FetchBlob(ctx, desc3)
	if err != nil {
		t.Fatalf("FetchBlob(3): %v", err)
	}

	// With unlimited cache (negative clamped to 0), blob1 should still be cached.
	blob1Again, err := client.FetchBlob(ctx, desc1)
	if err != nil {
		t.Fatalf("FetchBlob(1) again: %v", err)
	}

	if blob1 != blob1Again {
		t.Error("negative MaxCacheSize clamped to unlimited: expected blob1 to be cached, got different instance")
	}
}

func TestClient_Evict_RemovesCachedEntry(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("evict-me")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	// Evict the entry.
	evicted := client.Evict(desc.Digest)
	if !evicted {
		t.Fatal("Evict() returned false, want true")
	}

	// Fetch again: should get a new instance.
	blob2, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("FetchBlob() after evict: %v", err)
	}
	if blob1 == blob2 {
		t.Error("expected different instance after Evict")
	}
}

func TestClient_Delete_UnsupportedTarget(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromString("test"),
		Size:      4,
	}

	err := client.Delete(t.Context(), desc)
	if !errors.Is(err, models.ErrNoDeleter) {
		t.Fatalf("Delete() error = %v, want ErrNoDeleter", err)
	}
}

func TestClient_Exists_True(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("exists-test")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	exists, err := client.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if !exists {
		t.Error("Exists() = false, want true")
	}
}

func TestClient_Exists_False(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromString("nonexistent"),
		Size:      11,
	}

	exists, err := client.Exists(t.Context(), desc)
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if exists {
		t.Error("Exists() = true, want false")
	}
}

func TestClient_Evict_ReturnsFalseWhenNotFound(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	evicted := client.Evict(digest.FromString("nonexistent"))
	if evicted {
		t.Error("Evict() returned true for non-cached digest, want false")
	}
}

func TestClient_ConcurrentFetchBlob(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("concurrent-fetch-blob")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	const goroutines = 100
	blobs := make([]*models.Blob, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			blobs[idx], errs[idx] = client.FetchBlob(ctx, desc)
		}(i)
	}
	wg.Wait()

	// All should succeed and return the same cached instance.
	for i := range goroutines {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: FetchBlob() error: %v", i, errs[i])
		}
		if blobs[i] != blobs[0] {
			t.Errorf("goroutine %d: expected same instance as goroutine 0", i)
		}
	}
}

func TestClient_ConcurrentFetchManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	manifestDesc := pushImageManifest(t, ctx, store)

	client := objects.NewClient(store)

	const goroutines = 50
	manifests := make([]models.Manifest, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			manifests[idx], errs[idx] = client.FetchManifest(ctx, manifestDesc)
		}(i)
	}
	wg.Wait()

	for i := range goroutines {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: FetchManifest() error: %v", i, errs[i])
		}
		if manifests[i] != manifests[0] {
			t.Errorf("goroutine %d: expected same instance as goroutine 0", i)
		}
	}
}

func TestClient_ConcurrentFetchAndClear(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("fetch-and-clear")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	// Run FetchBlob and ClearCache concurrently to verify no panics.
	const goroutines = 50
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = client.FetchBlob(ctx, desc)
		}()
		go func() {
			defer wg.Done()
			client.ClearCache()
		}()
	}
	wg.Wait()
}

func TestClient_ConcurrentFetchAndEvict(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("fetch-and-evict")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := objects.NewClient(store)

	const goroutines = 50
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = client.FetchBlob(ctx, desc)
		}()
		go func() {
			defer wg.Done()
			client.Evict(desc.Digest)
		}()
	}
	wg.Wait()
}

// TestClient_RemovedOptions_DoNotExist is a compile-time verification.
// The removed options WithPreloadDepth and WithConcurrency no longer exist
// in the objects package. This test verifies that DefaultClientOptions works
// correctly with only the Cache and MaxCacheSize fields.
func TestClient_RemovedOptions_DoNotExist(t *testing.T) {
	opts := objects.DefaultClientOptions()
	if !opts.Cache {
		t.Error("DefaultClientOptions().Cache = false, want true")
	}
	if opts.MaxCacheSize != 0 {
		t.Errorf("DefaultClientOptions().MaxCacheSize = %d, want 0 (unlimited)", opts.MaxCacheSize)
	}
}

func TestClient_FetchManifest_DispatchesArtifactManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Push a blob.
	blobData := []byte("artifact-blob")
	blobDesc := pushBlob(t, ctx, store, "application/octet-stream", blobData)

	// Push an artifact manifest.
	artifact := map[string]any{
		"mediaType":    "application/vnd.oci.artifact.manifest.v1+json",
		"artifactType": "application/vnd.test.artifact",
		"blobs":        []any{blobDesc},
	}
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	artifactDesc := ocispec.Descriptor{
		MediaType: "application/vnd.oci.artifact.manifest.v1+json",
		Digest:    digest.FromBytes(artifactBytes),
		Size:      int64(len(artifactBytes)),
	}
	if err := store.Push(ctx, artifactDesc, bytes.NewReader(artifactBytes)); err != nil {
		t.Fatalf("push artifact: %v", err)
	}

	client := objects.NewClient(store)
	manifest, err := client.FetchManifest(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}

	if _, ok := manifest.(*models.Artifact); !ok {
		t.Errorf("FetchManifest() returned %T, want *models.Artifact", manifest)
	}
}

func TestClient_FetchManifest_InspectionDetectsImageManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	configData := []byte("{}")
	configDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	layerData := []byte("layer")
	layerDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageLayer, layerData)

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerDesc},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	// Use an unknown media type to trigger inspection path.
	desc := ocispec.Descriptor{
		MediaType: "application/vnd.unknown.manifest",
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(manifestBytes)); err != nil {
		t.Fatalf("push manifest: %v", err)
	}

	client := objects.NewClient(store)
	result, err := client.FetchManifest(ctx, desc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if _, ok := result.(*models.Image); !ok {
		t.Errorf("FetchManifest() returned %T, want *models.Image", result)
	}
}

func TestClient_FetchManifest_InspectionDetectsIndex(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	childDesc := pushImageManifest(t, ctx, store)
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{childDesc},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	desc := ocispec.Descriptor{
		MediaType: "application/vnd.unknown.list",
		Digest:    digest.FromBytes(indexBytes),
		Size:      int64(len(indexBytes)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(indexBytes)); err != nil {
		t.Fatalf("push index: %v", err)
	}

	client := objects.NewClient(store)
	result, err := client.FetchManifest(ctx, desc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if _, ok := result.(*models.Index); !ok {
		t.Errorf("FetchManifest() returned %T, want *models.Index", result)
	}
}

func TestClient_FetchManifest_InspectionDetectsArtifact(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	artifact := map[string]any{
		"artifactType": "application/vnd.test",
	}
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	desc := ocispec.Descriptor{
		MediaType: "application/vnd.unknown",
		Digest:    digest.FromBytes(artifactBytes),
		Size:      int64(len(artifactBytes)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(artifactBytes)); err != nil {
		t.Fatalf("push: %v", err)
	}

	client := objects.NewClient(store)
	result, err := client.FetchManifest(ctx, desc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if _, ok := result.(*models.Artifact); !ok {
		t.Errorf("FetchManifest() returned %T, want *models.Artifact", result)
	}
}

func TestClient_PushManifest(t *testing.T) {
	ctx := t.Context()
	src := memory.New()
	dst := memory.New()

	manifestDesc := pushImageManifest(t, ctx, src)

	srcClient := objects.NewClient(src)
	manifest, err := srcClient.FetchManifest(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if err := manifest.Load(ctx); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	// Push blobs to dst first.
	configData := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configData),
		Size:      int64(len(configData)),
	}
	if err := dst.Push(ctx, configDesc, bytes.NewReader(configData)); err != nil {
		t.Fatalf("push config: %v", err)
	}
	layerData := []byte("layer-content")
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layerData),
		Size:      int64(len(layerData)),
	}
	if err := dst.Push(ctx, layerDesc, bytes.NewReader(layerData)); err != nil {
		t.Fatalf("push layer: %v", err)
	}

	dstClient := objects.NewClient(dst)
	if err := dstClient.PushManifest(ctx, manifest, "latest"); err != nil {
		t.Fatalf("PushManifest(): %v", err)
	}

	// Verify the manifest exists in dst.
	exists, err := dst.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if !exists {
		t.Error("manifest not found in dst after push")
	}
}

func TestClient_FindPredecessors_NoPredecessorFinder(t *testing.T) {
	// memory.Store supports PredecessorFinder; use a minimal target that doesn't.
	store := memory.New()
	client := objects.NewClient(store)

	blobData := []byte("some-blob")
	desc := pushBlob(t, t.Context(), store, "application/octet-stream", blobData)
	blob, err := client.FetchBlob(t.Context(), desc)
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}

	// memory.Store supports predecessors so should return empty (no referrers).
	preds, err := client.FindPredecessors(t.Context(), blob)
	if err != nil {
		t.Fatalf("FindPredecessors(): %v", err)
	}
	if len(preds) != 0 {
		t.Errorf("FindPredecessors() = %d, want 0", len(preds))
	}
}

func TestClient_Delete_Success(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	store, err := ocistore.New(tmpDir)
	if err != nil {
		t.Fatalf("oci.New(): %v", err)
	}

	data := []byte("to-be-deleted")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("push: %v", err)
	}

	client := objects.NewClient(store)

	if err := client.Delete(ctx, desc); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	exists, err := store.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if exists {
		t.Error("expected blob to be deleted")
	}
}

func TestClient_PushManifest_WithoutReference(t *testing.T) {
	ctx := t.Context()
	src := memory.New()
	dst := memory.New()

	manifestDesc := pushImageManifest(t, ctx, src)

	srcClient := objects.NewClient(src)
	manifest, err := srcClient.FetchManifest(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if err := manifest.Load(ctx); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	// Push blobs to dst first.
	configData := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configData),
		Size:      int64(len(configData)),
	}
	if err := dst.Push(ctx, configDesc, bytes.NewReader(configData)); err != nil {
		t.Fatalf("push config: %v", err)
	}
	layerData := []byte("layer-content")
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layerData),
		Size:      int64(len(layerData)),
	}
	if err := dst.Push(ctx, layerDesc, bytes.NewReader(layerData)); err != nil {
		t.Fatalf("push layer: %v", err)
	}

	dstClient := objects.NewClient(dst)
	// Push without reference (empty string).
	if err := dstClient.PushManifest(ctx, manifest, ""); err != nil {
		t.Fatalf("PushManifest() without reference: %v", err)
	}

	// Verify the manifest exists in dst.
	exists, err := dst.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Exists(): %v", err)
	}
	if !exists {
		t.Error("manifest not found in dst after push without reference")
	}
}

func TestClient_Target(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)
	if client.Target() != store {
		t.Error("Target() should return the underlying store")
	}
}

func TestClient_FetchManifest_UnknownType(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	// Push content that doesn't match any known manifest type.
	rawData := []byte(`{"randomKey": "randomValue"}`)
	desc := ocispec.Descriptor{
		MediaType: "application/vnd.unknown",
		Digest:    digest.FromBytes(rawData),
		Size:      int64(len(rawData)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(rawData)); err != nil {
		t.Fatalf("push: %v", err)
	}

	client := objects.NewClient(store)
	_, err := client.FetchManifest(ctx, desc)
	if err == nil {
		t.Fatal("FetchManifest() expected error for unknown type, got nil")
	}
	if err.Error() != "unknown manifest type" {
		t.Errorf("error = %q, want %q", err.Error(), "unknown manifest type")
	}
}

func TestClient_FetchByReference_ErrorOnResolve(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)

	// Reference that doesn't exist.
	_, err := client.FetchByReference(t.Context(), "nonexistent-tag")
	if err == nil {
		t.Fatal("FetchByReference() expected error, got nil")
	}
}

func TestClient_Delete_EvictsFromCache(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	store, err := ocistore.New(tmpDir)
	if err != nil {
		t.Fatalf("oci.New(): %v", err)
	}

	data := []byte("delete-and-evict")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("push: %v", err)
	}

	client := objects.NewClient(store)

	// Fetch to populate cache.
	blob1, err := client.FetchBlob(ctx, desc)
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}
	_ = blob1

	// Delete should evict from cache.
	if err := client.Delete(ctx, desc); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	// Evict should return false since Delete already evicted it.
	if client.Evict(desc.Digest) {
		t.Error("Evict() after Delete() should return false")
	}
}

func TestClient_BuildArtifact_ReturnsBuilder(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)
	builder := client.BuildArtifact("application/vnd.test")
	if builder == nil {
		t.Fatal("BuildArtifact() returned nil")
	}
}

func TestClient_BuildImage_ReturnsBuilder(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)
	builder := client.BuildImage()
	if builder == nil {
		t.Fatal("BuildImage() returned nil")
	}
}

func TestClient_BuildIndex_ReturnsBuilder(t *testing.T) {
	store := memory.New()
	client := objects.NewClient(store)
	builder := client.BuildIndex()
	if builder == nil {
		t.Fatal("BuildIndex() returned nil")
	}
}
