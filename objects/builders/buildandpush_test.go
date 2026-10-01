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
	"context"
	"errors"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/objects"
)

// errTagTarget wraps memory.Store and fails the Tag step, which is the last
// thing BuildAndPush does. It lets the push half of BuildAndPush fail without
// disturbing the build half.
type errTagTarget struct {
	*memory.Store
	err error
}

func (t *errTagTarget) Tag(_ context.Context, _ ocispec.Descriptor, _ string) error {
	return t.err
}

// errTagClient returns a client whose Tag always fails.
func errTagClient(err error) *objects.Client {
	return objects.NewClient(&errTagTarget{Store: memory.New(), err: err})
}

func TestImageBuilder_BuildAndPush_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := objects.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	layer := client.NewBlob(ocispec.MediaTypeImageLayer, []byte("layer-data"))

	image, err := client.BuildImage().
		WithConfig(config).
		AddLayer(layer).
		BuildAndPush(ctx, "v1.0")
	if err != nil {
		t.Fatalf("BuildAndPush() unexpected error: %v", err)
	}
	if image == nil {
		t.Fatal("BuildAndPush() returned nil image")
	}

	// The tag must resolve to the digest that was built.
	desc, err := store.Resolve(ctx, "v1.0")
	if err != nil {
		t.Fatalf("Resolve(v1.0) unexpected error: %v", err)
	}
	if desc.Digest != image.Descriptor().Digest {
		t.Errorf("tag resolves to %s, want the built manifest %s", desc.Digest, image.Descriptor().Digest)
	}
}

func TestImageBuilder_BuildAndPush_BuildError(t *testing.T) {
	ctx := t.Context()
	client := objects.NewClient(memory.New())

	// No config: Build fails before anything is pushed.
	image, err := client.BuildImage().BuildAndPush(ctx, "v1.0")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when config is missing")
	}
	if image != nil {
		t.Error("BuildAndPush() returned a non-nil image alongside an error")
	}
}

func TestImageBuilder_BuildAndPush_PushError(t *testing.T) {
	ctx := t.Context()
	wantErr := errors.New("tag failed")
	client := errTagClient(wantErr)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))

	image, err := client.BuildImage().
		WithConfig(config).
		BuildAndPush(ctx, "v1.0")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when the push fails")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("BuildAndPush() error = %v, want it to wrap %v", err, wantErr)
	}
	if image != nil {
		t.Error("BuildAndPush() returned a non-nil image alongside an error")
	}
}

func TestArtifactBuilder_BuildAndPush_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := objects.NewClient(store)

	blob := client.NewBlob("application/vnd.test.blob", []byte("blob-data"))

	artifact, err := client.BuildArtifact("application/vnd.test.artifact").
		AddBlob(blob).
		BuildAndPush(ctx, "sbom")
	if err != nil {
		t.Fatalf("BuildAndPush() unexpected error: %v", err)
	}
	if artifact == nil {
		t.Fatal("BuildAndPush() returned nil artifact")
	}

	desc, err := store.Resolve(ctx, "sbom")
	if err != nil {
		t.Fatalf("Resolve(sbom) unexpected error: %v", err)
	}
	if desc.Digest != artifact.Descriptor().Digest {
		t.Errorf("tag resolves to %s, want the built manifest %s", desc.Digest, artifact.Descriptor().Digest)
	}
}

func TestArtifactBuilder_BuildAndPush_BuildError(t *testing.T) {
	ctx := t.Context()
	client := objects.NewClient(memory.New())

	// Empty artifactType: Build fails before anything is pushed.
	artifact, err := client.BuildArtifact("").BuildAndPush(ctx, "sbom")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when artifactType is empty")
	}
	if artifact != nil {
		t.Error("BuildAndPush() returned a non-nil artifact alongside an error")
	}
}

func TestArtifactBuilder_BuildAndPush_PushError(t *testing.T) {
	ctx := t.Context()
	wantErr := errors.New("tag failed")
	client := errTagClient(wantErr)

	artifact, err := client.BuildArtifact("application/vnd.test.artifact").
		BuildAndPush(ctx, "sbom")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when the push fails")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("BuildAndPush() error = %v, want it to wrap %v", err, wantErr)
	}
	if artifact != nil {
		t.Error("BuildAndPush() returned a non-nil artifact alongside an error")
	}
}

func TestIndexBuilder_BuildAndPush_Success(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	client := objects.NewClient(store)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error building the child image: %v", err)
	}

	index, err := client.BuildIndex().
		AddManifest(image).
		BuildAndPush(ctx, "multi")
	if err != nil {
		t.Fatalf("BuildAndPush() unexpected error: %v", err)
	}
	if index == nil {
		t.Fatal("BuildAndPush() returned nil index")
	}

	desc, err := store.Resolve(ctx, "multi")
	if err != nil {
		t.Fatalf("Resolve(multi) unexpected error: %v", err)
	}
	if desc.Digest != index.Descriptor().Digest {
		t.Errorf("tag resolves to %s, want the built index %s", desc.Digest, index.Descriptor().Digest)
	}
}

func TestIndexBuilder_BuildAndPush_BuildError(t *testing.T) {
	ctx := t.Context()
	client := objects.NewClient(memory.New())

	// No manifests: Build fails before anything is pushed.
	index, err := client.BuildIndex().BuildAndPush(ctx, "multi")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when no manifests are added")
	}
	if index != nil {
		t.Error("BuildAndPush() returned a non-nil index alongside an error")
	}
}

func TestIndexBuilder_BuildAndPush_PushError(t *testing.T) {
	ctx := t.Context()
	wantErr := errors.New("tag failed")
	client := errTagClient(wantErr)

	config := client.NewBlob(ocispec.MediaTypeImageConfig, []byte("{}"))
	image, err := client.BuildImage().WithConfig(config).Build(ctx)
	if err != nil {
		t.Fatalf("Build() unexpected error building the child image: %v", err)
	}

	index, err := client.BuildIndex().
		AddManifest(image).
		BuildAndPush(ctx, "multi")
	if err == nil {
		t.Fatal("BuildAndPush() expected an error when the push fails")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("BuildAndPush() error = %v, want it to wrap %v", err, wantErr)
	}
	if index != nil {
		t.Error("BuildAndPush() returned a non-nil index alongside an error")
	}
}
