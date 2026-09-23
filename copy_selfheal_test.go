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

package oras_test

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/internal/cas"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
)

// interruptedIndexDst simulates a destination registry left in the partially
// populated state produced by an interrupted recursive copy of a multi-arch
// image index: the child manifests' blobs have already been uploaded, but the
// child manifests themselves were never durably committed. The destination
// nonetheless reports those child manifests as already existing (a HEAD
// false-positive), so a plain copy skips re-pushing them; and it rejects any
// index that references a not-yet-committed child manifest with
// MANIFEST_BLOB_UNKNOWN, exactly as a spec-compliant registry does.
type interruptedIndexDst struct {
	content.Storage
	mu        sync.Mutex
	phantom   map[digest.Digest]bool // reported as existing but not yet committed
	committed map[digest.Digest]bool // actually pushed during the copy
}

func newInterruptedIndexDst(storage content.Storage, phantom ...digest.Digest) *interruptedIndexDst {
	d := &interruptedIndexDst{
		Storage:   storage,
		phantom:   make(map[digest.Digest]bool),
		committed: make(map[digest.Digest]bool),
	}
	for _, dg := range phantom {
		d.phantom[dg] = true
	}
	return d
}

func (d *interruptedIndexDst) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	d.mu.Lock()
	phantom := d.phantom[target.Digest] && !d.committed[target.Digest]
	d.mu.Unlock()
	if phantom {
		// Falsely report the not-yet-committed child manifest as present.
		return true, nil
	}
	return d.Storage.Exists(ctx, target)
}

func (d *interruptedIndexDst) Push(ctx context.Context, expected ocispec.Descriptor, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if expected.MediaType == ocispec.MediaTypeImageIndex {
		var idx ocispec.Index
		if err := json.Unmarshal(body, &idx); err != nil {
			return err
		}
		var missing errcode.Errors
		d.mu.Lock()
		for _, m := range idx.Manifests {
			if d.phantom[m.Digest] && !d.committed[m.Digest] {
				missing = append(missing, errcode.Error{
					Code:    errcode.ErrorCodeManifestBlobUnknown,
					Message: "blob unknown to registry",
					Detail:  m.Digest.String(),
				})
			}
		}
		d.mu.Unlock()
		if len(missing) > 0 {
			// Reject the index just like a registry whose referenced child
			// manifests are not all present.
			return missing
		}
	}
	d.mu.Lock()
	if d.phantom[expected.Digest] {
		d.committed[expected.Digest] = true
	}
	d.mu.Unlock()
	return d.Storage.Push(ctx, expected, bytes.NewReader(body))
}

// TestCopyGraph_SelfHealsInterruptedIndexCopy reproduces the failure in which a
// re-run of a recursive copy of a multi-arch image index never self-heals: the
// not-yet-committed child manifests are reported as already existing and thus
// skipped, and the subsequent index push is rejected with MANIFEST_BLOB_UNKNOWN.
// With the self-healing retry in copyGraph, the skipped child manifests are
// re-pushed and the index push succeeds.
func TestCopyGraph_SelfHealsInterruptedIndexCopy(t *testing.T) {
	ctx := context.Background()
	src := cas.NewMemory()

	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) ocispec.Descriptor {
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		blobs = append(blobs, blob)
		descs = append(descs, desc)
		return desc
	}
	makeManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) ocispec.Descriptor {
		m := ocispec.Manifest{Config: config, Layers: layers}
		j, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return appendBlob(ocispec.MediaTypeImageManifest, j)
	}
	makeIndex := func(manifests ...ocispec.Descriptor) ocispec.Descriptor {
		idx := ocispec.Index{Manifests: manifests}
		j, err := json.Marshal(idx)
		if err != nil {
			t.Fatal(err)
		}
		return appendBlob(ocispec.MediaTypeImageIndex, j)
	}

	// A two-platform index: each child manifest has its own config + layer,
	// mirroring linux/amd64 and linux/arm64 children of mongo:7.0.34.
	cfgA := appendBlob(ocispec.MediaTypeImageConfig, []byte("config-amd64"))
	layerA := appendBlob(ocispec.MediaTypeImageLayer, []byte("layer-amd64"))
	cfgB := appendBlob(ocispec.MediaTypeImageConfig, []byte("config-arm64"))
	layerB := appendBlob(ocispec.MediaTypeImageLayer, []byte("layer-arm64"))
	manifestA := makeManifest(cfgA, layerA)
	manifestB := makeManifest(cfgB, layerB)
	root := makeIndex(manifestA, manifestB)

	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to seed src: %d: %v", i, err)
		}
	}

	// Simulate the interrupted-copy state in the destination: every blob has
	// already been uploaded, but the two child manifests were never committed
	// (and the destination reports them as existing).
	backing := cas.NewMemory()
	for _, d := range []ocispec.Descriptor{cfgA, layerA, cfgB, layerB} {
		body, err := content.FetchAll(ctx, src, d)
		if err != nil {
			t.Fatal(err)
		}
		if err := backing.Push(ctx, d, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	dst := newInterruptedIndexDst(backing, manifestA.Digest, manifestB.Digest)

	// Copy the index. Without the self-healing retry this fails with
	// MANIFEST_BLOB_UNKNOWN; with it, the copy succeeds.
	if err := oras.CopyGraph(ctx, src, dst, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, want nil", err)
	}

	// The index and both child manifests must now be present and correct.
	for _, d := range []ocispec.Descriptor{manifestA, manifestB, root} {
		got, err := content.FetchAll(ctx, backing, d)
		if err != nil {
			t.Fatalf("missing content %s after copy: %v", d.Digest, err)
		}
		want, err := content.FetchAll(ctx, src, d)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("content mismatch for %s", d.Digest)
		}
	}
}
