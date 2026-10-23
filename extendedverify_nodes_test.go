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
	"context"
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
)

// checkVerifiedNodes checks that the report is successful, that each wanted
// node was verified exactly once, and that no unwanted node was verified.
func checkVerifiedNodes(t *testing.T, report *oras.VerifyGraphReport, want, unwanted []ocispec.Descriptor) {
	t.Helper()
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	count := make(map[digest.Digest]int, len(report.Verified))
	for _, desc := range report.Verified {
		count[desc.Digest]++
	}
	for _, desc := range want {
		if got := count[desc.Digest]; got != 1 {
			t.Errorf("%s verified %d times, want 1", desc.Digest, got)
		}
	}
	for _, desc := range unwanted {
		if got := count[desc.Digest]; got != 0 {
			t.Errorf("%s verified %d times, want 0", desc.Digest, got)
		}
	}
}

func TestExtendedVerifyGraphNodes_DisjointSubgraphs(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	a := g.manifest("a", nil)
	referrerOfA := g.manifest("referrer-of-a", &a)
	b := g.manifest("b", nil)
	referrerOfB := g.manifest("referrer-of-b", &b)
	unrelated := g.manifest("unrelated", nil)
	referrerOfUnrelated := g.manifest("referrer-of-unrelated", &unrelated)

	report, err := oras.ExtendedVerifyGraphNodes(ctx, g.store, []ocispec.Descriptor{a, b}, oras.ExtendedVerifyGraphOptions{})
	if err != nil {
		t.Fatalf("ExtendedVerifyGraphNodes() error = %v", err)
	}
	checkVerifiedNodes(t, report,
		[]ocispec.Descriptor{a, referrerOfA, b, referrerOfB},
		[]ocispec.Descriptor{unrelated, referrerOfUnrelated})
}

func TestExtendedVerifyGraphNodes_OverlappingAndDuplicateStartNodes(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	leaf := g.manifest("leaf", nil)
	idx := g.index("index", leaf)
	referrerOfIndex := g.manifest("referrer-of-index", &idx)
	unrelated := g.manifest("unrelated", nil)

	// leaf is listed twice, and is also a descendant of the start node idx
	nodes := []ocispec.Descriptor{leaf, idx, leaf}
	report, err := oras.ExtendedVerifyGraphNodes(ctx, g.store, nodes, oras.ExtendedVerifyGraphOptions{})
	if err != nil {
		t.Fatalf("ExtendedVerifyGraphNodes() error = %v", err)
	}
	checkVerifiedNodes(t, report,
		[]ocispec.Descriptor{leaf, idx, referrerOfIndex},
		[]ocispec.Descriptor{unrelated})
}

func TestExtendedVerifyGraphNodes_DepthFromNearestStartNode(t *testing.T) {
	ctx := context.Background()
	g := newMultiStartGraph(t)
	// c[i+1] is a referrer of c[i]
	c := g.chain("c", 6)

	// c[4] is at depth 2 from the start node c[2], and c[5] is beyond it.
	opts := oras.ExtendedVerifyGraphOptions{Depth: 2}
	report, err := oras.ExtendedVerifyGraphNodes(ctx, g.store, []ocispec.Descriptor{c[0], c[2]}, opts)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraphNodes() error = %v", err)
	}
	checkVerifiedNodes(t, report, c[:5], c[5:])
}

// TestExtendedVerifyGraphNodes_IndexAndChildReferrers covers verifying an index
// together with the referrers of its child manifests, while the index itself
// has no referrers.
func TestExtendedVerifyGraphNodes_IndexAndChildReferrers(t *testing.T) {
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
	report, err := oras.ExtendedVerifyGraph(
		ctx, g.store, idx, oras.ExtendedVerifyGraphOptions{},
	)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() error = %v", err)
	}
	checkVerifiedNodes(t, report,
		[]ocispec.Descriptor{idx, m1, m2},
		[]ocispec.Descriptor{r1, r2, referrerOfR1},
	)

	// Starting from the index and the referrers of its children verifies
	// everything, with the default FindPredecessors.
	report, err = oras.ExtendedVerifyGraphNodes(
		ctx, g.store,
		[]ocispec.Descriptor{idx, r1, r2},
		oras.ExtendedVerifyGraphOptions{},
	)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraphNodes() error = %v", err)
	}
	checkVerifiedNodes(t, report,
		[]ocispec.Descriptor{idx, m1, m2, r1, r2, referrerOfR1},
		[]ocispec.Descriptor{other, referrerOfOther},
	)
}

func TestExtendedVerifyGraphNodes_EmptyNodes(t *testing.T) {
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
			report, err := oras.ExtendedVerifyGraphNodes(ctx, g.store, tc.nodes, oras.ExtendedVerifyGraphOptions{})
			if err == nil {
				t.Fatal("ExtendedVerifyGraphNodes() error = nil, want an error")
			}
			var copyError *oras.CopyError
			if !errors.As(err, &copyError) {
				t.Fatalf("ExtendedVerifyGraphNodes() error = %T, want *oras.CopyError", err)
			}
			if report != nil {
				t.Errorf("ExtendedVerifyGraphNodes() report = %v, want nil", report)
			}
		})
	}
}

func TestExtendedVerifyGraphNodes_Error(t *testing.T) {
	ctx := context.Background()
	report, err := oras.ExtendedVerifyGraphNodes(ctx, nil, []ocispec.Descriptor{{}}, oras.ExtendedVerifyGraphOptions{})
	if report != nil {
		t.Errorf("ExtendedVerifyGraphNodes() report = %v, want nil", report)
	}
	var copyErr *oras.CopyError
	if !errors.As(err, &copyErr) {
		t.Fatalf("ExtendedVerifyGraphNodes() error = %v, want a CopyError", err)
	}
	if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
		t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
	}
	if want := "ExtendedVerifyGraphNodes"; copyErr.Op != want {
		t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
	}
}
