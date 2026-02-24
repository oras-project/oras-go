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

func TestIndexBuilder_Build_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	// Build a child image.
	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer"))

	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		WithPlatform(&ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		}).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	// Build the index.
	idx, err := client.BuildIndex().
		AddManifest(image).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	if idx == nil {
		t.Fatal("Build() returned nil index")
	}
	if idx.MediaType() != ocispec.MediaTypeImageIndex {
		t.Errorf("MediaType() = %q, want %q", idx.MediaType(), ocispec.MediaTypeImageIndex)
	}

	// Verify index can be loaded.
	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestIndexBuilder_Build_NilManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	_, err = client.BuildIndex().
		AddManifest(image).
		AddManifest(nil).
		Build(ctx)
	if err == nil {
		t.Fatal("Build() expected error for nil manifest, got nil")
	}
	if err.Error() != "manifest at index 1 is nil" {
		t.Errorf("Build() error = %q, want %q", err.Error(), "manifest at index 1 is nil")
	}
}

func TestIndexBuilder_Build_NoManifests(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	_, err := client.BuildIndex().Build(ctx)
	if err == nil {
		t.Fatal("Build() expected error for no manifests, got nil")
	}
}

func TestIndexBuilder_WithManifests(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config1 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image1, err := client.BuildImage().
		WithConfig(config1).
		WithPlatform(&ocispec.Platform{Architecture: "amd64", OS: "linux"}).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 1: %v", err)
	}

	config2 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{"os":"linux"}`))
	image2, err := client.BuildImage().
		WithConfig(config2).
		WithPlatform(&ocispec.Platform{Architecture: "arm64", OS: "linux"}).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 2: %v", err)
	}

	idx, err := client.BuildIndex().
		WithManifests([]models.Manifest{image1, image2}).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Manifests) != 2 {
		t.Fatalf("Manifests length = %d, want 2", len(m.Manifests))
	}
}

func TestIndexBuilder_AddManifest(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config1 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image1, err := client.BuildImage().WithConfig(config1).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 1: %v", err)
	}

	config2 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{"os":"windows"}`))
	image2, err := client.BuildImage().WithConfig(config2).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 2: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(image1).
		AddManifest(image2).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Manifests) != 2 {
		t.Fatalf("Manifests length = %d, want 2", len(m.Manifests))
	}
	if m.Manifests[0].Digest != image1.Digest() {
		t.Errorf("Manifests[0].Digest = %v, want %v", m.Manifests[0].Digest, image1.Digest())
	}
	if m.Manifests[1].Digest != image2.Digest() {
		t.Errorf("Manifests[1].Digest = %v, want %v", m.Manifests[1].Digest, image2.Digest())
	}
}

func TestIndexBuilder_WithSubject(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	// Build subject image.
	subjectConfig := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	subject, err := client.BuildImage().WithConfig(subjectConfig).Build(ctx)
	if err != nil {
		t.Fatalf("Build subject: %v", err)
	}

	// Build child image for the index.
	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{"arch":"amd64"}`))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		WithSubject(subject).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Subject == nil {
		t.Fatal("Subject is nil in serialized index")
	}
	if m.Subject.Digest != subject.Digest() {
		t.Errorf("Subject.Digest = %v, want %v", m.Subject.Digest, subject.Digest())
	}
}

func TestIndexBuilder_WithAnnotation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		WithAnnotation("org.test.version", "1.0").
		WithAnnotation("org.test.name", "test-index").
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["org.test.version"] != "1.0" {
		t.Errorf("Annotations[org.test.version] = %q, want %q", m.Annotations["org.test.version"], "1.0")
	}
	if m.Annotations["org.test.name"] != "test-index" {
		t.Errorf("Annotations[org.test.name] = %q, want %q", m.Annotations["org.test.name"], "test-index")
	}
}

func TestIndexBuilder_WithAnnotations(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	annotations := map[string]string{
		"k1": "v1",
		"k2": "v2",
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		WithAnnotations(annotations).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["k1"] != "v1" {
		t.Errorf("Annotations[k1] = %q, want %q", m.Annotations["k1"], "v1")
	}
}

func TestIndexBuilder_BuildAndPush(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer"))

	child, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	// Build creates the index and pushes it to the store.
	idx, err := client.BuildIndex().
		AddManifest(child).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// Verify the index was pushed to storage during Build().
	exists, err := store.Exists(ctx, idx.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push index to storage")
	}

	// Tag manually since memory store rejects duplicate pushes.
	if err := store.Tag(ctx, idx.Descriptor(), "multi-arch"); err != nil {
		t.Fatalf("Tag() unexpected error: %v", err)
	}

	desc, err := store.Resolve(ctx, "multi-arch")
	if err != nil {
		t.Fatalf("Resolve(multi-arch) unexpected error: %v", err)
	}
	if desc.Digest != idx.Digest() {
		t.Errorf("Resolve(multi-arch) digest = %v, want %v", desc.Digest, idx.Digest())
	}
}

func TestIndexBuilder_Build_ManifestInStorage(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	exists, err := store.Exists(ctx, idx.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push index to storage")
	}
}

func TestIndexBuilder_Build_SchemaVersion(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", m.SchemaVersion)
	}
	if m.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("MediaType = %q, want %q", m.MediaType, ocispec.MediaTypeImageIndex)
	}
}

func TestIndexBuilder_Build_DescriptorMediaType(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	child, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	idx, err := client.BuildIndex().
		AddManifest(child).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	desc := idx.Descriptor()
	if desc.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("Descriptor().MediaType = %q, want %q", desc.MediaType, ocispec.MediaTypeImageIndex)
	}
}

func TestIndexBuilder_MultipleManifests_WithPlatforms(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	platforms := []ocispec.Platform{
		{Architecture: "amd64", OS: "linux"},
		{Architecture: "arm64", OS: "linux"},
		{Architecture: "amd64", OS: "windows"},
	}

	var children []models.Manifest
	for i, plat := range platforms {
		cfg := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
		layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer-"+plat.Architecture+"-"+plat.OS))
		_ = i

		p := plat // capture
		img, err := client.BuildImage().
			WithConfig(cfg).
			AddLayer(layer).
			WithPlatform(&p).
			Build(ctx)
		if err != nil {
			t.Fatalf("BuildImage(%s/%s): %v", plat.OS, plat.Architecture, err)
		}
		children = append(children, img)
	}

	idx, err := client.BuildIndex().
		WithManifests(children).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Manifests) != 3 {
		t.Fatalf("Manifests length = %d, want 3", len(m.Manifests))
	}

	for i, child := range children {
		if m.Manifests[i].Digest != child.Digest() {
			t.Errorf("Manifests[%d].Digest = %v, want %v", i, m.Manifests[i].Digest, child.Digest())
		}
	}
}

func TestIndexBuilder_WithManifests_SliceIsolation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	config1 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image1, err := client.BuildImage().WithConfig(config1).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 1: %v", err)
	}

	config2 := client.NewBlob(ocispec.MediaTypeImageConfig, []byte(`{"iso":"test"}`))
	image2, err := client.BuildImage().WithConfig(config2).Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage 2: %v", err)
	}

	input := []models.Manifest{image1}
	builder := client.BuildIndex().WithManifests(input)

	// Mutate the original slice after calling WithManifests.
	input[0] = image2

	idx, err := builder.Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := idx.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := idx.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m ocispec.Index
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The index should contain image1, not image2.
	if len(m.Manifests) != 1 {
		t.Fatalf("Manifests length = %d, want 1", len(m.Manifests))
	}
	if m.Manifests[0].Digest != image1.Digest() {
		t.Errorf("Manifests[0].Digest = %v, want %v (image1)", m.Manifests[0].Digest, image1.Digest())
	}
}
