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
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/objects/models"
)

func TestReference_Name(t *testing.T) {
	ref := models.NewReference("v1.0.0", nil, nil)

	if ref.Name() != "v1.0.0" {
		t.Errorf("Name() = %q, want %q", ref.Name(), "v1.0.0")
	}
}

func TestReference_Resolve_PreCached(t *testing.T) {
	ctx := t.Context()

	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("cached")),
		Size:      6,
	}
	manifest := models.NewArtifact(desc, nil, nil, nil)

	// Pre-cache the manifest.
	ref := models.NewReference("latest", manifest, nil)

	got, err := ref.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("Resolve() = nil, want non-nil")
	}
	if got.Digest() != desc.Digest {
		t.Errorf("Resolve().Digest() = %v, want %v", got.Digest(), desc.Digest)
	}
}

func TestReference_Resolve_FetchesFromClient(t *testing.T) {
	ctx := t.Context()

	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("fetched")),
		Size:      7,
	}
	expected := models.NewArtifact(desc, nil, nil, nil)

	var fetchCount int
	client := &mockManifestClient{
		fetchByRef: func(ctx context.Context, ref string) (models.Manifest, error) {
			fetchCount++
			if ref != "latest" {
				t.Errorf("FetchByReference() ref = %q, want %q", ref, "latest")
			}
			return expected, nil
		},
	}

	// No pre-cached manifest.
	ref := models.NewReference("latest", nil, client)

	got, err := ref.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got.Digest() != desc.Digest {
		t.Errorf("Resolve().Digest() = %v, want %v", got.Digest(), desc.Digest)
	}
	if fetchCount != 1 {
		t.Errorf("FetchByReference called %d times, want 1", fetchCount)
	}

	// Second Resolve should use cached value.
	got2, err := ref.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve() second call unexpected error: %v", err)
	}
	if got2.Digest() != desc.Digest {
		t.Errorf("Resolve() second call digest mismatch")
	}
	if fetchCount != 1 {
		t.Errorf("FetchByReference called %d times after second Resolve, want 1", fetchCount)
	}
}

func TestReference_Resolve_NoClient(t *testing.T) {
	ref := models.NewReference("latest", nil, nil)

	_, err := ref.Resolve(t.Context())
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Resolve() error = %v, want ErrNoClient", err)
	}
}

func TestReference_Resolve_ClientError(t *testing.T) {
	expectedErr := errors.New("network error")
	client := &mockManifestClient{
		fetchByRef: func(ctx context.Context, ref string) (models.Manifest, error) {
			return nil, expectedErr
		},
	}

	ref := models.NewReference("latest", nil, client)

	_, err := ref.Resolve(t.Context())
	if !errors.Is(err, expectedErr) {
		t.Errorf("Resolve() error = %v, want %v", err, expectedErr)
	}
}

func TestReference_Tag(t *testing.T) {
	ctx := t.Context()

	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("tagged")),
		Size:      6,
	}
	manifest := models.NewArtifact(desc, nil, nil, nil)

	var pushRef string
	client := &mockManifestClient{
		pushManifest: func(ctx context.Context, m models.Manifest, ref string) error {
			pushRef = ref
			return nil
		},
	}

	ref := models.NewReference("v2.0", nil, client)

	if err := ref.Tag(ctx, manifest); err != nil {
		t.Fatalf("Tag() unexpected error: %v", err)
	}
	if pushRef != "v2.0" {
		t.Errorf("PushManifest received ref %q, want %q", pushRef, "v2.0")
	}

	// After tagging, the manifest should be cached.
	got, ok := ref.Manifest()
	if !ok {
		t.Fatal("Manifest() returned false after Tag()")
	}
	if got.Digest() != desc.Digest {
		t.Errorf("Manifest().Digest() = %v, want %v", got.Digest(), desc.Digest)
	}
}

func TestReference_Tag_NoClient(t *testing.T) {
	manifest := models.NewArtifact(ocispec.Descriptor{
		Digest: digest.FromBytes([]byte("x")),
		Size:   1,
	}, nil, nil, nil)

	ref := models.NewReference("tag", nil, nil)

	err := ref.Tag(t.Context(), manifest)
	if !errors.Is(err, models.ErrNoClient) {
		t.Errorf("Tag() error = %v, want ErrNoClient", err)
	}
}

func TestReference_Tag_PushError(t *testing.T) {
	expectedErr := errors.New("push failed")
	client := &mockManifestClient{
		pushManifest: func(ctx context.Context, m models.Manifest, ref string) error {
			return expectedErr
		},
	}

	manifest := models.NewArtifact(ocispec.Descriptor{
		Digest: digest.FromBytes([]byte("x")),
		Size:   1,
	}, nil, nil, nil)

	ref := models.NewReference("tag", nil, client)

	err := ref.Tag(t.Context(), manifest)
	if !errors.Is(err, expectedErr) {
		t.Errorf("Tag() error = %v, want %v", err, expectedErr)
	}

	// Manifest should not be cached on error.
	_, ok := ref.Manifest()
	if ok {
		t.Error("Manifest() returned true after failed Tag(), want false")
	}
}

func TestReference_Manifest_NotResolved(t *testing.T) {
	ref := models.NewReference("latest", nil, nil)

	got, ok := ref.Manifest()
	if ok {
		t.Error("Manifest() returned true for unresolved reference, want false")
	}
	if got != nil {
		t.Errorf("Manifest() = %v, want nil", got)
	}
}

func TestReference_Manifest_PreCached(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("pre")),
		Size:      3,
	}
	manifest := models.NewArtifact(desc, nil, nil, nil)

	ref := models.NewReference("latest", manifest, nil)

	got, ok := ref.Manifest()
	if !ok {
		t.Fatal("Manifest() returned false for pre-cached reference, want true")
	}
	if got.Digest() != desc.Digest {
		t.Errorf("Manifest().Digest() = %v, want %v", got.Digest(), desc.Digest)
	}
}

func TestReference_ConcurrentResolve(t *testing.T) {
	desc := ocispec.Descriptor{
		MediaType: spec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes([]byte("concurrent")),
		Size:      10,
	}
	expected := models.NewArtifact(desc, nil, nil, nil)

	var fetchCount atomic.Int32
	client := &mockManifestClient{
		fetchByRef: func(ctx context.Context, ref string) (models.Manifest, error) {
			fetchCount.Add(1)
			return expected, nil
		},
	}

	ref := models.NewReference("latest", nil, client)
	ctx := t.Context()

	var wg sync.WaitGroup
	var errCount atomic.Int32
	const goroutines = 50

	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			got, err := ref.Resolve(ctx)
			if err != nil {
				errCount.Add(1)
				t.Errorf("goroutine %d: Resolve() error: %v", id, err)
				return
			}
			if got.Digest() != desc.Digest {
				errCount.Add(1)
				t.Errorf("goroutine %d: Resolve().Digest() = %v, want %v", id, got.Digest(), desc.Digest)
			}
		}(i)
	}

	wg.Wait()

	if errCount.Load() > 0 {
		t.Fatalf("%d goroutines encountered errors", errCount.Load())
	}

	// Due to lazy[T] semantics with a mutex, fetchByRef should be called
	// exactly once (the first goroutine to acquire the lock fetches,
	// others get the cached result).
	if fetchCount.Load() != 1 {
		t.Errorf("FetchByReference called %d times, want 1", fetchCount.Load())
	}
}
