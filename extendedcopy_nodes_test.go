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
	"fmt"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
)

// multiStartGraph builds small graphs in a memory store for the tests of the
// multi-start extended copy and verify functions.
//
// Every node name must be used at most once, as the name makes the content,
// and hence the digest, of the node unique.
type multiStartGraph struct {
	t     *testing.T
	store *memory.Store
}

func newMultiStartGraph(t *testing.T) *multiStartGraph {
	return &multiStartGraph{t: t, store: memory.New()}
}

func (g *multiStartGraph) push(mediaType string, data []byte) ocispec.Descriptor {
	g.t.Helper()
	desc := content.NewDescriptorFromBytes(mediaType, data)
	if err := g.store.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		g.t.Fatal(err)
	}
	return desc
}

// manifest pushes an image manifest with its own config blob. If subject is
// not nil, the manifest is a referrer of subject.
func (g *multiStartGraph) manifest(name string, subject *ocispec.Descriptor) ocispec.Descriptor {
	g.t.Helper()
	config := g.push("application/vnd.test.config", []byte("config-"+name))
	data, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    config,
		Layers:    []ocispec.Descriptor{},
		Subject:   subject,
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return g.push(ocispec.MediaTypeImageManifest, data)
}

// index pushes an image index that has the given manifests as successors.
func (g *multiStartGraph) index(name string, manifests ...ocispec.Descriptor) ocispec.Descriptor {
	g.t.Helper()
	if manifests == nil {
		manifests = []ocispec.Descriptor{}
	}
	data, err := json.Marshal(ocispec.Index{
		Versioned:   specs.Versioned{SchemaVersion: 2},
		MediaType:   ocispec.MediaTypeImageIndex,
		Manifests:   manifests,
		Annotations: map[string]string{"name": name},
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return g.push(ocispec.MediaTypeImageIndex, data)
}

// chain pushes n manifests, where the first has no subject and every other
// one is a referrer of the one before it.
func (g *multiStartGraph) chain(prefix string, n int) []ocispec.Descriptor {
	g.t.Helper()
	descs := make([]ocispec.Descriptor, 0, n)
	var subject *ocispec.Descriptor
	for i := 0; i < n; i++ {
		desc := g.manifest(fmt.Sprintf("%s%d", prefix, i), subject)
		descs = append(descs, desc)
		subject = &descs[i]
	}
	return descs
}

// predecessorCalls counts the FindPredecessors calls per node.
type predecessorCalls struct {
	mu    sync.Mutex
	count map[digest.Digest]int
}

func newPredecessorCalls() *predecessorCalls {
	return &predecessorCalls{count: make(map[digest.Digest]int)}
}

func (c *predecessorCalls) find(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	c.mu.Lock()
	c.count[desc.Digest]++
	c.mu.Unlock()
	return src.Predecessors(ctx, desc)
}

// checkVisitedOnce reports every node that was not looked up exactly once.
func (c *predecessorCalls) checkVisitedOnce(t *testing.T, nodes ...ocispec.Descriptor) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range nodes {
		if got := c.count[node.Digest]; got != 1 {
			t.Errorf("FindPredecessors called %d times for %s, want 1", got, node.Digest)
		}
	}
}

// checkExists checks that every descriptor is, or is not, in storage.
func checkExists(t *testing.T, storage content.Storage, want bool, descs ...ocispec.Descriptor) {
	t.Helper()
	for _, desc := range descs {
		got, err := storage.Exists(context.Background(), desc)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Exists(%s) = %v, want %v", desc.Digest, got, want)
		}
	}
}

func TestExtendedCopyGraphNodes_DisjointSubgraphs(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	a := g.manifest("a", nil)
	referrerOfA := g.manifest("referrer-of-a", &a)
	b := g.manifest("b", nil)
	referrerOfB := g.manifest("referrer-of-b", &b)
	unrelated := g.manifest("unrelated", nil)
	referrerOfUnrelated := g.manifest("referrer-of-unrelated", &unrelated)

	dst := memory.New()
	if err := oras.ExtendedCopyGraphNodes(ctx, g.store, dst, []ocispec.Descriptor{a, b}, oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraphNodes() error = %v", err)
	}
	checkExists(t, dst, true, a, referrerOfA, b, referrerOfB)
	checkExists(t, dst, false, unrelated, referrerOfUnrelated)
}

func TestExtendedCopyGraphNodes_OverlappingStartNodes(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	leaf := g.manifest("leaf", nil)
	idx := g.index("index", leaf)
	referrerOfIndex := g.manifest("referrer-of-index", &idx)
	unrelated := g.manifest("unrelated", nil)

	// The start nodes are an ancestor and a descendant of each other. The
	// order must not matter.
	for _, tc := range []struct {
		name  string
		nodes []ocispec.Descriptor
	}{
		{"descendant first", []ocispec.Descriptor{leaf, idx}},
		{"ancestor first", []ocispec.Descriptor{idx, leaf}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := newPredecessorCalls()
			opts := oras.ExtendedCopyGraphOptions{FindPredecessors: calls.find}
			dst := memory.New()
			if err := oras.ExtendedCopyGraphNodes(ctx, g.store, dst, tc.nodes, opts); err != nil {
				t.Fatalf("ExtendedCopyGraphNodes() error = %v", err)
			}
			checkExists(t, dst, true, leaf, idx, referrerOfIndex)
			checkExists(t, dst, false, unrelated)
			calls.checkVisitedOnce(t, leaf, idx, referrerOfIndex)
		})
	}
}

func TestExtendedCopyGraphNodes_DuplicateStartNodes(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	a := g.manifest("a", nil)
	referrerOfA := g.manifest("referrer-of-a", &a)

	calls := newPredecessorCalls()
	opts := oras.ExtendedCopyGraphOptions{FindPredecessors: calls.find}
	dst := memory.New()
	if err := oras.ExtendedCopyGraphNodes(ctx, g.store, dst, []ocispec.Descriptor{a, a, a}, opts); err != nil {
		t.Fatalf("ExtendedCopyGraphNodes() error = %v", err)
	}
	checkExists(t, dst, true, a, referrerOfA)
	calls.checkVisitedOnce(t, a, referrerOfA)
}

func TestExtendedCopyGraphNodes_Depth(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	// c[i+1] is a referrer of c[i]
	c := g.chain("c", 6)

	for _, tc := range []struct {
		name   string
		nodes  []ocispec.Descriptor
		depth  int
		copied []ocispec.Descriptor
		absent []ocispec.Descriptor
	}{
		{
			name:   "single start node",
			nodes:  []ocispec.Descriptor{c[0]},
			depth:  2,
			copied: c[:3],
			absent: c[3:],
		},
		{
			// c[2] is a start node, so c[4] is at depth 2 although it is at
			// depth 4 from c[0].
			name:   "measured from the nearest start node",
			nodes:  []ocispec.Descriptor{c[0], c[2]},
			depth:  2,
			copied: c[:5],
			absent: c[5:],
		},
		{
			name:   "depth 1",
			nodes:  []ocispec.Descriptor{c[0], c[2]},
			depth:  1,
			copied: c[:4],
			absent: c[4:],
		},
		{
			name:   "no depth limit",
			nodes:  []ocispec.Descriptor{c[0], c[2]},
			copied: c,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := memory.New()
			opts := oras.ExtendedCopyGraphOptions{Depth: tc.depth}
			if err := oras.ExtendedCopyGraphNodes(ctx, g.store, dst, tc.nodes, opts); err != nil {
				t.Fatalf("ExtendedCopyGraphNodes() error = %v", err)
			}
			checkExists(t, dst, true, tc.copied...)
			checkExists(t, dst, false, tc.absent...)
		})
	}
}

// TestExtendedCopyGraphNodes_IndexAndChildReferrers covers copying an index
// together with the referrers of its child manifests, while the index itself
// has no referrers.
func TestExtendedCopyGraphNodes_IndexAndChildReferrers(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	m1 := g.manifest("m1", nil)
	m2 := g.manifest("m2", nil)
	idx := g.index("index", m1, m2)
	r1 := g.manifest("r1", &m1)
	r2 := g.manifest("r2", &m2)
	referrerOfR1 := g.manifest("referrer-of-r1", &r1)
	other := g.manifest("other", nil)
	referrerOfOther := g.manifest("referrer-of-other", &other)

	// The index alone does not reach the referrers of its children.
	alone := memory.New()
	if err := oras.ExtendedCopyGraph(ctx, g.store, alone, idx, oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraph() error = %v", err)
	}
	checkExists(t, alone, true, idx, m1, m2)
	checkExists(t, alone, false, r1, r2, referrerOfR1)

	// Starting from the index and the referrers of its children copies
	// everything, with the default FindPredecessors.
	dst := memory.New()
	nodes := []ocispec.Descriptor{idx, r1, r2}
	if err := oras.ExtendedCopyGraphNodes(ctx, g.store, dst, nodes, oras.ExtendedCopyGraphOptions{}); err != nil {
		t.Fatalf("ExtendedCopyGraphNodes() error = %v", err)
	}
	checkExists(t, dst, true, idx, m1, m2, r1, r2, referrerOfR1)
	checkExists(t, dst, false, other, referrerOfOther)
}

func TestExtendedCopyGraphNodes_EmptyNodes(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	g.manifest("a", nil)

	for _, tc := range []struct {
		name  string
		nodes []ocispec.Descriptor
	}{
		{"nil", nil},
		{"empty", []ocispec.Descriptor{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := oras.ExtendedCopyGraphNodes(ctx, g.store, memory.New(), tc.nodes, oras.ExtendedCopyGraphOptions{})
			if err == nil {
				t.Fatal("ExtendedCopyGraphNodes() error = nil, want an error")
			}
			var copyError *oras.CopyError
			if !errors.As(err, &copyError) {
				t.Fatalf("ExtendedCopyGraphNodes() error = %T, want *oras.CopyError", err)
			}
		})
	}
}

func TestExtendedCopyGraphNodes_CopyError(t *testing.T) {
	ctx := context.Background()
	nodes := []ocispec.Descriptor{{}}

	for _, tc := range []struct {
		name       string
		src        content.ReadOnlyGraphStorage
		dst        content.Storage
		wantOrigin oras.CopyErrorOrigin
	}{
		{"nil source", nil, memory.New(), oras.CopyErrorOriginSource},
		{"nil destination", memory.New(), nil, oras.CopyErrorOriginDestination},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := oras.ExtendedCopyGraphNodes(ctx, tc.src, tc.dst, nodes, oras.ExtendedCopyGraphOptions{})
			var copyErr *oras.CopyError
			if !errors.As(err, &copyErr) {
				t.Fatalf("ExtendedCopyGraphNodes() error = %v, want a CopyError", err)
			}
			if copyErr.Origin != tc.wantOrigin {
				t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, tc.wantOrigin)
			}
			if want := "ExtendedCopyGraphNodes"; copyErr.Op != want {
				t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
			}
		})
	}
}
