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
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/objects/models"
)

// buildIndexManifest creates a serialized OCI index with the given parameters.
func buildIndexManifest(t *testing.T, manifests []ocispec.Descriptor, subject *ocispec.Descriptor, annotations map[string]string) []byte {
	t.Helper()
	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
		Subject:   subject,
	}
	if annotations != nil {
		idx.Annotations = annotations
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}
	return data
}

func TestIndex_Descriptor(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	got := idx.Descriptor()
	if got.Digest != desc.Digest {
		t.Errorf("Descriptor().Digest = %v, want %v", got.Digest, desc.Digest)
	}
	if got.Size != desc.Size {
		t.Errorf("Descriptor().Size = %d, want %d", got.Size, desc.Size)
	}
	if got.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("Descriptor().MediaType = %q, want %q", got.MediaType, ocispec.MediaTypeImageIndex)
	}
}

func TestIndex_DigestMediaTypeSizeAnnotations(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("idx")),
		Size:      3,
		Annotations: map[string]string{
			"idx.key": "idx.val",
		},
	}

	idx := models.NewIndex(desc, nil, nil, nil)

	if idx.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", idx.Digest(), desc.Digest)
	}
	if idx.MediaType() != ocispec.MediaTypeImageIndex {
		t.Errorf("MediaType() = %q, want %q", idx.MediaType(), ocispec.MediaTypeImageIndex)
	}
	if idx.Size() != 3 {
		t.Errorf("Size() = %d, want 3", idx.Size())
	}
	ann := idx.Annotations()
	if ann["idx.key"] != "idx.val" {
		t.Errorf("Annotations()[idx.key] = %q, want %q", ann["idx.key"], "idx.val")
	}

	// Verify annotations are a copy.
	ann["idx.key"] = "modified"
	if idx.Annotations()["idx.key"] != "idx.val" {
		t.Error("Annotations() returned map that is not a copy")
	}
}

func TestIndex_Load_Success(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestIndex_Load_NoFetcher(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("idx")),
		Size:      3,
	}

	idx := models.NewIndex(desc, nil, nil, nil)

	err := idx.Load(t.Context())
	if err == nil {
		t.Fatal("Load() expected error, got nil")
	}

	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Load() error type = %T, want *models.ObjectsError", err)
	}
	if ormErr.Op != "load" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "load")
	}
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Errorf("Load() error should wrap ErrNoFetcher, got %v", err)
	}
}

func TestIndex_Manifests(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	child1Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child-1")),
		Size:      7,
		Platform: &ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		},
	}
	child2Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child-2")),
		Size:      7,
		Platform: &ocispec.Platform{
			Architecture: "arm64",
			OS:           "linux",
		},
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{child1Desc, child2Desc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	manifests, err := idx.Manifests(ctx)
	if err != nil {
		t.Fatalf("Manifests() unexpected error: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("Manifests() returned %d, want 2", len(manifests))
	}
	if manifests[0].Digest() != child1Desc.Digest {
		t.Errorf("Manifests()[0].Digest() = %v, want %v", manifests[0].Digest(), child1Desc.Digest)
	}
	if manifests[1].Digest() != child2Desc.Digest {
		t.Errorf("Manifests()[1].Digest() = %v, want %v", manifests[1].Digest(), child2Desc.Digest)
	}

	// Second call should return cached manifests.
	manifests2, err := idx.Manifests(ctx)
	if err != nil {
		t.Fatalf("Manifests() second call unexpected error: %v", err)
	}
	if len(manifests2) != len(manifests) {
		t.Errorf("Manifests() second call returned %d, want %d", len(manifests2), len(manifests))
	}
}

func TestIndex_Manifests_NoClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	// No client.
	idx := models.NewIndex(desc, store, store, nil)

	_, err := idx.Manifests(ctx)
	if err == nil {
		t.Fatal("Manifests() expected error, got nil")
	}

	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Manifests() error type = %T, want *models.ObjectsError", err)
	}
	if ormErr.Op != "fetch_manifests" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "fetch_manifests")
	}
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Manifests() error should wrap ErrNoClient, got %v", err)
	}
}

func TestIndex_FilterByPlatform(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	amd64Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("amd64")),
		Size:      5,
		Platform: &ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		},
	}
	arm64Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("arm64")),
		Size:      5,
		Platform: &ocispec.Platform{
			Architecture: "arm64",
			OS:           "linux",
		},
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{amd64Desc, arm64Desc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	// Filter for arm64/linux.
	target := &ocispec.Platform{
		Architecture: "arm64",
		OS:           "linux",
	}
	filtered, err := idx.FilterByPlatform(ctx, target)
	if err != nil {
		t.Fatalf("FilterByPlatform() unexpected error: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("FilterByPlatform() returned %d, want 1", len(filtered))
	}
	if filtered[0].Digest() != arm64Desc.Digest {
		t.Errorf("FilterByPlatform()[0].Digest() = %v, want %v", filtered[0].Digest(), arm64Desc.Digest)
	}
}

func TestIndex_FilterByPlatform_NilTarget(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
		Platform: &ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		},
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	// Nil target should return all manifests.
	filtered, err := idx.FilterByPlatform(ctx, nil)
	if err != nil {
		t.Fatalf("FilterByPlatform(nil) unexpected error: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("FilterByPlatform(nil) returned %d, want 1", len(filtered))
	}
}

func TestIndex_FilterByPlatform_NoMatch(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
		Platform: &ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		},
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	target := &ocispec.Platform{
		Architecture: "s390x",
		OS:           "linux",
	}
	filtered, err := idx.FilterByPlatform(ctx, target)
	if err != nil {
		t.Fatalf("FilterByPlatform() unexpected error: %v", err)
	}
	if len(filtered) != 0 {
		t.Errorf("FilterByPlatform() returned %d, want 0", len(filtered))
	}
}

func TestIndex_Subject_NoSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	subject, err := idx.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject() unexpected error: %v", err)
	}
	if subject != nil {
		t.Errorf("Subject() = %v, want nil", subject)
	}
}

func TestIndex_Subject_WithSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("subject")),
		Size:      7,
	}

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	subject, err := idx.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject() unexpected error: %v", err)
	}
	if subject == nil {
		t.Fatal("Subject() = nil, want non-nil")
	}
	if subject.Digest() != subjectDesc.Digest {
		t.Errorf("Subject().Digest() = %v, want %v", subject.Digest(), subjectDesc.Digest)
	}
}

func TestIndex_Subject_NoClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("subject")),
		Size:      7,
	}

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	_, err := idx.Subject(ctx)
	if err == nil {
		t.Fatal("Subject() expected error, got nil")
	}

	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Subject() error type = %T, want *models.ObjectsError", err)
	}
	if ormErr.Op != "fetch_subject" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "fetch_subject")
	}
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Subject() error should wrap ErrNoClient, got %v", err)
	}
}

func TestIndex_SetSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("s")),
		Size:      1,
	}
	subjectImage := models.NewImage(subjectDesc, nil, nil, nil)

	idx.SetSubject(subjectImage)

	subject, err := idx.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject() after SetSubject() unexpected error: %v", err)
	}
	if subject == nil {
		t.Fatal("Subject() after SetSubject() = nil, want non-nil")
	}
	if subject.Digest() != subjectDesc.Digest {
		t.Errorf("Subject().Digest() = %v, want %v", subject.Digest(), subjectDesc.Digest)
	}
}

func TestIndex_MarshalJSON_NotLoaded(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("idx")),
		Size:      3,
	}
	idx := models.NewIndex(desc, nil, nil, nil)

	_, err := idx.MarshalJSON()
	if !errors.Is(err, models.ErrNotLoaded) {
		t.Errorf("MarshalJSON() error = %v, want ErrNotLoaded", err)
	}
}

func TestIndex_MarshalJSON_Loaded(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, map[string]string{"ver": "1"})
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	idx := models.NewIndex(desc, store, store, nil)

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if len(m.Manifests) != 1 {
		t.Fatalf("Manifests length = %d, want 1", len(m.Manifests))
	}
	if m.Manifests[0].Digest != childDesc.Digest {
		t.Errorf("Manifests[0].Digest = %v, want %v", m.Manifests[0].Digest, childDesc.Digest)
	}
	if m.Annotations["ver"] != "1" {
		t.Errorf("Annotations[ver] = %q, want %q", m.Annotations["ver"], "1")
	}
}

func TestIndex_Push_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	var pushed bool
	client := &mockManifestClient{
		pushManifest: func(ctx context.Context, m models.Manifest, ref string) error {
			pushed = true
			if ref != "multi-arch" {
				t.Errorf("Push() ref = %q, want %q", ref, "multi-arch")
			}
			return nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	if err := idx.Push(ctx, "multi-arch"); err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if !pushed {
		t.Error("Push() did not delegate to client.PushManifest")
	}
}

func TestIndex_Push_WithoutClient_WithPusher(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	targetStore := newMemoryStore()
	idx := models.NewIndex(desc, store, targetStore, nil)

	if err := idx.Push(ctx, ""); err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	exists, err := targetStore.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Push() did not push content to target store")
	}
}

func TestIndex_Push_NoPusherNoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("idx")),
		Size:      3,
	}
	idx := models.NewIndex(desc, nil, nil, nil)

	err := idx.Push(t.Context(), "tag")
	if !errors.Is(err, models.ErrNoPusher) {
		t.Errorf("Push() error = %v, want ErrNoPusher", err)
	}
}

func TestIndex_Predecessors_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}
	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageIndex, indexBytes)

	predDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("pred")),
		Size:      4,
	}
	predIndex := models.NewIndex(predDesc, nil, nil, nil)

	client := &mockManifestClient{
		findPreds: func(ctx context.Context, c models.Content) ([]models.Manifest, error) {
			return []models.Manifest{predIndex}, nil
		},
	}

	idx := models.NewIndex(desc, store, store, client)

	preds, err := idx.Predecessors(ctx)
	if err != nil {
		t.Fatalf("Predecessors() unexpected error: %v", err)
	}
	if len(preds) != 1 {
		t.Fatalf("Predecessors() returned %d, want 1", len(preds))
	}
}

func TestIndex_Predecessors_NoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes([]byte("idx")),
		Size:      3,
	}
	idx := models.NewIndex(desc, nil, nil, nil)

	_, err := idx.Predecessors(t.Context())
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Predecessors() error = %v, want ErrNoClient", err)
	}
}

func TestIndex_ImplementsManifest(t *testing.T) {
	var _ models.Manifest = (*models.Index)(nil)
}

func TestNewIndexFromManifestBytes_Success(t *testing.T) {
	ctx := t.Context()

	child1Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child1")),
		Size:      6,
	}
	child2Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child2")),
		Size:      6,
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{child1Desc, child2Desc}, nil, nil)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(indexBytes),
		Size:      int64(len(indexBytes)),
	}

	idx, err := models.NewIndexFromManifestBytes(desc, nil, nil, nil, indexBytes)
	if err != nil {
		t.Fatalf("NewIndexFromManifestBytes() unexpected error: %v", err)
	}

	// Verify descriptor is set correctly.
	if idx.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", idx.Digest(), desc.Digest)
	}
	if idx.MediaType() != ocispec.MediaTypeImageIndex {
		t.Errorf("MediaType() = %q, want %q", idx.MediaType(), ocispec.MediaTypeImageIndex)
	}
	if idx.Size() != desc.Size {
		t.Errorf("Size() = %d, want %d", idx.Size(), desc.Size)
	}

	// Verify index is pre-loaded: Load should succeed without a fetcher.
	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Verify the pre-loaded data round-trips through MarshalJSON.
	marshaled, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}
	var roundTripped ocispec.Index
	if err := json.Unmarshal(marshaled, &roundTripped); err != nil {
		t.Fatalf("failed to unmarshal round-tripped index: %v", err)
	}
	if len(roundTripped.Manifests) != 2 {
		t.Fatalf("round-tripped index has %d manifests, want 2", len(roundTripped.Manifests))
	}
	if roundTripped.Manifests[0].Digest != child1Desc.Digest {
		t.Errorf("Manifests[0].Digest = %v, want %v", roundTripped.Manifests[0].Digest, child1Desc.Digest)
	}
	if roundTripped.Manifests[1].Digest != child2Desc.Digest {
		t.Errorf("Manifests[1].Digest = %v, want %v", roundTripped.Manifests[1].Digest, child2Desc.Digest)
	}
}

func TestNewIndexFromManifestBytes_NoFetcher(t *testing.T) {
	ctx := t.Context()

	childDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("child")),
		Size:      5,
	}

	indexBytes := buildIndexManifest(t, []ocispec.Descriptor{childDesc}, nil, nil)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(indexBytes),
		Size:      int64(len(indexBytes)),
	}

	// Create with nil fetcher — the index is pre-loaded so Load still works.
	idx, err := models.NewIndexFromManifestBytes(desc, nil, nil, nil, indexBytes)
	if err != nil {
		t.Fatalf("NewIndexFromManifestBytes() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error (should use pre-loaded data): %v", err)
	}
}

func TestNewIndexFromManifestBytes_CorruptJSON(t *testing.T) {
	corruptData := []byte("this is not valid JSON!!!")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(corruptData),
		Size:      int64(len(corruptData)),
	}

	_, err := models.NewIndexFromManifestBytes(desc, nil, nil, nil, corruptData)
	if err == nil {
		t.Fatal("NewIndexFromManifestBytes() expected error for corrupt JSON, got nil")
	}

	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("error type = %T, want *models.ObjectsError", err)
	}
	if ormErr.Op != "load" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "load")
	}

	var syntaxErr *json.SyntaxError
	if !errors.As(ormErr.Err, &syntaxErr) {
		t.Errorf("ObjectsError.Err type = %T, want *json.SyntaxError", ormErr.Err)
	}
}

func TestIndex_Load_CorruptJSON(t *testing.T) {
	ctx := t.Context()

	// Use corrupt data that is not valid JSON.
	corruptData := []byte("this is not valid JSON!!!")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(corruptData),
		Size:      int64(len(corruptData)),
	}

	// Use a byteFetcher that returns the corrupt data directly,
	// bypassing memory store's manifest validation on push.
	fetcher := &byteFetcher{data: corruptData}
	idx := models.NewIndex(desc, fetcher, nil, nil)

	err := idx.Load(ctx)
	if err == nil {
		t.Fatal("Load() expected error for corrupt JSON, got nil")
	}

	// Verify it returns an ObjectsError wrapping a JSON parse error.
	var ormErr *models.ObjectsError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Load() error type = %T, want *models.ObjectsError", err)
	}
	if ormErr.Op != "load" {
		t.Errorf("ObjectsError.Op = %q, want %q", ormErr.Op, "load")
	}

	var syntaxErr *json.SyntaxError
	if !errors.As(ormErr.Err, &syntaxErr) {
		t.Errorf("ObjectsError.Err type = %T, want *json.SyntaxError", ormErr.Err)
	}
}
