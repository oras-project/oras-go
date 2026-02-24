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

package builders_test

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/orm"
	"github.com/oras-project/oras-go/v3/orm/models"
)

func TestImageBuilder_Build_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer-data"))

	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	if image == nil {
		t.Fatal("Build() returned nil image")
	}
	if image.MediaType() != ocispec.MediaTypeImageManifest {
		t.Errorf("MediaType() = %q, want %q", image.MediaType(), ocispec.MediaTypeImageManifest)
	}

	// Verify image can be loaded.
	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Verify config.
	gotConfig, err := image.Config(ctx)
	if err != nil {
		t.Fatalf("Config() unexpected error: %v", err)
	}
	if gotConfig.Digest() != config.Digest() {
		t.Errorf("Config().Digest() = %v, want %v", gotConfig.Digest(), config.Digest())
	}

	// Verify layers.
	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("Layers() returned %d, want 1", len(layers))
	}
	if layers[0].Digest() != layer.Digest() {
		t.Errorf("Layers()[0].Digest() = %v, want %v", layers[0].Digest(), layer.Digest())
	}
}

func TestImageBuilder_Build_NilLayer(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	_, err := client.BuildImage().
		WithConfig(config).
		AddLayer(nil).
		Build(ctx)
	if err == nil {
		t.Fatal("Build() expected error for nil layer, got nil")
	}
	if err.Error() != "layer at index 0 is nil" {
		t.Errorf("Build() error = %q, want %q", err.Error(), "layer at index 0 is nil")
	}
}

func TestImageBuilder_Build_NoConfig(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	_, err := client.BuildImage().Build(ctx)
	if err == nil {
		t.Fatal("Build() expected error for missing config, got nil")
	}
}

func TestImageBuilder_Build_NoLayers(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	image, err := client.BuildImage().
		WithConfig(config).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 0 {
		t.Errorf("Layers() returned %d, want 0", len(layers))
	}
}

func TestImageBuilder_WithLayers(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer1 := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("l1"))
	layer2 := client.NewBlob(ocispec.MediaTypeImageLayerGzip, []byte("l2"))

	image, err := client.BuildImage().
		WithConfig(config).
		WithLayers([]*models.Blob{layer1, layer2}).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("Layers() returned %d, want 2", len(layers))
	}
}

func TestImageBuilder_WithPlatform(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	platform := &ocispec.Platform{
		Architecture: "amd64",
		OS:           "linux",
	}

	image, err := client.BuildImage().
		WithConfig(config).
		WithPlatform(platform).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// Platform should be set on the descriptor.
	desc := image.Descriptor()
	if desc.Platform == nil {
		t.Fatal("Descriptor().Platform = nil, want non-nil")
	}
	if desc.Platform.Architecture != "amd64" {
		t.Errorf("Platform.Architecture = %q, want %q", desc.Platform.Architecture, "amd64")
	}
	if desc.Platform.OS != "linux" {
		t.Errorf("Platform.OS = %q, want %q", desc.Platform.OS, "linux")
	}

	// Platform should also be accessible via Image.Platform().
	gotPlatform, err := image.Platform(ctx)
	if err != nil {
		t.Fatalf("Platform() unexpected error: %v", err)
	}
	if gotPlatform == nil {
		t.Fatal("Platform() = nil, want non-nil")
	}
	if gotPlatform.Architecture != "amd64" {
		t.Errorf("Platform().Architecture = %q, want %q", gotPlatform.Architecture, "amd64")
	}
}

func TestImageBuilder_WithSubject(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	// Build a subject image.
	subjectConfig := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	subject, err := client.BuildImage().
		WithConfig(subjectConfig).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build subject: %v", err)
	}

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{"os":"linux"}`))
	image, err := client.BuildImage().
		WithConfig(config).
		WithSubject(subject).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := image.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Manifest
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Subject == nil {
		t.Fatal("Subject is nil in serialized manifest")
	}
	if m.Subject.Digest != subject.Digest() {
		t.Errorf("Subject.Digest = %v, want %v", m.Subject.Digest, subject.Digest())
	}
}

func TestImageBuilder_WithAnnotation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	image, err := client.BuildImage().
		WithConfig(config).
		WithAnnotation("org.test.created", "2024-01-01").
		WithAnnotation("org.test.author", "test-user").
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := image.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Manifest
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["org.test.created"] != "2024-01-01" {
		t.Errorf("Annotations[org.test.created] = %q, want %q", m.Annotations["org.test.created"], "2024-01-01")
	}
	if m.Annotations["org.test.author"] != "test-user" {
		t.Errorf("Annotations[org.test.author] = %q, want %q", m.Annotations["org.test.author"], "test-user")
	}
}

func TestImageBuilder_WithAnnotations(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	annotations := map[string]string{
		"a": "1",
		"b": "2",
	}

	image, err := client.BuildImage().
		WithConfig(config).
		WithAnnotations(annotations).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := image.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Manifest
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["a"] != "1" {
		t.Errorf("Annotations[a] = %q, want %q", m.Annotations["a"], "1")
	}
	if m.Annotations["b"] != "2" {
		t.Errorf("Annotations[b] = %q, want %q", m.Annotations["b"], "2")
	}
}

func TestImageBuilder_BuildAndPush(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer"))

	// Build creates the manifest and pushes it to the store.
	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// Verify the manifest was pushed to storage during Build().
	exists, err := store.Exists(ctx, image.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push manifest to storage")
	}

	// Tag manually since memory store rejects duplicate pushes.
	if err := store.Tag(ctx, image.Descriptor(), "latest"); err != nil {
		t.Fatalf("Tag() unexpected error: %v", err)
	}

	desc, err := store.Resolve(ctx, "latest")
	if err != nil {
		t.Fatalf("Resolve(latest) unexpected error: %v", err)
	}
	if desc.Digest != image.Digest() {
		t.Errorf("Resolve(latest) digest = %v, want %v", desc.Digest, image.Digest())
	}
}

func TestImageBuilder_Build_ManifestInStorage(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	image, err := client.BuildImage().
		WithConfig(config).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	exists, err := store.Exists(ctx, image.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push manifest to storage")
	}
}

func TestImageBuilder_Build_SchemaVersion(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	image, err := client.BuildImage().
		WithConfig(config).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := image.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := image.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Manifest
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", m.SchemaVersion)
	}
	if m.MediaType != ocispec.MediaTypeImageManifest {
		t.Errorf("MediaType = %q, want %q", m.MediaType, ocispec.MediaTypeImageManifest)
	}
}

func TestImageBuilder_MultipleLayers(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	l1 := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("base"))
	l2 := client.NewBlob(ocispec.MediaTypeImageLayerGzip, []byte("overlay"))
	l3 := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("top"))

	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(l1).
		AddLayer(l2).
		AddLayer(l3).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("Layers() returned %d, want 3", len(layers))
	}

	expectedDigests := []string{l1.Digest().String(), l2.Digest().String(), l3.Digest().String()}
	for i, layer := range layers {
		if layer.Digest().String() != expectedDigests[i] {
			t.Errorf("Layers()[%d].Digest() = %v, want %v", i, layer.Digest(), expectedDigests[i])
		}
	}
}

func TestImageBuilder_WithLayers_SliceIsolation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer1 := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("l1"))
	layer2 := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("l2"))

	input := []*models.Blob{layer1}
	builder := client.BuildImage().WithConfig(config).WithLayers(input)

	// Mutate the original slice after calling WithLayers.
	input[0] = layer2

	image, err := builder.Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	layers, err := image.Layers(ctx)
	if err != nil {
		t.Fatalf("Layers() unexpected error: %v", err)
	}
	// The builder should still have layer1, not layer2.
	if len(layers) != 1 {
		t.Fatalf("Layers() returned %d, want 1", len(layers))
	}
	if layers[0].Digest() != layer1.Digest() {
		t.Errorf("Layers()[0].Digest() = %v, want %v (layer1)", layers[0].Digest(), layer1.Digest())
	}
}
