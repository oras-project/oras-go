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
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/objects/models"
)

// awkwardManifest is a valid image manifest that a Go round-trip cannot
// reproduce byte-for-byte: the keys are not in struct order and it carries an
// extension field that ocispec.Manifest does not model.
const awkwardManifest = `{"layers":[],"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"schemaVersion":2,"x-unmodelled-extension":{"kept":true}}`

// recordingPusher captures what was handed to Push.
type recordingPusher struct {
	desc ocispec.Descriptor
	data []byte
}

func (p *recordingPusher) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	p.desc = expected
	p.data = data
	return nil
}

func pushAwkward(t *testing.T, ctx context.Context, store *memory.Store) ocispec.Descriptor {
	t.Helper()
	raw := []byte(awkwardManifest)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(raw),
		Size:      int64(len(raw)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(raw)); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	return desc
}

// TestImage_BytesPreservesOriginalEncoding is the core guarantee: what comes
// back out is what the digest was computed over.
func TestImage_BytesPreservesOriginalEncoding(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	desc := pushAwkward(t, ctx, store)

	img := models.NewImage(desc, store, store, nil)
	got, err := img.Bytes(ctx)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	if !bytes.Equal(got, []byte(awkwardManifest)) {
		t.Errorf("Bytes did not preserve the original encoding:\n got: %s\nwant: %s", got, awkwardManifest)
	}
	if d := digest.FromBytes(got); d != desc.Digest {
		t.Errorf("digest of Bytes = %v, want %v", d, desc.Digest)
	}
}

// TestImage_MarshalJSONDoesNotRoundTrip documents why Push must not marshal:
// if this ever starts matching, the encoding happens to agree for this input,
// not because marshalling is digest-safe in general.
func TestImage_MarshalJSONDoesNotRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	desc := pushAwkward(t, ctx, store)

	img := models.NewImage(desc, store, store, nil)
	if err := img.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	marshalled, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if digest.FromBytes(marshalled) == desc.Digest {
		t.Skip("marshalling happened to reproduce the original bytes for this input")
	}
	t.Logf("re-encode digest %v != original %v, as expected", digest.FromBytes(marshalled), desc.Digest)
}

// TestImage_PushSendsOriginalBytes covers the path that actually corrupts:
// pushing under a descriptor whose digest the payload no longer matches.
func TestImage_PushSendsOriginalBytes(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	desc := pushAwkward(t, ctx, store)

	pusher := &recordingPusher{}
	// client is nil so Push takes the direct pusher path.
	img := models.NewImage(desc, store, pusher, nil)
	if err := img.Push(ctx, ""); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if !bytes.Equal(pusher.data, []byte(awkwardManifest)) {
		t.Errorf("Push sent re-encoded bytes:\n got: %s\nwant: %s", pusher.data, awkwardManifest)
	}
	if d := digest.FromBytes(pusher.data); d != pusher.desc.Digest {
		t.Errorf("pushed content digest %v does not match the descriptor it was pushed under (%v)", d, pusher.desc.Digest)
	}
}
