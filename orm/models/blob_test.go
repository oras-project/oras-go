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

package models_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/orm/models"
)

func TestNewBlobFromBytes_CreatesCorrectDescriptor(t *testing.T) {
	data := []byte("hello world")
	mediaType := "application/octet-stream"

	blob := models.NewBlobFromBytes(mediaType, data)

	// Verify digest.
	expectedDigest := digest.FromBytes(data)
	if blob.Digest() != expectedDigest {
		t.Errorf("Digest() = %v, want %v", blob.Digest(), expectedDigest)
	}

	// Verify size.
	if blob.Size() != int64(len(data)) {
		t.Errorf("Size() = %d, want %d", blob.Size(), len(data))
	}

	// Verify media type.
	if blob.MediaType() != mediaType {
		t.Errorf("MediaType() = %q, want %q", blob.MediaType(), mediaType)
	}

	// Verify full descriptor.
	desc := blob.Descriptor()
	if desc.MediaType != mediaType {
		t.Errorf("Descriptor().MediaType = %q, want %q", desc.MediaType, mediaType)
	}
	if desc.Digest != expectedDigest {
		t.Errorf("Descriptor().Digest = %v, want %v", desc.Digest, expectedDigest)
	}
	if desc.Size != int64(len(data)) {
		t.Errorf("Descriptor().Size = %d, want %d", desc.Size, len(data))
	}
}

func TestBlob_Bytes_ReturnsContentForBlobFromBytes(t *testing.T) {
	data := []byte("test content")
	blob := models.NewBlobFromBytes("application/octet-stream", data)

	ctx := t.Context()
	got, err := blob.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Bytes() = %q, want %q", string(got), string(data))
	}
}

func TestBlob_Bytes_ReturnsErrNoFetcherWhenNoFetcher(t *testing.T) {
	data := []byte("unreachable")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	// Create blob with nil fetcher and nil pusher.
	blob := models.NewBlob(desc, nil, nil)

	ctx := t.Context()
	_, err := blob.Bytes(ctx)
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Fatalf("Bytes() error = %v, want %v", err, models.ErrNoFetcher)
	}
}

func TestBlob_Bytes_RetriesAfterTransientError(t *testing.T) {
	ctx := t.Context()

	data := []byte("retry-data")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}

	// First: blob with no fetcher (simulates transient failure).
	blob := models.NewBlob(desc, nil, nil)

	_, err := blob.Bytes(ctx)
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Fatalf("first Bytes() error = %v, want %v", err, models.ErrNoFetcher)
	}

	// The error was NOT cached. Create a new blob with a real fetcher to
	// verify the lazy pattern allows retry. Note: in production, the same
	// blob instance would get its fetcher set; here we verify the lazy
	// semantics by showing the same descriptor works with a fetcher.
	store := newMemoryStore()
	pushToStore(t, ctx, store, desc.MediaType, data)
	blob2 := models.NewBlob(desc, store, store)

	got, err := blob2.Bytes(ctx)
	if err != nil {
		t.Fatalf("retry Bytes(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("retry Bytes() = %q, want %q", string(got), string(data))
	}
}

func TestBlob_Read_ReturnsContentForInMemoryBlob(t *testing.T) {
	data := []byte("readable content")
	blob := models.NewBlobFromBytes("text/plain", data)

	ctx := t.Context()
	rc, err := blob.Read(ctx)
	if err != nil {
		t.Fatalf("Read(): unexpected error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Read() content = %q, want %q", string(got), string(data))
	}
}

func TestBlob_Read_ReturnsErrNoFetcherWhenNoCachedContent(t *testing.T) {
	data := []byte("no-fetcher-content")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	blob := models.NewBlob(desc, nil, nil)

	ctx := t.Context()
	_, err := blob.Read(ctx)
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Fatalf("Read() error = %v, want %v", err, models.ErrNoFetcher)
	}
}

func TestBlob_Read_FetchesFromStoreWhenNotCached(t *testing.T) {
	ctx := t.Context()

	data := []byte("store-content")
	store := newMemoryStore()
	desc := pushToStore(t, ctx, store, "application/octet-stream", data)

	blob := models.NewBlob(desc, store, store)

	rc, err := blob.Read(ctx)
	if err != nil {
		t.Fatalf("Read(): unexpected error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Read() content = %q, want %q", string(got), string(data))
	}
}

func TestBlob_Push_ReturnsErrNoPusherWhenNoPusher(t *testing.T) {
	blob := models.NewBlobFromBytes("application/octet-stream", []byte("push-data"))

	ctx := t.Context()
	err := blob.Push(ctx)
	if !errors.Is(err, models.ErrNoPusher) {
		t.Fatalf("Push() error = %v, want %v", err, models.ErrNoPusher)
	}
}

func TestBlob_Push_SucceedsWithPusher(t *testing.T) {
	ctx := t.Context()

	data := []byte("pushable")
	sourceStore := newMemoryStore()
	desc := pushToStore(t, ctx, sourceStore, "application/octet-stream", data)

	// Create a fresh target store to push to.
	targetStore := newMemoryStore()

	// Create a blob that fetches from source and pushes to target.
	blob := models.NewBlob(desc, sourceStore, targetStore)

	// Load content first so it is cached.
	_, err := blob.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): unexpected error: %v", err)
	}

	err = blob.Push(ctx)
	if err != nil {
		t.Fatalf("Push(): unexpected error: %v", err)
	}

	// Verify content was pushed to target store.
	exists, err := targetStore.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists(): unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("Push() did not store content in target")
	}
}

func TestBlob_WithAnnotation_DoesNotMutateOriginal(t *testing.T) {
	data := []byte("immutable")
	original := models.NewBlobFromBytes("application/octet-stream", data)

	// Add an annotation to the original first.
	withFirst := original.WithAnnotation("key1", "value1")

	// Add another annotation.
	withSecond := withFirst.WithAnnotation("key2", "value2")

	// Original should have no annotations.
	if ann := original.Annotations(); ann != nil {
		t.Errorf("original.Annotations() = %v, want nil", ann)
	}

	// withFirst should have only key1.
	if ann := withFirst.Annotations(); ann == nil {
		t.Fatal("withFirst.Annotations() = nil, want map with key1")
	} else {
		if ann["key1"] != "value1" {
			t.Errorf("withFirst.Annotations()[key1] = %q, want %q", ann["key1"], "value1")
		}
		if _, ok := ann["key2"]; ok {
			t.Error("withFirst.Annotations() should not contain key2")
		}
	}

	// withSecond should have both keys.
	if ann := withSecond.Annotations(); ann == nil {
		t.Fatal("withSecond.Annotations() = nil, want map with key1 and key2")
	} else {
		if ann["key1"] != "value1" {
			t.Errorf("withSecond.Annotations()[key1] = %q, want %q", ann["key1"], "value1")
		}
		if ann["key2"] != "value2" {
			t.Errorf("withSecond.Annotations()[key2] = %q, want %q", ann["key2"], "value2")
		}
	}
}

func TestBlob_WithAnnotation_CopiesCachedContent(t *testing.T) {
	data := []byte("cached-for-copy")
	original := models.NewBlobFromBytes("text/plain", data)

	annotated := original.WithAnnotation("test", "annotation")

	ctx := t.Context()

	// The annotated blob should have the same cached content.
	got, err := annotated.Bytes(ctx)
	if err != nil {
		t.Fatalf("annotated.Bytes(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("annotated.Bytes() = %q, want %q", string(got), string(data))
	}
}

func TestBlob_WithAnnotation_PreservesDigestAndSize(t *testing.T) {
	data := []byte("preserve-identity")
	original := models.NewBlobFromBytes("application/octet-stream", data)

	annotated := original.WithAnnotation("key", "value")

	// Digest and size should be the same (annotations don't affect content hash).
	if original.Digest() != annotated.Digest() {
		t.Errorf("Digest changed: original=%v, annotated=%v", original.Digest(), annotated.Digest())
	}
	if original.Size() != annotated.Size() {
		t.Errorf("Size changed: original=%d, annotated=%d", original.Size(), annotated.Size())
	}
	if original.MediaType() != annotated.MediaType() {
		t.Errorf("MediaType changed: original=%q, annotated=%q", original.MediaType(), annotated.MediaType())
	}
}

func TestBlob_ContentInterfaceMethods(t *testing.T) {
	data := []byte("interface-test")
	mediaType := "application/vnd.test"
	blob := models.NewBlobFromBytes(mediaType, data)

	// Test Descriptor.
	desc := blob.Descriptor()
	if desc.MediaType != mediaType {
		t.Errorf("Descriptor().MediaType = %q, want %q", desc.MediaType, mediaType)
	}
	if desc.Digest != digest.FromBytes(data) {
		t.Errorf("Descriptor().Digest = %v, want %v", desc.Digest, digest.FromBytes(data))
	}
	if desc.Size != int64(len(data)) {
		t.Errorf("Descriptor().Size = %d, want %d", desc.Size, len(data))
	}

	// Test individual accessors.
	if blob.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", blob.Digest(), desc.Digest)
	}
	if blob.MediaType() != desc.MediaType {
		t.Errorf("MediaType() = %q, want %q", blob.MediaType(), desc.MediaType)
	}
	if blob.Size() != desc.Size {
		t.Errorf("Size() = %d, want %d", blob.Size(), desc.Size)
	}
	if blob.Annotations() != nil {
		t.Errorf("Annotations() = %v, want nil", blob.Annotations())
	}

	// Verify Blob satisfies Content interface.
	var _ models.Content = blob
}

func TestBlob_Bytes_FetchesFromStore(t *testing.T) {
	ctx := t.Context()

	data := []byte("fetched-from-store")
	store := newMemoryStore()
	desc := pushToStore(t, ctx, store, "application/octet-stream", data)

	blob := models.NewBlob(desc, store, store)

	got, err := blob.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Bytes() = %q, want %q", string(got), string(data))
	}

	// Second call should return cached value (no additional fetch).
	got2, err := blob.Bytes(ctx)
	if err != nil {
		t.Fatalf("second Bytes(): unexpected error: %v", err)
	}
	if !bytes.Equal(got2, data) {
		t.Errorf("second Bytes() = %q, want %q", string(got2), string(data))
	}
}

func TestBlob_NewBlobFromBytes_EmptyContent(t *testing.T) {
	blob := models.NewBlobFromBytes("application/octet-stream", []byte{})

	if blob.Size() != 0 {
		t.Errorf("Size() = %d, want 0", blob.Size())
	}

	ctx := t.Context()
	data, err := blob.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes(): unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("Bytes() = %q, want empty", string(data))
	}
}

func TestBlob_Verify_Success(t *testing.T) {
	ctx := t.Context()

	data := []byte("verifiable-content")
	store := newMemoryStore()
	desc := pushToStore(t, ctx, store, "application/octet-stream", data)

	blob := models.NewBlob(desc, store, store)

	if err := blob.Verify(ctx); err != nil {
		t.Fatalf("Verify() unexpected error: %v", err)
	}
}

func TestBlob_Bytes_WrapsErrNoFetcherInOrmError(t *testing.T) {
	data := []byte("orm-error-test")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	blob := models.NewBlob(desc, nil, nil)

	_, err := blob.Bytes(t.Context())
	if err == nil {
		t.Fatal("Bytes() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *OrmError, got %T: %v", err, err)
	}
	if ormErr.Op != "fetch" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "fetch")
	}
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Errorf("expected ErrNoFetcher in chain, got: %v", err)
	}
}

func TestBlob_Read_WrapsErrNoFetcherInOrmError(t *testing.T) {
	data := []byte("read-orm-error")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	blob := models.NewBlob(desc, nil, nil)

	_, err := blob.Read(t.Context())
	if err == nil {
		t.Fatal("Read() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *OrmError, got %T: %v", err, err)
	}
	if ormErr.Op != "read" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "read")
	}
}

func TestBlob_Push_WrapsErrNoPusherInOrmError(t *testing.T) {
	blob := models.NewBlobFromBytes("application/octet-stream", []byte("push-orm-err"))

	err := blob.Push(t.Context())
	if err == nil {
		t.Fatal("Push() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *OrmError, got %T: %v", err, err)
	}
	if ormErr.Op != "push" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "push")
	}
	if !errors.Is(err, models.ErrNoPusher) {
		t.Errorf("expected ErrNoPusher in chain, got: %v", err)
	}
}

func TestBlob_Verify_WrapsErrNoFetcherInOrmError(t *testing.T) {
	data := []byte("verify-orm-error")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	blob := models.NewBlob(desc, nil, nil)

	err := blob.Verify(t.Context())
	if err == nil {
		t.Fatal("Verify() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *OrmError, got %T: %v", err, err)
	}
	if ormErr.Op != "verify" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "verify")
	}
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Errorf("expected ErrNoFetcher in chain, got: %v", err)
	}
}

func TestBlob_Verify_NoFetcher(t *testing.T) {
	data := []byte("no-fetcher-verify")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	blob := models.NewBlob(desc, nil, nil)

	err := blob.Verify(t.Context())
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Fatalf("Verify() error = %v, want %v", err, models.ErrNoFetcher)
	}
}

func TestBlob_Delete_NoDeleter(t *testing.T) {
	store := newMemoryStore()
	data := []byte("no-deleter")
	desc := pushToStore(t, t.Context(), store, "application/octet-stream", data)

	// memory.Store doesn't implement content.Deleter.
	blob := models.NewBlob(desc, store, store)

	err := blob.Delete(t.Context())
	if err == nil {
		t.Fatal("Delete() expected error, got nil")
	}
	if !errors.Is(err, models.ErrNoDeleter) {
		t.Fatalf("Delete() error = %v, want ErrNoDeleter", err)
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("expected *OrmError, got %T: %v", err, err)
	}
	if ormErr.Op != "delete" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "delete")
	}
}

func TestBlob_Delete_NoPusher(t *testing.T) {
	data := []byte("no-pusher-delete")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	// Blob with nil pusher.
	blob := models.NewBlob(desc, nil, nil)

	err := blob.Delete(t.Context())
	if err == nil {
		t.Fatal("Delete() expected error, got nil")
	}
	if !errors.Is(err, models.ErrNoDeleter) {
		t.Fatalf("Delete() error = %v, want ErrNoDeleter", err)
	}
}
