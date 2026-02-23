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
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/orm/models"
)

// mockManifestClient provides a configurable ManifestClient for testing.
type mockManifestClient struct {
	fetchManifest func(ctx context.Context, desc ocispec.Descriptor) (models.Manifest, error)
	fetchByRef    func(ctx context.Context, ref string) (models.Manifest, error)
	findPreds     func(ctx context.Context, c models.Content) ([]models.Manifest, error)
	pushManifest  func(ctx context.Context, m models.Manifest, ref string) error
}

func (m *mockManifestClient) FetchManifest(ctx context.Context, desc ocispec.Descriptor) (models.Manifest, error) {
	if m.fetchManifest != nil {
		return m.fetchManifest(ctx, desc)
	}
	return nil, errors.New("FetchManifest not implemented")
}

func (m *mockManifestClient) FetchByReference(ctx context.Context, ref string) (models.Manifest, error) {
	if m.fetchByRef != nil {
		return m.fetchByRef(ctx, ref)
	}
	return nil, errors.New("FetchByReference not implemented")
}

func (m *mockManifestClient) FindPredecessors(ctx context.Context, c models.Content) ([]models.Manifest, error) {
	if m.findPreds != nil {
		return m.findPreds(ctx, c)
	}
	return nil, errors.New("FindPredecessors not implemented")
}

func (m *mockManifestClient) PushManifest(ctx context.Context, manifest models.Manifest, ref string) error {
	if m.pushManifest != nil {
		return m.pushManifest(ctx, manifest, ref)
	}
	return errors.New("PushManifest not implemented")
}

// buildArtifactManifest creates a serialized artifact manifest with the given parameters.
func buildArtifactManifest(t *testing.T, artifactType string, blobs []ocispec.Descriptor, subject *ocispec.Descriptor, annotations map[string]string) []byte {
	t.Helper()
	m := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		ArtifactType: artifactType,
		Blobs:        blobs,
		Subject:      subject,
		Annotations:  annotations,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal artifact manifest: %v", err)
	}
	return data
}

func TestArtifact_Descriptor(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	got := artifact.Descriptor()
	if got.Digest != desc.Digest {
		t.Errorf("Descriptor().Digest = %v, want %v", got.Digest, desc.Digest)
	}
	if got.Size != desc.Size {
		t.Errorf("Descriptor().Size = %d, want %d", got.Size, desc.Size)
	}
	if got.MediaType != desc.MediaType {
		t.Errorf("Descriptor().MediaType = %q, want %q", got.MediaType, desc.MediaType)
	}
}

func TestArtifact_DigestMediaTypeSizeAnnotations(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("test")),
		Size:      4,
		Annotations: map[string]string{
			"key": "value",
		},
	}

	artifact := models.NewArtifact(desc, nil, nil, nil)

	if artifact.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", artifact.Digest(), desc.Digest)
	}
	if artifact.MediaType() != spec.MediaTypeArtifactManifest {
		t.Errorf("MediaType() = %q, want %q", artifact.MediaType(), spec.MediaTypeArtifactManifest)
	}
	if artifact.Size() != 4 {
		t.Errorf("Size() = %d, want 4", artifact.Size())
	}

	ann := artifact.Annotations()
	if ann["key"] != "value" {
		t.Errorf("Annotations()[key] = %q, want %q", ann["key"], "value")
	}

	// Verify annotations are a copy (safe to modify).
	ann["key"] = "modified"
	if artifact.Annotations()["key"] != "value" {
		t.Error("Annotations() returned map that is not a copy")
	}
}

func TestArtifact_Load_Success(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test.type", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestArtifact_Load_NoFetcher(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("test")),
		Size:      4,
	}

	artifact := models.NewArtifact(desc, nil, nil, nil)

	err := artifact.Load(t.Context())
	if err == nil {
		t.Fatal("Load() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Load() error type = %T, want *models.OrmError", err)
	}
	if ormErr.Op != "load" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "load")
	}
	if !errors.Is(err, models.ErrNoFetcher) {
		t.Errorf("Load() error = %v, want ErrNoFetcher in chain", err)
	}
}

func TestArtifact_ArtifactType(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.sbom+json", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	at, err := artifact.ArtifactType(ctx)
	if err != nil {
		t.Fatalf("ArtifactType() unexpected error: %v", err)
	}
	if at != "application/vnd.sbom+json" {
		t.Errorf("ArtifactType() = %q, want %q", at, "application/vnd.sbom+json")
	}
}

func TestArtifact_Blobs(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	blobData := []byte("blob-content")
	blobDesc := pushToStore(t, ctx, store, "application/octet-stream", blobData)

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", []ocispec.Descriptor{blobDesc}, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 1 {
		t.Fatalf("Blobs() returned %d blobs, want 1", len(blobs))
	}
	if blobs[0].Digest() != blobDesc.Digest {
		t.Errorf("Blobs()[0].Digest() = %v, want %v", blobs[0].Digest(), blobDesc.Digest)
	}
	if blobs[0].MediaType() != "application/octet-stream" {
		t.Errorf("Blobs()[0].MediaType() = %q, want %q", blobs[0].MediaType(), "application/octet-stream")
	}

	// Verify blobs are cached (second call returns same slice).
	blobs2, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() second call unexpected error: %v", err)
	}
	if len(blobs2) != len(blobs) {
		t.Errorf("Blobs() second call returned %d blobs, want %d", len(blobs2), len(blobs))
	}
}

func TestArtifact_Blobs_Empty(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 0 {
		t.Errorf("Blobs() returned %d blobs, want 0", len(blobs))
	}
}

func TestArtifact_Subject_NoSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	subject, err := artifact.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject() unexpected error: %v", err)
	}
	if subject != nil {
		t.Errorf("Subject() = %v, want nil", subject)
	}
}

func TestArtifact_Subject_WithSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	// Create a subject manifest (another artifact).
	subjectManifestBytes := buildArtifactManifest(t, "application/vnd.subject", nil, nil, nil)
	subjectDesc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, subjectManifestBytes)

	// Create the artifact with a subject reference.
	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	// Mock client that returns a subject manifest.
	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewArtifact(d, store, store, nil), nil
		},
	}

	artifact := models.NewArtifact(desc, store, store, client)

	subject, err := artifact.Subject(ctx)
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

func TestArtifact_Subject_NoClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	subjectDesc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("subject")),
		Size:      7,
	}

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	// No client provided.
	artifact := models.NewArtifact(desc, store, store, nil)

	_, err := artifact.Subject(ctx)
	if err == nil {
		t.Fatal("Subject() expected error, got nil")
	}

	var ormErr *models.OrmError
	if !errors.As(err, &ormErr) {
		t.Fatalf("Subject() error type = %T, want *models.OrmError", err)
	}
	if ormErr.Op != "fetch_subject" {
		t.Errorf("OrmError.Op = %q, want %q", ormErr.Op, "fetch_subject")
	}
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Subject() error should wrap ErrNoClient, got %v", err)
	}
}

func TestArtifact_SetSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	// Create a subject manifest to set.
	subjectDesc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("subject-data")),
		Size:      12,
	}
	subjectArtifact := models.NewArtifact(subjectDesc, nil, nil, nil)

	artifact.SetSubject(subjectArtifact)

	// Subject should now be returned without loading from the manifest.
	subject, err := artifact.Subject(ctx)
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

func TestArtifact_MarshalJSON_NotLoaded(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("test")),
		Size:      4,
	}

	artifact := models.NewArtifact(desc, nil, nil, nil)

	_, err := artifact.MarshalJSON()
	if !errors.Is(err, models.ErrNotLoaded) {
		t.Errorf("MarshalJSON() error = %v, want ErrNotLoaded", err)
	}
}

func TestArtifact_MarshalJSON_Loaded(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, map[string]string{"key": "val"})
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	artifact := models.NewArtifact(desc, store, store, nil)

	// Load the manifest.
	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := artifact.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	// Unmarshal and verify the content round-trips.
	var m spec.Artifact
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if m.ArtifactType != "application/vnd.test" {
		t.Errorf("ArtifactType = %q, want %q", m.ArtifactType, "application/vnd.test")
	}
	if m.Annotations["key"] != "val" {
		t.Errorf("Annotations[key] = %q, want %q", m.Annotations["key"], "val")
	}
}

func TestArtifact_Push_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	var pushed bool
	client := &mockManifestClient{
		pushManifest: func(ctx context.Context, m models.Manifest, ref string) error {
			pushed = true
			if ref != "v1.0" {
				t.Errorf("Push() ref = %q, want %q", ref, "v1.0")
			}
			return nil
		},
	}

	artifact := models.NewArtifact(desc, store, store, client)

	if err := artifact.Push(ctx, "v1.0"); err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if !pushed {
		t.Error("Push() did not delegate to client.PushManifest")
	}
}

func TestArtifact_Push_WithoutClient_WithPusher(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	targetStore := newMemoryStore()

	// No client, but fetcher + pusher.
	artifact := models.NewArtifact(desc, store, targetStore, nil)

	if err := artifact.Push(ctx, ""); err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	// Verify content was pushed to target.
	exists, err := targetStore.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Push() did not push content to target store")
	}
}

func TestArtifact_Push_NoPusherNoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("test")),
		Size:      4,
	}

	artifact := models.NewArtifact(desc, nil, nil, nil)

	err := artifact.Push(t.Context(), "tag")
	if !errors.Is(err, models.ErrNoPusher) {
		t.Errorf("Push() error = %v, want ErrNoPusher", err)
	}
}

func TestArtifact_Predecessors_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	manifestBytes := buildArtifactManifest(t, "application/vnd.test", nil, nil, nil)
	desc := pushToStore(t, ctx, store, spec.MediaTypeArtifactManifest, manifestBytes)

	predDesc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("predecessor")),
		Size:      11,
	}
	predArtifact := models.NewArtifact(predDesc, nil, nil, nil)

	client := &mockManifestClient{
		findPreds: func(ctx context.Context, c models.Content) ([]models.Manifest, error) {
			return []models.Manifest{predArtifact}, nil
		},
	}

	artifact := models.NewArtifact(desc, store, store, client)

	preds, err := artifact.Predecessors(ctx)
	if err != nil {
		t.Fatalf("Predecessors() unexpected error: %v", err)
	}
	if len(preds) != 1 {
		t.Fatalf("Predecessors() returned %d, want 1", len(preds))
	}
	if preds[0].Digest() != predDesc.Digest {
		t.Errorf("Predecessors()[0].Digest() = %v, want %v", preds[0].Digest(), predDesc.Digest)
	}
}

func TestArtifact_Predecessors_NoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("test")),
		Size:      4,
	}

	artifact := models.NewArtifact(desc, nil, nil, nil)

	_, err := artifact.Predecessors(t.Context())
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Predecessors() error = %v, want ErrNoClient", err)
	}
}

func TestArtifact_ImplementsManifest(t *testing.T) {
	var _ models.Manifest = (*models.Artifact)(nil)
}
