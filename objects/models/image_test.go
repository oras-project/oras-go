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

// buildImageManifest creates a serialized image manifest with the given parameters.
func buildImageManifest(t *testing.T, config ocispec.Descriptor, layers []ocispec.Descriptor, subject *ocispec.Descriptor, annotations map[string]string) []byte {
	t.Helper()
	m := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    config,
		Layers:    layers,
		Subject:   subject,
	}
	if annotations != nil {
		m.Annotations = annotations
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal image manifest: %v", err)
	}
	return data
}

func TestImage_Descriptor(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	got := image.Descriptor()
	if got.Digest != desc.Digest {
		t.Errorf("Descriptor().Digest = %v, want %v", got.Digest, desc.Digest)
	}
	if got.Size != desc.Size {
		t.Errorf("Descriptor().Size = %d, want %d", got.Size, desc.Size)
	}
	if got.MediaType != ocispec.MediaTypeImageManifest {
		t.Errorf("Descriptor().MediaType = %q, want %q", got.MediaType, ocispec.MediaTypeImageManifest)
	}
}

func TestImage_DigestMediaTypeSizeAnnotations(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
		Annotations: map[string]string{
			"org.test": "hello",
		},
	}

	image := models.NewImage(desc, nil, nil, nil)

	if image.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", image.Digest(), desc.Digest)
	}
	if image.MediaType() != ocispec.MediaTypeImageManifest {
		t.Errorf("MediaType() = %q, want %q", image.MediaType(), ocispec.MediaTypeImageManifest)
	}
	if image.Size() != 3 {
		t.Errorf("Size() = %d, want 3", image.Size())
	}
	ann := image.Annotations()
	if ann["org.test"] != "hello" {
		t.Errorf("Annotations()[org.test] = %q, want %q", ann["org.test"], "hello")
	}

	// Verify annotations are a copy.
	ann["org.test"] = "modified"
	if image.Annotations()["org.test"] != "hello" {
		t.Error("Annotations() returned map that is not a copy")
	}
}

func TestImage_Load_Success(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestImage_Load_NoFetcher(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
	}

	image := models.NewImage(desc, nil, nil, nil)

	err := image.Load(t.Context())
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

func TestImage_Config(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte(`{"architecture":"amd64"}`)
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	config, err := image.Config(ctx)
	if err != nil {
		t.Fatalf("Config() unexpected error: %v", err)
	}
	if config.Digest() != configDesc.Digest {
		t.Errorf("Config().Digest() = %v, want %v", config.Digest(), configDesc.Digest)
	}
	if config.MediaType() != ocispec.MediaTypeImageConfig {
		t.Errorf("Config().MediaType() = %q, want %q", config.MediaType(), ocispec.MediaTypeImageConfig)
	}

	// Second call should return cached config.
	config2, err := image.Config(ctx)
	if err != nil {
		t.Fatalf("Config() second call unexpected error: %v", err)
	}
	if config2.Digest() != config.Digest() {
		t.Errorf("Config() second call returned different digest")
	}
}

func TestImage_Layers(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	layer1Data := []byte("layer-1")
	layer1Desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageLayer, layer1Data)

	layer2Data := []byte("layer-2")
	layer2Desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageLayerGzip, layer2Data)

	manifestBytes := buildImageManifest(t, configDesc, []ocispec.Descriptor{layer1Desc, layer2Desc}, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("Layers() returned %d layers, want 2", len(layers))
	}
	if layers[0].Digest() != layer1Desc.Digest {
		t.Errorf("Layers()[0].Digest() = %v, want %v", layers[0].Digest(), layer1Desc.Digest)
	}
	if layers[1].Digest() != layer2Desc.Digest {
		t.Errorf("Layers()[1].Digest() = %v, want %v", layers[1].Digest(), layer2Desc.Digest)
	}
}

func TestImage_Layers_Empty(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 0 {
		t.Errorf("Layers() returned %d layers, want 0", len(layers))
	}
}

func TestImage_Platform(t *testing.T) {
	plat := &ocispec.Platform{
		Architecture: "arm64",
		OS:           "linux",
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
		Platform:  plat,
	}

	image := models.NewImage(desc, nil, nil, nil)

	got, err := image.Platform(t.Context())
	if err != nil {
		t.Fatalf("Platform() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("Platform() = nil, want non-nil")
	}
	if got.Architecture != "arm64" {
		t.Errorf("Platform().Architecture = %q, want %q", got.Architecture, "arm64")
	}
	if got.OS != "linux" {
		t.Errorf("Platform().OS = %q, want %q", got.OS, "linux")
	}
}

func TestImage_Platform_NoPlatform(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
	}

	image := models.NewImage(desc, nil, nil, nil)

	got, err := image.Platform(t.Context())
	if err != nil {
		t.Fatalf("Platform() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Platform() = %v, want nil", got)
	}
}

func TestImage_Subject_NoSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	subject, err := image.Subject(ctx)
	if err != nil {
		t.Fatalf("Subject() unexpected error: %v", err)
	}
	if subject != nil {
		t.Errorf("Subject() = %v, want nil", subject)
	}
}

func TestImage_Subject_WithSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	// Create a subject image first (uses distinct config content).
	subjectConfigData := []byte(`{"subject":true}`)
	subjectConfigDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, subjectConfigData)
	subjectManifestBytes := buildImageManifest(t, subjectConfigDesc, nil, nil, nil)
	subjectDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, subjectManifestBytes)

	// Create the image with a subject reference.
	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	client := &mockManifestClient{
		fetchManifest: func(ctx context.Context, d ocispec.Descriptor) (models.Manifest, error) {
			return models.NewImage(d, store, store, nil), nil
		},
	}

	image := models.NewImage(desc, store, store, client)

	subject, err := image.Subject(ctx)
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

func TestImage_Subject_NoClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("subject")),
		Size:      7,
	}

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, &subjectDesc, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	_, err := image.Subject(ctx)
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

func TestImage_SetSubject(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("s")),
		Size:      1,
	}
	subjectImage := models.NewImage(subjectDesc, nil, nil, nil)

	image.SetSubject(subjectImage)

	subject, err := image.Subject(ctx)
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

func TestImage_MarshalJSON_NotLoaded(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
	}
	image := models.NewImage(desc, nil, nil, nil)

	_, err := image.MarshalJSON()
	if !errors.Is(err, models.ErrNotLoaded) {
		t.Errorf("MarshalJSON() error = %v, want ErrNotLoaded", err)
	}
}

func TestImage_MarshalJSON_Loaded(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, map[string]string{"env": "test"})
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	image := models.NewImage(desc, store, store, nil)

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := image.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if m.Config.Digest != configDesc.Digest {
		t.Errorf("Config.Digest = %v, want %v", m.Config.Digest, configDesc.Digest)
	}
	if m.MediaType != ocispec.MediaTypeImageManifest {
		t.Errorf("MediaType = %q, want %q", m.MediaType, ocispec.MediaTypeImageManifest)
	}
	if m.Annotations["env"] != "test" {
		t.Errorf("Annotations[env] = %q, want %q", m.Annotations["env"], "test")
	}
}

func TestImage_Push_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	var pushed bool
	client := &mockManifestClient{
		pushManifest: func(ctx context.Context, m models.Manifest, ref string) error {
			pushed = true
			if ref != "latest" {
				t.Errorf("Push() ref = %q, want %q", ref, "latest")
			}
			return nil
		},
	}

	image := models.NewImage(desc, store, store, client)

	if err := image.Push(ctx, "latest"); err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if !pushed {
		t.Error("Push() did not delegate to client.PushManifest")
	}
}

func TestImage_Push_WithoutClient_WithPusher(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	targetStore := newMemoryStore()
	image := models.NewImage(desc, store, targetStore, nil)

	if err := image.Push(ctx, ""); err != nil {
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

func TestImage_Push_NoPusherNoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
	}
	image := models.NewImage(desc, nil, nil, nil)

	err := image.Push(t.Context(), "tag")
	if !errors.Is(err, models.ErrNoPusher) {
		t.Errorf("Push() error = %v, want ErrNoPusher", err)
	}
}

func TestImage_Predecessors_WithClient(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte("{}")
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)
	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageManifest, manifestBytes)

	predDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("pred")),
		Size:      4,
	}
	predImage := models.NewImage(predDesc, nil, nil, nil)

	client := &mockManifestClient{
		findPreds: func(ctx context.Context, c models.Content) ([]models.Manifest, error) {
			return []models.Manifest{predImage}, nil
		},
	}

	image := models.NewImage(desc, store, store, client)

	preds, err := image.Predecessors(ctx)
	if err != nil {
		t.Fatalf("Predecessors() unexpected error: %v", err)
	}
	if len(preds) != 1 {
		t.Fatalf("Predecessors() returned %d, want 1", len(preds))
	}
}

func TestImage_Predecessors_NoClient(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes([]byte("img")),
		Size:      3,
	}
	image := models.NewImage(desc, nil, nil, nil)

	_, err := image.Predecessors(t.Context())
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Predecessors() error = %v, want ErrNoClient", err)
	}
}

func TestImage_ImplementsManifest(t *testing.T) {
	var _ models.Manifest = (*models.Image)(nil)
}

func TestNewImageFromManifestBytes_Success(t *testing.T) {
	store := newMemoryStore()
	ctx := t.Context()

	configData := []byte(`{"architecture":"amd64"}`)
	configDesc := pushToStore(t, ctx, store, ocispec.MediaTypeImageConfig, configData)

	layer1Data := []byte("layer-1")
	layer1Desc := pushToStore(t, ctx, store, ocispec.MediaTypeImageLayer, layer1Data)

	manifestBytes := buildImageManifest(t, configDesc, []ocispec.Descriptor{layer1Desc}, nil, nil)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	image, err := models.NewImageFromManifestBytes(desc, store, store, nil, manifestBytes)
	if err != nil {
		t.Fatalf("NewImageFromManifestBytes() unexpected error: %v", err)
	}

	// Verify descriptor is set correctly.
	if image.Digest() != desc.Digest {
		t.Errorf("Digest() = %v, want %v", image.Digest(), desc.Digest)
	}
	if image.MediaType() != ocispec.MediaTypeImageManifest {
		t.Errorf("MediaType() = %q, want %q", image.MediaType(), ocispec.MediaTypeImageManifest)
	}
	if image.Size() != desc.Size {
		t.Errorf("Size() = %d, want %d", image.Size(), desc.Size)
	}

	// Verify manifest is pre-loaded: Load should succeed without a fetcher.
	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Verify the pre-loaded manifest has correct config.
	config, err := image.Config(ctx)
	if err != nil {
		t.Fatalf("Config() unexpected error: %v", err)
	}
	if config.Digest() != configDesc.Digest {
		t.Errorf("Config().Digest() = %v, want %v", config.Digest(), configDesc.Digest)
	}

	// Verify layers.
	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("Layers() returned %d layers, want 1", len(layers))
	}
	if layers[0].Digest() != layer1Desc.Digest {
		t.Errorf("Layers()[0].Digest() = %v, want %v", layers[0].Digest(), layer1Desc.Digest)
	}
}

func TestNewImageFromManifestBytes_NoFetcher(t *testing.T) {
	ctx := t.Context()

	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes([]byte("{}")),
		Size:      2,
	}

	manifestBytes := buildImageManifest(t, configDesc, nil, nil, nil)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// Create with nil fetcher — the manifest is pre-loaded so Load still works.
	image, err := models.NewImageFromManifestBytes(desc, nil, nil, nil, manifestBytes)
	if err != nil {
		t.Fatalf("NewImageFromManifestBytes() unexpected error: %v", err)
	}

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error (should use pre-loaded data): %v", err)
	}
}

func TestNewImageFromManifestBytes_CorruptJSON(t *testing.T) {
	corruptData := []byte("this is not valid JSON!!!")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(corruptData),
		Size:      int64(len(corruptData)),
	}

	_, err := models.NewImageFromManifestBytes(desc, nil, nil, nil, corruptData)
	if err == nil {
		t.Fatal("NewImageFromManifestBytes() expected error for corrupt JSON, got nil")
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

func TestImage_Load_CorruptJSON(t *testing.T) {
	ctx := t.Context()

	// Use corrupt data that is not valid JSON.
	corruptData := []byte("this is not valid JSON!!!")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(corruptData),
		Size:      int64(len(corruptData)),
	}

	// Use a byteFetcher that returns the corrupt data directly,
	// bypassing memory store's manifest validation on push.
	fetcher := &byteFetcher{data: corruptData}
	image := models.NewImage(desc, fetcher, nil, nil)

	err := image.Load(ctx)
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
