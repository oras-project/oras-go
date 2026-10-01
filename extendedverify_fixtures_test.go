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
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/internal/spec"
)

// extendedVerifyGraph is a small helper for generating test content.
type extendedVerifyGraph struct {
	t     *testing.T
	blobs [][]byte
	descs []ocispec.Descriptor
}

func (g *extendedVerifyGraph) appendBlob(mediaType string, blob []byte) {
	g.blobs = append(g.blobs, blob)
	g.descs = append(g.descs, ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	})
}

func (g *extendedVerifyGraph) generateManifest(subject *ocispec.Descriptor, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
	manifest := ocispec.Manifest{
		Config:  config,
		Layers:  layers,
		Subject: subject,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		g.t.Fatal(err)
	}
	g.appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
}

func (g *extendedVerifyGraph) generateIndex(manifests ...ocispec.Descriptor) {
	index := ocispec.Index{
		Manifests: manifests,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		g.t.Fatal(err)
	}
	g.appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
}

func (g *extendedVerifyGraph) generateArtifactManifest(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
	manifest := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subject,
		Blobs:     blobs,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		g.t.Fatal(err)
	}
	g.appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
}

// push pushes all the generated content to a new memory store.
func (g *extendedVerifyGraph) push() *memory.Store {
	store := memory.New()
	ctx := context.Background()
	for i := range g.blobs {
		if err := store.Push(ctx, g.descs[i], bytes.NewReader(g.blobs[i])); err != nil {
			g.t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}
	return store
}

// newReferrerGraph generates the following graph, where an arrow points from
// a node to its successors (subject links included):
//
//	9 (manifest) --subject--> 3 (manifest) --> 0, 1, 2
//	5 (artifact) --subject--> 3, and 5 --> 4
//	7 (artifact) --subject--> 5, and 7 --> 6
//	9 --> 0, 8
//
// Extended verifying from descs[3] finds the roots 7 and 9.
func newReferrerGraph(t *testing.T) *extendedVerifyGraph {
	g := &extendedVerifyGraph{t: t}
	g.appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	g.generateManifest(nil, g.descs[0], g.descs[1:3]...)         // Blob 3
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 4
	g.generateArtifactManifest(g.descs[3], g.descs[4])           // Blob 5
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 6
	g.generateArtifactManifest(g.descs[5], g.descs[6])           // Blob 7 (root)
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))     // Blob 8
	g.generateManifest(&g.descs[3], g.descs[0], g.descs[8])      // Blob 9 (root)
	return g
}

// newDepthGraph generates a graph that is used to test the Depth option.
// Extended verifying from descs[0] finds the roots 5 and 11 with no depth
// limit; descs[12:15] are unrelated to descs[0] and must never be verified.
func newDepthGraph(t *testing.T) *extendedVerifyGraph {
	g := &extendedVerifyGraph{t: t}
	g.appendBlob(ocispec.MediaTypeImageConfig, []byte("config_1")) // Blob 0
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))       // Blob 1
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))       // Blob 2
	g.generateManifest(nil, g.descs[0], g.descs[1:3]...)           // Blob 3
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))       // Blob 4
	g.generateManifest(nil, g.descs[0], g.descs[4])                // Blob 5 (root)
	g.appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2")) // Blob 6
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))     // Blob 7
	g.generateManifest(nil, g.descs[6], g.descs[7])                // Blob 8
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))     // Blob 9
	g.generateArtifactManifest(g.descs[8], g.descs[9])             // Blob 10
	g.generateIndex(g.descs[3], g.descs[10])                       // Blob 11 (root)
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("goodbye"))   // Blob 12
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))     // Blob 13
	g.generateArtifactManifest(g.descs[12], g.descs[13])           // Blob 14 (root)
	return g
}

// indices selects the descriptors at the given indices.
func indices(descs []ocispec.Descriptor, idx ...int) []ocispec.Descriptor {
	selected := make([]ocispec.Descriptor, 0, len(idx))
	for _, i := range idx {
		selected = append(selected, descs[i])
	}
	return selected
}

// makeRange returns the integers in [from, to].
func makeRange(from, to int) []int {
	var r []int
	for i := from; i <= to; i++ {
		r = append(r, i)
	}
	return r
}

// checkDescriptors checks that got contains exactly the descriptors at the
// wanted indices of descs, in any order and without duplicates.
func checkDescriptors(t *testing.T, name string, got []ocispec.Descriptor, descs []ocispec.Descriptor, wantIdx []int) {
	t.Helper()
	if len(got) != len(wantIdx) {
		t.Errorf("len(%s) = %d, want %d", name, len(got), len(wantIdx))
	}
	gotSet := make(map[digest.Digest]bool, len(got))
	for _, d := range got {
		gotSet[d.Digest] = true
	}
	wantSet := make(map[digest.Digest]bool, len(wantIdx))
	for _, i := range wantIdx {
		wantSet[descs[i].Digest] = true
		if !gotSet[descs[i].Digest] {
			t.Errorf("%s missing descs[%d]", name, i)
		}
	}
	for _, d := range got {
		if !wantSet[d.Digest] {
			for i := range descs {
				if descs[i].Digest == d.Digest {
					t.Errorf("%s unexpectedly contains descs[%d]", name, i)
				}
			}
		}
	}
}

type graphTarget struct {
	oras.ReadOnlyGraphTarget
	digest digest.Digest
}

func (c *graphTarget) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if target.Digest == c.digest {
		return io.NopCloser(bytes.NewReader([]byte("digest no longer matches the descriptor"))), nil
	}
	return c.ReadOnlyGraphTarget.Fetch(ctx, target)
}

// unresolvableGraphTarget fails to resolve any reference.
type unresolvableGraphTarget struct {
	oras.ReadOnlyGraphTarget
}

var errExtendedVerifyResolve = errors.New("extended verify resolve error")

func (u *unresolvableGraphTarget) Resolve(_ context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errExtendedVerifyResolve
}
