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

package orm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/orm"
	"github.com/oras-project/oras-go/v3/orm/models"
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
		if !ok || msg != "orm: target must not be nil" {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	orm.NewClient(nil)
}

func TestClient_CacheHitReturnsSameInstance(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	data := []byte("cached-blob")
	desc := pushBlob(t, ctx, store, "application/octet-stream", data)

	client := orm.NewClient(store) // Cache enabled by default.

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

	client := orm.NewClient(store, orm.WithCache(false))

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

	client := orm.NewClient(store)

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
	client := orm.NewClient(store, orm.WithMaxCacheSize(2))

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
	client := orm.NewClient(store, orm.WithMaxCacheSize(2))

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
	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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
	client := orm.NewClient(store)

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
	client := orm.NewClient(store)

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

	client := orm.NewClient(store)

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
	client := orm.NewClient(store)

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
	client := orm.NewClient(store, orm.WithMaxCacheSize(-5))

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

	client := orm.NewClient(store)

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

func TestClient_Evict_ReturnsFalseWhenNotFound(t *testing.T) {
	store := memory.New()
	client := orm.NewClient(store)

	evicted := client.Evict(digest.FromString("nonexistent"))
	if evicted {
		t.Error("Evict() returned true for non-cached digest, want false")
	}
}

// TestClient_RemovedOptions_DoNotExist is a compile-time verification.
// The removed options WithPreloadDepth and WithConcurrency no longer exist
// in the orm package. This test verifies that DefaultClientOptions works
// correctly with only the Cache and MaxCacheSize fields.
func TestClient_RemovedOptions_DoNotExist(t *testing.T) {
	opts := orm.DefaultClientOptions()
	if !opts.Cache {
		t.Error("DefaultClientOptions().Cache = false, want true")
	}
	if opts.MaxCacheSize != 0 {
		t.Errorf("DefaultClientOptions().MaxCacheSize = %d, want 0 (unlimited)", opts.MaxCacheSize)
	}
}
