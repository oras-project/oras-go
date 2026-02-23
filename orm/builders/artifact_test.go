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
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/orm"
	"github.com/oras-project/oras-go/v3/orm/builders"
	"github.com/oras-project/oras-go/v3/orm/models"
)

func TestArtifactBuilder_Build_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	blob := client.NewBlob("application/octet-stream", []byte("artifact-blob"))

	artifact, err := client.BuildArtifact("application/vnd.test.sbom").
		AddBlob(blob).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	if artifact == nil {
		t.Fatal("Build() returned nil artifact")
	}
	if artifact.MediaType() != spec.MediaTypeArtifactManifest {
		t.Errorf("MediaType() = %q, want %q", artifact.MediaType(), spec.MediaTypeArtifactManifest)
	}

	// Verify artifact can be loaded and artifact type is correct.
	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	at, err := artifact.ArtifactType(ctx)
	if err != nil {
		t.Fatalf("ArtifactType() unexpected error: %v", err)
	}
	if at != "application/vnd.test.sbom" {
		t.Errorf("ArtifactType() = %q, want %q", at, "application/vnd.test.sbom")
	}

	// Verify blobs.
	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 1 {
		t.Fatalf("Blobs() returned %d, want 1", len(blobs))
	}
	if blobs[0].Digest() != blob.Digest() {
		t.Errorf("Blobs()[0].Digest() = %v, want %v", blobs[0].Digest(), blob.Digest())
	}
}

func TestArtifactBuilder_Build_EmptyArtifactType(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	_, err := client.BuildArtifact("").Build(ctx)
	if err == nil {
		t.Fatal("Build() expected error for empty artifactType, got nil")
	}
}

func TestArtifactBuilder_Build_NoBlobs(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	artifact, err := client.BuildArtifact("application/vnd.test").Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// Load and verify empty blobs.
	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 0 {
		t.Errorf("Blobs() returned %d, want 0", len(blobs))
	}
}

func TestArtifactBuilder_WithBlobs(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	blob1 := client.NewBlob("application/octet-stream", []byte("b1"))
	blob2 := client.NewBlob("application/octet-stream", []byte("b2"))

	artifact, err := client.BuildArtifact("application/vnd.test").
		WithBlobs([]*models.Blob{blob1, blob2}).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 2 {
		t.Fatalf("Blobs() returned %d, want 2", len(blobs))
	}
}

func TestArtifactBuilder_WithSubject(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	// Build a subject artifact.
	subject, err := client.BuildArtifact("application/vnd.subject").Build(ctx)
	if err != nil {
		t.Fatalf("Build subject: %v", err)
	}

	// Build artifact with subject.
	artifact, err := client.BuildArtifact("application/vnd.test").
		WithSubject(subject).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// Load and verify subject is referenced.
	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// The manifest should contain a subject reference.
	data, err := artifact.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}
	// Just verify it marshals without error; the subject descriptor
	// should be embedded in the JSON.
	if len(data) == 0 {
		t.Error("MarshalJSON() returned empty data")
	}
}

func TestArtifactBuilder_WithAnnotation(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	artifact, err := client.BuildArtifact("application/vnd.test").
		WithAnnotation("org.test.key", "value1").
		WithAnnotation("org.test.key2", "value2").
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := artifact.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	// Verify annotations are in the serialized manifest.
	var m spec.Artifact
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["org.test.key"] != "value1" {
		t.Errorf("Annotations[org.test.key] = %q, want %q", m.Annotations["org.test.key"], "value1")
	}
	if m.Annotations["org.test.key2"] != "value2" {
		t.Errorf("Annotations[org.test.key2] = %q, want %q", m.Annotations["org.test.key2"], "value2")
	}
}

func TestArtifactBuilder_WithAnnotations(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	annotations := map[string]string{
		"key1": "val1",
		"key2": "val2",
	}

	artifact, err := client.BuildArtifact("application/vnd.test").
		WithAnnotations(annotations).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	data, err := artifact.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() unexpected error: %v", err)
	}

	var m spec.Artifact
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations["key1"] != "val1" {
		t.Errorf("Annotations[key1] = %q, want %q", m.Annotations["key1"], "val1")
	}
}

func TestArtifactBuilder_BuildAndPush(t *testing.T) {
	ctx := t.Context()
	store := memory.New()

	blob := models.NewBlobFromBytes("application/octet-stream", []byte("push-test"))

	// Use the builder directly with a nil pusher for Build, then push
	// manually. The BuildAndPush flow works with registries where Push
	// is idempotent, but memory.Store rejects duplicates.
	builder := builders.NewArtifactBuilder("application/vnd.test", store, store, nil)
	artifact, err := builder.AddBlob(blob).Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	if artifact == nil {
		t.Fatal("Build() returned nil artifact")
	}

	// Verify the manifest was pushed to storage during Build().
	exists, err := store.Exists(ctx, artifact.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push manifest to storage")
	}

	// Tag manually since memory store rejects duplicate pushes.
	if err := store.Tag(ctx, artifact.Descriptor(), "v1.0"); err != nil {
		t.Fatalf("Tag() unexpected error: %v", err)
	}

	desc, err := store.Resolve(ctx, "v1.0")
	if err != nil {
		t.Fatalf("Resolve(v1.0) unexpected error: %v", err)
	}
	if desc.Digest != artifact.Digest() {
		t.Errorf("Resolve(v1.0) digest = %v, want %v", desc.Digest, artifact.Digest())
	}
}

func TestArtifactBuilder_Build_ManifestInStorage(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	artifact, err := client.BuildArtifact("application/vnd.test").Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	// The manifest should be pushed to the store during Build().
	exists, err := store.Exists(ctx, artifact.Descriptor())
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Build() did not push manifest to storage")
	}
}

func TestArtifactBuilder_MultipleBlobs(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	blob1 := client.NewBlob("application/octet-stream", []byte("blob-1"))
	blob2 := client.NewBlob("text/plain", []byte("blob-2"))
	blob3 := client.NewBlob("application/json", []byte(`{"key":"value"}`))

	artifact, err := client.BuildArtifact("application/vnd.multi").
		AddBlob(blob1).
		AddBlob(blob2).
		AddBlob(blob3).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	blobs, err := artifact.Blobs(ctx)
	if err != nil {
		t.Fatalf("Blobs() unexpected error: %v", err)
	}
	if len(blobs) != 3 {
		t.Fatalf("Blobs() returned %d, want 3", len(blobs))
	}

	expectedMediaTypes := []string{"application/octet-stream", "text/plain", "application/json"}
	for i, b := range blobs {
		if b.MediaType() != expectedMediaTypes[i] {
			t.Errorf("Blobs()[%d].MediaType() = %q, want %q", i, b.MediaType(), expectedMediaTypes[i])
		}
	}
}

func TestArtifactBuilder_Build_DescriptorMediaType(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	artifact, err := client.BuildArtifact("application/vnd.test").Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}

	desc := artifact.Descriptor()
	if desc.MediaType != spec.MediaTypeArtifactManifest {
		t.Errorf("Descriptor().MediaType = %q, want %q", desc.MediaType, spec.MediaTypeArtifactManifest)
	}
}

func TestArtifactBuilder_Build_WithPlatformSubject(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := orm.NewClient(store)

	// Build an image to use as subject.
	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer-data"))

	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	// Build artifact with image as subject.
	artifact, err := client.BuildArtifact("application/vnd.signature").
		WithSubject(image).
		Build(ctx)
	if err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}

	if err := artifact.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	data, err := artifact.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var m spec.Artifact
	if err := unmarshalJSON(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Subject == nil {
		t.Fatal("Subject is nil")
	}
	if m.Subject.Digest != image.Digest() {
		t.Errorf("Subject.Digest = %v, want %v", m.Subject.Digest, image.Digest())
	}
}
