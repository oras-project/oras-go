package oras_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/spec"
)

func TestExtendedVerify_FullVerify(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()

	manifest := g.descs[3]
	ref := "foobar"
	ctx := context.Background()
	if err := src.Tag(ctx, manifest, ref); err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test extended verify with the zero options
	gotDesc, report, err := oras.ExtendedVerify(ctx, src, ref, oras.ExtendedVerifyOptions{})
	if err != nil {
		t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, manifest) {
		t.Errorf("ExtendedVerify() = %v, want %v", gotDesc, manifest)
	}
	if report == nil {
		t.Fatal("ExtendedVerify() report = nil, want non-nil")
	}
	if !reflect.DeepEqual(report.Node, manifest) {
		t.Errorf("report.Node = %v, want %v", report.Node, manifest)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	// the referrers of the tagged node are verified too
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 9))
	checkDescriptors(t, "report.Skipped", report.Skipped, g.descs, nil)
	checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, nil)

	// test extended verify with the default options
	gotDesc, report, err = oras.ExtendedVerify(ctx, src, ref, oras.DefaultExtendedVerifyOptions)
	if err != nil {
		t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, manifest) {
		t.Errorf("ExtendedVerify() = %v, want %v", gotDesc, manifest)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 9))
}

// failedDescriptors extracts the descriptors of the failed nodes.
func failedDescriptors(failed []oras.FailedNode) []ocispec.Descriptor {
	var descs []ocispec.Descriptor
	for _, f := range failed {
		descs = append(descs, f.Descriptor)
	}
	return descs
}

func TestExtendedVerify_NotFound(t *testing.T) {
	src := memory.New()

	ref := "foobar"
	ctx := context.Background()
	_, report, err := oras.ExtendedVerify(ctx, src, ref, oras.ExtendedVerifyOptions{})
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
	if report != nil {
		t.Errorf("ExtendedVerify() report = %v, want nil", report)
	}
}

func TestExtendedVerify_ResumeAfterCorruption(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()

	manifest := g.descs[3]
	ref := "foobar"
	ctx := context.Background()
	if err := src.Tag(ctx, manifest, ref); err != nil {
		t.Fatal("fail to tag root node", err)
	}

	corruptTarget := &graphTarget{ReadOnlyGraphTarget: src, digest: g.descs[8].Digest}
	gotDesc, report, err := oras.ExtendedVerify(ctx, corruptTarget, ref, oras.DefaultExtendedVerifyOptions)
	if err != nil {
		t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, manifest) {
		t.Errorf("ExtendedVerify() = %v, want %v", gotDesc, manifest)
	}
	if report.OK() {
		t.Fatal("report.OK() = true, want false")
	}
	checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, []int{8})
	// on error resume next: everything else is still verified
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, []int{0, 1, 2, 3, 4, 5, 6, 7, 9})
}

func TestExtendedVerify_CopyError(t *testing.T) {
	ctx := context.Background()

	t.Run("src target is nil", func(t *testing.T) {
		_, report, err := oras.ExtendedVerify(ctx, nil, "", oras.DefaultExtendedVerifyOptions)
		if err == nil {
			t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, true)
		}
		if report != nil {
			t.Errorf("ExtendedVerify() report = %v, want nil", report)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedVerify() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if want := "ExtendedVerify"; copyErr.Op != want {
			t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
		}
	})

	t.Run("resolve error", func(t *testing.T) {
		src := &unresolvableGraphTarget{ReadOnlyGraphTarget: memory.New()}
		_, report, err := oras.ExtendedVerify(ctx, src, "latest", oras.DefaultExtendedVerifyOptions)
		if err == nil {
			t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, true)
		}
		if report != nil {
			t.Errorf("ExtendedVerify() report = %v, want nil", report)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedVerify() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if want := "Resolve"; copyErr.Op != want {
			t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
		}
		if !errors.Is(copyErr.Err, errExtendedVerifyResolve) {
			t.Errorf("ExtendedVerify() error = %v, wantErr %v", copyErr.Err, errExtendedVerifyResolve)
		}
	})

	t.Run("find predecessors error", func(t *testing.T) {
		g := newReferrerGraph(t)
		src := g.push()
		ref := "foobar"
		if err := src.Tag(ctx, g.descs[3], ref); err != nil {
			t.Fatal("fail to tag root node", err)
		}

		wantErr := errors.New("find predecessors error")
		opts := oras.ExtendedVerifyOptions{
			ExtendedVerifyGraphOptions: oras.ExtendedVerifyGraphOptions{
				FindPredecessors: func(context.Context, content.ReadOnlyGraphStorage, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					return nil, wantErr
				},
			},
		}
		gotDesc, report, err := oras.ExtendedVerify(ctx, src, ref, opts)
		if !errors.Is(err, wantErr) {
			t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, wantErr)
		}
		// the resolved node is still returned, but there is no report
		if !reflect.DeepEqual(gotDesc, g.descs[3]) {
			t.Errorf("ExtendedVerify() = %v, want %v", gotDesc, g.descs[3])
		}
		if report != nil {
			t.Errorf("ExtendedVerify() report = %v, want nil", report)
		}
	})

	t.Run("walk aborted by PostVerify", func(t *testing.T) {
		g := newReferrerGraph(t)
		src := g.push()
		ref := "foobar"
		if err := src.Tag(ctx, g.descs[3], ref); err != nil {
			t.Fatal("fail to tag root node", err)
		}

		wantErr := errors.New("PostVerify error")
		opts := oras.ExtendedVerifyOptions{
			ExtendedVerifyGraphOptions: oras.ExtendedVerifyGraphOptions{
				VerifyGraphOptions: oras.VerifyGraphOptions{
					PostVerify: func(context.Context, ocispec.Descriptor, error) error {
						return wantErr
					},
				},
			},
		}
		gotDesc, report, err := oras.ExtendedVerify(ctx, src, ref, opts)
		if !errors.Is(err, wantErr) {
			t.Fatalf("ExtendedVerify() error = %v, wantErr %v", err, wantErr)
		}
		if !reflect.DeepEqual(gotDesc, g.descs[3]) {
			t.Errorf("ExtendedVerify() = %v, want %v", gotDesc, g.descs[3])
		}
		if report == nil {
			t.Fatal("ExtendedVerify() report = nil, want the partial report")
		}
		if !reflect.DeepEqual(report.Node, g.descs[3]) {
			t.Errorf("report.Node = %v, want %v", report.Node, g.descs[3])
		}
	})
}

func TestExtendedVerifyGraph_FullVerify(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()

	ctx := context.Background()

	// test extended verify by descs[3], a node in the middle of the DAG
	report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[3], oras.ExtendedVerifyGraphOptions{})
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	// graphs rooted by descs[7] and descs[9] should be verified
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 9))
	checkDescriptors(t, "report.Skipped", report.Skipped, g.descs, nil)
	checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, nil)

	// test extended verify by descs[7], which is already a root and thus
	// verifies its own sub-DAG (subjects included) only
	report, err = oras.ExtendedVerifyGraph(ctx, src, g.descs[7], oras.DefaultExtendedVerifyGraphOptions)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 7))
}

func TestExtendedVerifyGraph_WithDepthOption(t *testing.T) {
	g := newDepthGraph(t)
	src := g.push()
	ctx := context.Background()

	tests := []struct {
		name         string
		depth        int
		wantVerified []int
	}{
		{
			name:         "default depth 0",
			depth:        0,
			wantVerified: makeRange(0, 11), // graphs rooted by descs[5] and descs[11]
		},
		{
			name:         "depth 1",
			depth:        1,
			wantVerified: makeRange(0, 5), // graphs rooted by descs[3] and descs[5]
		},
		{
			name:         "depth 2",
			depth:        2,
			wantVerified: makeRange(0, 11), // graphs rooted by descs[5] and descs[11]
		},
		{
			name:         "negative depth",
			depth:        -1,
			wantVerified: makeRange(0, 11),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := oras.ExtendedVerifyGraphOptions{Depth: tt.depth}
			report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[0], opts)
			if err != nil {
				t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
			}
			if !report.OK() {
				t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
			}
			checkDescriptors(t, "report.Verified", report.Verified, g.descs, tt.wantVerified)
		})
	}
}

func TestExtendedVerifyGraph_WithFindPredecessorsOption(t *testing.T) {
	// generate test content
	g := &extendedVerifyGraph{t: t}
	g.appendBlob(ocispec.MediaTypeImageConfig, []byte("config_1")) // Blob 0
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))       // Blob 1
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))       // Blob 2
	g.generateManifest(nil, g.descs[0], g.descs[1:3]...)           // Blob 3
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))     // Blob 4
	g.generateArtifactManifest(g.descs[3], g.descs[4])             // Blob 5 (root)
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("baz"))       // Blob 6
	g.generateArtifactManifest(g.descs[3], g.descs[6])             // Blob 7 (root)
	g.appendBlob(ocispec.MediaTypeImageConfig, []byte("config_2")) // Blob 8
	g.appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))     // Blob 9
	g.generateManifest(nil, g.descs[8], g.descs[9])                // Blob 10
	g.generateIndex(g.descs[3], g.descs[10])                       // Blob 11 (root)
	src := g.push()
	ctx := context.Background()

	// test extended verify by descs[3] with media type filter
	var findPredecessorsCalls int64
	opts := oras.ExtendedVerifyGraphOptions{
		FindPredecessors: func(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			atomic.AddInt64(&findPredecessorsCalls, 1)
			predecessors, err := src.Predecessors(ctx, desc)
			if err != nil {
				return nil, err
			}
			var filtered []ocispec.Descriptor
			for _, p := range predecessors {
				// filter media type
				switch p.MediaType {
				case spec.MediaTypeArtifactManifest:
					filtered = append(filtered, p)
				}
			}
			return filtered, nil
		},
	}
	report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[3], opts)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	if got := atomic.LoadInt64(&findPredecessorsCalls); got == 0 {
		t.Error("count(FindPredecessors()) = 0, want it to be called")
	}
	// graphs rooted by descs[5] and descs[7] should be verified, but not the
	// index descs[11] and what is only reachable from it
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 7))
}

func TestExtendedVerifyGraph_FindPredecessorsError(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()
	ctx := context.Background()

	wantErr := errors.New("find predecessors error")
	opts := oras.ExtendedVerifyGraphOptions{
		FindPredecessors: func(context.Context, content.ReadOnlyGraphStorage, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			return nil, wantErr
		},
	}
	report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[3], opts)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, wantErr)
	}
	if report != nil {
		t.Errorf("ExtendedVerifyGraph() report = %v, want nil", report)
	}
	var copyErr *oras.CopyError
	if !errors.As(err, &copyErr) {
		t.Fatalf("ExtendedVerifyGraph() error is not a CopyError: %v", err)
	}
	if want := "FindPredecessors"; copyErr.Op != want {
		t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
	}
	if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
		t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
	}
	if !reflect.DeepEqual(copyErr.Descriptor, g.descs[3]) {
		t.Errorf("CopyError descriptor = %v, want %v", copyErr.Descriptor, g.descs[3])
	}
}

func TestExtendedVerifyGraph_ResumeAfterCorruption(t *testing.T) {
	ctx := context.Background()

	t.Run("corrupt leaf", func(t *testing.T) {
		g := newReferrerGraph(t)
		// the layer "foo" is shared by all the sub-DAGs
		corruptTarget := &graphTarget{ReadOnlyGraphTarget: g.push(), digest: g.descs[1].Digest}

		report, err := oras.ExtendedVerifyGraph(ctx, corruptTarget, g.descs[3], oras.DefaultExtendedVerifyGraphOptions)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if report.OK() {
			t.Fatal("report.OK() = true, want false")
		}
		// the shared layer is reported exactly once
		checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, []int{1})
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, []int{0, 2, 3, 4, 5, 6, 7, 8, 9})
	})

	t.Run("corrupt manifest", func(t *testing.T) {
		g := newReferrerGraph(t)
		// descs[5] is reachable from the root descs[7] only, and its
		// successor descs[4] can only be discovered through it.
		corruptTarget := &graphTarget{ReadOnlyGraphTarget: g.push(), digest: g.descs[5].Digest}

		report, err := oras.ExtendedVerifyGraph(ctx, corruptTarget, g.descs[3], oras.DefaultExtendedVerifyGraphOptions)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if report.OK() {
			t.Fatal("report.OK() = true, want false")
		}
		checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, []int{5})
		// descs[4] is undiscoverable, while descs[3] and its successors are
		// still verified through the other root descs[9]
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, []int{0, 1, 2, 3, 6, 7, 8, 9})
	})
}

func TestExtendedVerifyGraph_WithOptions(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()
	ctx := context.Background()
	node := g.descs[3]

	t.Run("PreVerify PostVerify counts", func(t *testing.T) {
		var preVerifyCount, postVerifyCount int64
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				PreVerify: func(ctx context.Context, desc ocispec.Descriptor) error {
					atomic.AddInt64(&preVerifyCount, 1)
					return nil
				},
				PostVerify: func(ctx context.Context, desc ocispec.Descriptor, verifyErr error) error {
					atomic.AddInt64(&postVerifyCount, 1)
					if verifyErr != nil {
						t.Errorf("PostVerify() verifyErr = %v, want nil", verifyErr)
					}
					return nil
				},
			},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, src, node, opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if !report.OK() {
			t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
		}
		// each node is handled once, even the ones shared by several roots
		if got, want := atomic.LoadInt64(&preVerifyCount), int64(len(g.descs)); got != want {
			t.Errorf("count(PreVerify()) = %v, want %v", got, want)
		}
		if got, want := atomic.LoadInt64(&postVerifyCount), int64(len(g.descs)); got != want {
			t.Errorf("count(PostVerify()) = %v, want %v", got, want)
		}
	})

	t.Run("PostVerify receives the verify error", func(t *testing.T) {
		corruptTarget := &graphTarget{ReadOnlyGraphTarget: src, digest: g.descs[8].Digest}
		var gotErrs int64
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				PostVerify: func(ctx context.Context, desc ocispec.Descriptor, verifyErr error) error {
					if verifyErr != nil {
						atomic.AddInt64(&gotErrs, 1)
						if desc.Digest != g.descs[8].Digest {
							t.Errorf("PostVerify() verifyErr = %v for descs %v, want nil", verifyErr, desc)
						}
					}
					return nil
				},
			},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, corruptTarget, node, opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if report.OK() {
			t.Fatal("report.OK() = true, want false")
		}
		if got, want := atomic.LoadInt64(&gotErrs), int64(1); got != want {
			t.Errorf("count(PostVerify() with error) = %v, want %v", got, want)
		}
	})

	t.Run("PreVerify SkipNode", func(t *testing.T) {
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				PreVerify: func(ctx context.Context, desc ocispec.Descriptor) error {
					if desc.Digest == g.descs[1].Digest {
						return oras.SkipNode
					}
					return nil
				},
			},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, src, node, opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if !report.OK() {
			t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
		}
		checkDescriptors(t, "report.Skipped", report.Skipped, g.descs, []int{1})
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, []int{0, 2, 3, 4, 5, 6, 7, 8, 9})
	})

	t.Run("PreVerify error aborts the walk", func(t *testing.T) {
		wantErr := errors.New("PreVerify error")
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				PreVerify: func(ctx context.Context, desc ocispec.Descriptor) error {
					return wantErr
				},
			},
		}
		if _, err := oras.ExtendedVerifyGraph(ctx, src, node, opts); !errors.Is(err, wantErr) {
			t.Errorf("ExtendedVerifyGraph() error = %v, wantErr %v", err, wantErr)
		}
	})

	t.Run("PostVerify error aborts the walk", func(t *testing.T) {
		wantErr := errors.New("PostVerify error")
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				PostVerify: func(ctx context.Context, desc ocispec.Descriptor, verifyErr error) error {
					return wantErr
				},
			},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, src, node, opts)
		if !errors.Is(err, wantErr) {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, wantErr)
		}
		// unlike the other errors, the report of the processed nodes is
		// returned together with the error
		if report == nil {
			t.Error("ExtendedVerifyGraph() report = nil, want the partial report")
		}
	})

	t.Run("FindSuccessors override", func(t *testing.T) {
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{
				FindSuccessors: func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					successors, err := content.Successors(ctx, fetcher, desc)
					if err != nil {
						return nil, err
					}
					// filter out the config
					var filtered []ocispec.Descriptor
					for _, s := range successors {
						if s.MediaType != ocispec.MediaTypeImageConfig {
							filtered = append(filtered, s)
						}
					}
					return filtered, nil
				},
			},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, src, node, opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if !report.OK() {
			t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
		}
		// the config descs[0] is never discovered
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(1, 9))
	})

	t.Run("MaxMetadataBytes exceeded", func(t *testing.T) {
		opts := oras.ExtendedVerifyGraphOptions{
			VerifyGraphOptions: oras.VerifyGraphOptions{MaxMetadataBytes: 1},
		}
		report, err := oras.ExtendedVerifyGraph(ctx, src, node, opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, false)
		}
		if report.OK() {
			t.Fatal("report.OK() = true, want false")
		}
		// none of the manifests of the roots descs[7] and descs[9] can be read
		checkDescriptors(t, "report.Failed", failedDescriptors(report.Failed), g.descs, []int{7, 9})
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, nil)
		for _, f := range report.Failed {
			if !errors.Is(f.Err, errdef.ErrSizeExceedsLimit) {
				t.Errorf("report.Failed err = %v, want it to wrap %v", f.Err, errdef.ErrSizeExceedsLimit)
			}
		}
	})
}

func TestExtendedVerifyGraph_VerifiedSetReuse(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()
	ctx := context.Background()

	shared := oras.NewVerifiedSet()
	opts := oras.ExtendedVerifyGraphOptions{
		VerifyGraphOptions: oras.VerifyGraphOptions{Verified: shared},
	}
	report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[3], opts)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() [populate] error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	if got, want := shared.Len(), len(g.descs); got != want {
		t.Fatalf("shared.Len() = %v, want %v", got, want)
	}

	var skippedCount int64
	opts.OnVerifySkipped = func(ctx context.Context, desc ocispec.Descriptor) error {
		atomic.AddInt64(&skippedCount, 1)
		return nil
	}
	report, err = oras.ExtendedVerifyGraph(ctx, src, g.descs[3], opts)
	if err != nil {
		t.Fatalf("ExtendedVerifyGraph() [reuse] error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	// manifests are always re-read to discover their successors, while the
	// blobs that were already verified are skipped
	checkDescriptors(t, "report.Verified", report.Verified, g.descs, []int{3, 5, 7, 9})
	checkDescriptors(t, "report.Skipped", report.Skipped, g.descs, []int{0, 1, 2, 4, 6, 8})
	if got, want := atomic.LoadInt64(&skippedCount), int64(6); got != want {
		t.Errorf("count(OnVerifySkipped()) = %v, want %v", got, want)
	}
}

func TestExtendedVerifyGraph_WithConcurrencyLimit(t *testing.T) {
	g := newDepthGraph(t)
	src := g.push()
	ctx := context.Background()

	// non-positive concurrency falls back to the default
	for _, concurrency := range []int{-1, 0, 1, 2, 3, 10} {
		opts := oras.DefaultExtendedVerifyGraphOptions
		opts.Concurrency = concurrency
		report, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[0], opts)
		if err != nil {
			t.Fatalf("ExtendedVerifyGraph(concurrency: %d) error = %v, wantErr %v", concurrency, err, false)
		}
		if !report.OK() {
			t.Fatalf("ExtendedVerifyGraph(concurrency: %d) report.OK() = false, want true; Failed = %v", concurrency, report.Failed)
		}
		checkDescriptors(t, "report.Verified", report.Verified, g.descs, makeRange(0, 11))
	}
}

func TestExtendedVerifyGraph_ContextCanceled(t *testing.T) {
	g := newReferrerGraph(t)
	src := g.push()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := oras.ExtendedVerifyGraph(ctx, src, g.descs[3], oras.DefaultExtendedVerifyGraphOptions)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, context.Canceled)
	}
}

func TestExtendedVerifyGraph_CopyError(t *testing.T) {
	t.Run("src target is nil", func(t *testing.T) {
		ctx := context.Background()
		report, err := oras.ExtendedVerifyGraph(ctx, nil, ocispec.Descriptor{}, oras.DefaultExtendedVerifyGraphOptions)
		if err == nil {
			t.Fatalf("ExtendedVerifyGraph() error = %v, wantErr %v", err, true)
		}
		if report != nil {
			t.Errorf("ExtendedVerifyGraph() report = %v, want nil", report)
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("ExtendedVerifyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if want := "ExtendedVerifyGraph"; copyErr.Op != want {
			t.Errorf("CopyError op = %v, want %v", copyErr.Op, want)
		}
	})
}
