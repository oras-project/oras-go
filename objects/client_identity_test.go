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

package objects_test

import (
	"context"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/objects"
	"github.com/oras-project/oras-go/v3/objects/models"
)

// A digest identifies content, not a kind: a manifest is also a blob, so the
// identity map can hold an entry of one kind when the caller asks for another.
// The cache must degrade to an uncached instance rather than panicking on a
// type assertion.

// manifestKinds enumerates the manifest media types that FetchManifest
// dispatches on without inspecting content.
var manifestKinds = []struct {
	name      string
	mediaType string
}{
	{"image", ocispec.MediaTypeImageManifest},
	{"index", ocispec.MediaTypeImageIndex},
	{"artifact", spec.MediaTypeArtifactManifest},
}

func TestClient_BlobThenManifestSameDigest(t *testing.T) {
	ctx := context.Background()
	for _, kind := range manifestKinds {
		t.Run(kind.name, func(t *testing.T) {
			client := objects.NewClient(memory.New())

			// Cache the content as a blob first.
			blob := client.NewBlob(kind.mediaType, []byte(`{"schemaVersion":2}`))

			// Asking for the same digest as a manifest must not panic.
			manifest, err := client.FetchManifest(ctx, blob.Descriptor())
			if err != nil {
				t.Fatalf("FetchManifest after NewBlob: %v", err)
			}
			if manifest == nil {
				t.Fatal("FetchManifest returned nil manifest")
			}
			if got, want := manifest.Digest(), blob.Digest(); got != want {
				t.Errorf("manifest digest = %v, want %v", got, want)
			}
		})
	}
}

func TestClient_ManifestThenBlobSameDigest(t *testing.T) {
	ctx := context.Background()
	for _, kind := range manifestKinds {
		t.Run(kind.name, func(t *testing.T) {
			store := memory.New()
			client := objects.NewClient(store)
			desc := pushBlob(t, ctx, store, kind.mediaType, []byte(`{"schemaVersion":2}`))

			// Cache the content as a manifest first.
			if _, err := client.FetchManifest(ctx, desc); err != nil {
				t.Fatalf("FetchManifest: %v", err)
			}

			// Asking for the same digest as a blob must not panic.
			blob, err := client.FetchBlob(ctx, desc)
			if err != nil {
				t.Fatalf("FetchBlob after FetchManifest: %v", err)
			}
			if blob == nil {
				t.Fatal("FetchBlob returned nil blob")
			}
			if got, want := blob.Digest(), desc.Digest; got != want {
				t.Errorf("blob digest = %v, want %v", got, want)
			}
		})
	}
}

// TestClient_ManifestKindMismatchSameDigest documents what happens when two
// descriptors share a digest but disagree on media type. The identity map is
// keyed on digest and the bytes are authoritative, so the first-cached kind
// wins; the second request must resolve to it rather than panicking.
//
// Only degenerate content reaches this: real bytes cannot satisfy both an image
// manifest (needs "config") and an index (needs "manifests").
func TestClient_ManifestKindMismatchSameDigest(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := objects.NewClient(store)
	data := []byte(`{"schemaVersion":2}`)

	imageDesc := pushBlob(t, ctx, store, ocispec.MediaTypeImageManifest, data)
	first, err := client.FetchManifest(ctx, imageDesc)
	if err != nil {
		t.Fatalf("FetchManifest as image: %v", err)
	}
	if _, ok := first.(*models.Image); !ok {
		t.Fatalf("FetchManifest as image returned %T, want *models.Image", first)
	}

	// Same bytes, same digest, different declared kind.
	indexDesc := imageDesc
	indexDesc.MediaType = ocispec.MediaTypeImageIndex
	second, err := client.FetchManifest(ctx, indexDesc)
	if err != nil {
		t.Fatalf("FetchManifest as index: %v", err)
	}
	if second != first {
		t.Errorf("FetchManifest returned %T, want the cached %T for the same digest", second, first)
	}
}

// TestClient_SameKindStillSharesIdentity guards against the mismatch handling
// regressing the identity map for the ordinary same-kind case.
func TestClient_SameKindStillSharesIdentity(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := objects.NewClient(store)
	desc := pushBlob(t, ctx, store, ocispec.MediaTypeImageManifest, []byte(`{"schemaVersion":2}`))

	first, err := client.FetchManifest(ctx, desc)
	if err != nil {
		t.Fatalf("first FetchManifest: %v", err)
	}
	second, err := client.FetchManifest(ctx, desc)
	if err != nil {
		t.Fatalf("second FetchManifest: %v", err)
	}
	if first != second {
		t.Error("FetchManifest returned different instances for the same digest")
	}
}
