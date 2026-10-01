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
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/cas"
)

type storage struct {
	content.Storage
	digest digest.Digest
}

func (c *storage) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if target.Digest == c.digest {
		return io.NopCloser(bytes.NewReader([]byte("digest no longer matches the descriptor"))), nil
	}
	return c.Storage.Fetch(ctx, target)
}

type cancelFetch struct {
	content.ReadOnlyStorage
	cancel context.CancelFunc
}

func (c *cancelFetch) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if target.MediaType == ocispec.MediaTypeImageLayer {
		c.cancel()
		return nil, ctx.Err()
	}
	return c.ReadOnlyStorage.Fetch(ctx, target)
}

type mockReferenceFetcher struct {
	oras.ReadOnlyTarget
	fetchReference int64
	fetch          int64
}

func (f *mockReferenceFetcher) FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	atomic.AddInt64(&f.fetchReference, 1)
	desc, err := f.ReadOnlyTarget.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	rc, err := f.ReadOnlyTarget.Fetch(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, rc, nil
}

func (f *mockReferenceFetcher) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	atomic.AddInt64(&f.fetch, 1)
	return f.ReadOnlyTarget.Fetch(ctx, target)
}

type resolver struct {
	oras.ReadOnlyTarget
}

func (b *resolver) Resolve(_ context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errResolve
}

var errResolve = errors.New("resolve error")

func TestVerifyGraph_FullVerify(t *testing.T) {
	src := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	report, err := oras.VerifyGraph(ctx, src, root, oras.DefaultVerifyGraphOptions)
	if err != nil {
		t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("VerifyGraph() report.OK() = false, want true; Failed = %v", report.Failed)
	}
	if got, want := len(report.Verified), len(descs); got != want {
		t.Errorf("len(report.Verified) = %v, want %v", got, want)
	}
	for i, desc := range descs {
		found := false
		for _, v := range report.Verified {
			if reflect.DeepEqual(v, desc) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("report.Verified missing descs[%d] = %v", i, desc)
		}
	}
	if got := len(report.Failed); got != 0 {
		t.Errorf("len(report.Failed) = %v, want 0", got)
	}
	if got := len(report.Skipped); got != 0 {
		t.Errorf("len(report.Skipped) = %v, want 0", got)
	}
}

func TestVerifyGraph_ResumeAfterCorruption(t *testing.T) {
	src := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1 - will be corrupted
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	corrupt := &storage{Storage: src, digest: descs[1].Digest}
	root := descs[3]
	report, err := oras.VerifyGraph(ctx, corrupt, root, oras.DefaultVerifyGraphOptions)
	if err != nil {
		t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
	}
	if report.OK() {
		t.Fatal("VerifyGraph() report.OK() = true, want false")
	}
	if got, want := len(report.Failed), 1; got != want {
		t.Fatalf("len(report.Failed) = %v, want %v", got, want)
	}
	if got, want := report.Failed[0].Descriptor.Digest, descs[1].Digest; got != want {
		t.Errorf("report.Failed[0].Descriptor.Digest = %v, want %v", got, want)
	}
	// on error resume next: the manifest, config and sibling layer must
	// still be reported verified despite the corrupt layer.
	if got, want := len(report.Verified), len(descs)-1; got != want {
		t.Errorf("len(report.Verified) = %v, want %v", got, want)
	}
	for _, d := range []ocispec.Descriptor{descs[0], descs[2], descs[3]} {
		found := false
		for _, v := range report.Verified {
			if reflect.DeepEqual(v, d) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("report.Verified missing %v", d)
		}
	}
}

func TestVerifyGraph_WithOptions(t *testing.T) {
	src := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}
	root := descs[3]

	t.Run("FindSuccessors override", func(t *testing.T) {
		opts := oras.DefaultVerifyGraphOptions
		opts.FindSuccessors = func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			successors, err := content.Successors(ctx, fetcher, desc)
			if err != nil {
				return nil, err
			}
			// filter out the config, like the CopyGraph equivalent test does
			var filtered []ocispec.Descriptor
			for _, s := range successors {
				if s.MediaType != ocispec.MediaTypeImageConfig {
					filtered = append(filtered, s)
				}
			}
			return filtered, nil
		}
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
		}
		if !report.OK() {
			t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
		}
		// config is filtered out of successors, so only the manifest and the
		// two layers are verified.
		if got, want := len(report.Verified), 3; got != want {
			t.Errorf("len(report.Verified) = %v, want %v", got, want)
		}
	})

	t.Run("PreVerify PostVerify counts", func(t *testing.T) {
		var preVerifyCount, postVerifyCount int64
		opts := oras.VerifyGraphOptions{
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
		}
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
		}
		if !report.OK() {
			t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
		}
		if got, want := preVerifyCount, int64(len(descs)); got != want {
			t.Errorf("count(PreVerify()) = %v, want %v", got, want)
		}
		if got, want := postVerifyCount, int64(len(descs)); got != want {
			t.Errorf("count(PostVerify()) = %v, want %v", got, want)
		}
	})

	t.Run("PreVerify SkipNode", func(t *testing.T) {
		// PreVerify-returned SkipNode records
		// the node as skipped directly. OnVerifySkipped, like OnCopySkipped,
		// only fires for the "already verified" (Verified-set) skip path
		// below, not for this one.
		opts := oras.VerifyGraphOptions{
			PreVerify: func(ctx context.Context, desc ocispec.Descriptor) error {
				if desc.Digest == descs[1].Digest {
					return oras.SkipNode
				}
				return nil
			},
		}
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
		}
		if got, want := len(report.Skipped), 1; got != want {
			t.Fatalf("len(report.Skipped) = %v, want %v", got, want)
		}
		if got, want := report.Skipped[0].Digest, descs[1].Digest; got != want {
			t.Errorf("report.Skipped[0].Digest = %v, want %v", got, want)
		}
		// the rest of the graph is unaffected by the one skipped node.
		if got, want := len(report.Verified), len(descs)-1; got != want {
			t.Errorf("len(report.Verified) = %v, want %v", got, want)
		}
	})

	t.Run("OnVerifySkipped fires for Verified-set skips", func(t *testing.T) {
		shared := oras.NewVerifiedSet()
		shared.Add(descs[1].Digest) // pre-mark one leaf as already verified
		var skippedCount int64
		opts := oras.VerifyGraphOptions{
			KnownVerified: shared,
			OnVerifySkipped: func(ctx context.Context, desc ocispec.Descriptor) error {
				atomic.AddInt64(&skippedCount, 1)
				return nil
			},
		}
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
		}
		if got, want := len(report.Skipped), 1; got != want {
			t.Fatalf("len(report.Skipped) = %v, want %v", got, want)
		}
		if got, want := report.Skipped[0].Digest, descs[1].Digest; got != want {
			t.Errorf("report.Skipped[0].Digest = %v, want %v", got, want)
		}
		if got, want := skippedCount, int64(1); got != want {
			t.Errorf("count(OnVerifySkipped()) = %v, want %v", got, want)
		}
	})

	t.Run("MaxMetadataBytes exceeded", func(t *testing.T) {
		// on error resume next: exceeding the metadata limit while reading
		// the manifest is recorded as a failure, not returned as an error.
		opts := oras.VerifyGraphOptions{MaxMetadataBytes: 1}
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
		}
		if report.OK() {
			t.Fatal("report.OK() = true, want false")
		}
		if got, want := len(report.Failed), 1; got != want {
			t.Fatalf("len(report.Failed) = %v, want %v", got, want)
		}
		if got, want := report.Failed[0].Descriptor.Digest, root.Digest; got != want {
			t.Errorf("report.Failed[0].Descriptor.Digest = %v, want %v", got, want)
		}
		if !errors.Is(report.Failed[0].Err, errdef.ErrSizeExceedsLimit) {
			t.Errorf("report.Failed[0].Err = %v, want it to wrap %v", report.Failed[0].Err, errdef.ErrSizeExceedsLimit)
		}
	})

	t.Run("PreVerify error aborts the walk", func(t *testing.T) {
		wantErr := errors.New("PreVerify error")
		opts := oras.VerifyGraphOptions{
			PreVerify: func(ctx context.Context, desc ocispec.Descriptor) error {
				return wantErr
			},
		}
		_, err := oras.VerifyGraph(ctx, src, root, opts)
		if !errors.Is(err, wantErr) {
			t.Errorf("VerifyGraph() error = %v, want it to wrap %v", err, wantErr)
		}
	})

	t.Run("PostVerify error aborts the walk", func(t *testing.T) {
		wantErr := errors.New("PostVerify error")
		opts := oras.VerifyGraphOptions{
			PostVerify: func(ctx context.Context, desc ocispec.Descriptor, verifyErr error) error {
				return wantErr
			},
		}
		_, err := oras.VerifyGraph(ctx, src, root, opts)
		if !errors.Is(err, wantErr) {
			t.Errorf("VerifyGraph() error = %v, want it to wrap %v", err, wantErr)
		}
	})
}

func TestVerifyGraph_InvalidSuccessorDescriptor(t *testing.T) {
	ctx := context.Background()
	rootContent := []byte("root")
	root := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, rootContent)

	tests := []struct {
		name      string
		successor ocispec.Descriptor
		reason    string
	}{
		{
			name: "missing manifest size",
			successor: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    digest.FromString("successor"),
			},
			reason: "manifest size must be greater than zero",
		},
		{
			name: "negative size",
			successor: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    digest.FromString("successor"),
				Size:      -1,
			},
			reason: "invalid size -1",
		},
		{
			name: "invalid digest",
			successor: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    "not-a-digest",
				Size:      1,
			},
			reason: "invalid digest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := cas.NewMemory()
			if err := src.Push(ctx, root, bytes.NewReader(rootContent)); err != nil {
				t.Fatal(err)
			}
			opts := oras.VerifyGraphOptions{
				FindSuccessors: func(context.Context, content.Fetcher, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					return []ocispec.Descriptor{tt.successor}, nil
				},
			}

			// invalid successor descriptors are content problems, not walk
			// failures: VerifyGraph itself does not error, but the report
			// records root as failed with the reason in its error.
			report, err := oras.VerifyGraph(ctx, src, root, opts)
			if err != nil {
				t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
			}
			if got, want := len(report.Failed), 1; got != want {
				t.Fatalf("len(report.Failed) = %v, want %v", got, want)
			}
			want := fmt.Sprintf("invalid successor descriptor: successor media type: %s; successor size: %d; successor digest: %s",
				tt.successor.MediaType, tt.successor.Size, tt.successor.Digest)
			got := report.Failed[0].Err.Error()
			if !strings.Contains(got, want) || !strings.Contains(got, tt.reason) {
				t.Errorf("report.Failed[0].Err = %q, want it to contain %q and %q", got, want, tt.reason)
			}
		})
	}
}

func TestVerifyGraph_ZeroSizeBlobSuccessor(t *testing.T) {
	ctx := context.Background()
	src := cas.NewMemory()
	rootContent := []byte("root")
	root := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, rootContent)
	emptyBlob := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, nil)
	contents := []struct {
		desc ocispec.Descriptor
		blob []byte
	}{
		{root, rootContent},
		{emptyBlob, nil},
	}
	for _, item := range contents {
		if err := src.Push(ctx, item.desc, bytes.NewReader(item.blob)); err != nil {
			t.Fatal(err)
		}
	}
	opts := oras.VerifyGraphOptions{
		FindSuccessors: func(_ context.Context, _ content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			if content.Equal(desc, root) {
				return []ocispec.Descriptor{emptyBlob}, nil
			}
			return nil, nil
		},
	}

	report, err := oras.VerifyGraph(ctx, src, root, opts)
	if err != nil {
		t.Fatalf("VerifyGraph() error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	found := false
	for _, v := range report.Verified {
		if reflect.DeepEqual(v, emptyBlob) {
			found = true
		}
	}
	if !found {
		t.Errorf("report.Verified missing the empty blob = %v", emptyBlob)
	}
}

func TestVerifyGraph_ForeignLayers(t *testing.T) {
	src := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		desc := ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		}
		if mediaType == ocispec.MediaTypeImageLayerNonDistributable {
			desc.URLs = append(desc.URLs, "http://127.0.0.1/dummy")
			blob = nil
		}
		descs = append(descs, desc)
		blobs = append(blobs, blob)
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))               // Blob 0
	appendBlob(ocispec.MediaTypeImageLayerNonDistributable, []byte("hello")) // Blob 1 (foreign, not pushed)
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))                   // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))                   // Blob 3
	generateManifest(descs[0], descs[1:4]...)                                // Blob 4

	ctx := context.Background()
	for i := range blobs {
		if blobs[i] == nil {
			continue // the foreign layer is never pushed to src
		}
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[len(descs)-1]
	report, err := oras.VerifyGraph(ctx, src, root, oras.DefaultVerifyGraphOptions)
	if err != nil {
		t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	// the foreign layer (never pushed to src) is excluded before it would be
	// fetched, so it must not appear anywhere in the report.
	if got, want := len(report.Verified), len(blobs)-1; got != want {
		t.Errorf("len(report.Verified) = %v, want %v", got, want)
	}
	for _, v := range report.Verified {
		if v.Digest == descs[1].Digest {
			t.Errorf("report.Verified unexpectedly contains the foreign layer %v", descs[1])
		}
	}
}

func TestVerifyGraph_WithConcurrencyLimit(t *testing.T) {
	src := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    config,
			Layers:    layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(index.MediaType, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 4
	generateManifest(descs[0], descs[4])                       // Blob 5
	generateIndex(descs[3], descs[5])                          // Blob 6

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[len(descs)-1]
	directSuccessorsNum := 2
	opts := oras.DefaultVerifyGraphOptions
	for i := 1; i <= directSuccessorsNum; i++ {
		opts.Concurrency = i
		report, err := oras.VerifyGraph(ctx, src, root, opts)
		if err != nil {
			t.Fatalf("VerifyGraph(concurrency: %d) error = %v, wantErr %v", i, err, false)
		}
		if !report.OK() {
			t.Fatalf("VerifyGraph(concurrency: %d) report.OK() = false, want true; Failed = %v", i, report.Failed)
		}
		if got, want := len(report.Verified), len(blobs); got != want {
			t.Errorf("VerifyGraph(concurrency: %d): len(report.Verified) = %v, want %v", i, got, want)
		}
	}
}

func TestVerify_FullVerify(t *testing.T) {
	src := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "foobar"
	if err := src.Tag(ctx, root, ref); err != nil {
		t.Fatal("fail to tag root node", err)
	}

	gotDesc, report, err := oras.Verify(ctx, src, ref, oras.DefaultVerifyOptions)
	if err != nil {
		t.Fatalf("Verify() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Verify() root = %v, want %v", gotDesc, root)
	}
	if !reflect.DeepEqual(report.Node, root) {
		t.Errorf("Verify() report.Node = %v, want %v", report.Node, root)
	}
	if !report.OK() {
		t.Fatalf("Verify() report.OK() = false, want true; Failed = %v", report.Failed)
	}
	if got, want := len(report.Verified), len(descs); got != want {
		t.Errorf("len(report.Verified) = %v, want %v", got, want)
	}
}

// TestVerify_ReusesResolveRootFetch proves Verify shares resolveRoot's proxy
// with the graph walk: for a source implementing registry.ReferenceFetcher,
// the root manifest must be read exactly once, via FetchReference, and not
// fetched a second time by the walk that discovers its successors.
func TestVerify_ReusesResolveRootFetch(t *testing.T) {
	src := memory.New()

	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	generateManifest(descs[0], descs[1])                       // Blob 2

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}
	root := descs[2]
	ref := "quickstart"
	if err := src.Tag(ctx, root, ref); err != nil {
		t.Fatal("fail to tag root node", err)
	}

	fetcher := &mockReferenceFetcher{ReadOnlyTarget: src}
	gotDesc, report, err := oras.Verify(ctx, fetcher, ref, oras.DefaultVerifyOptions)
	if err != nil {
		t.Fatalf("Verify() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Verify() root = %v, want %v", gotDesc, root)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}

	if got, want := fetcher.fetchReference, int64(1); got != want {
		t.Errorf("count(FetchReference()) = %v, want %v", got, want)
	}
	// the root manifest must have been read only through FetchReference: a
	// fresh proxy inside the graph walk would fetch it again here.
	if got, want := fetcher.fetch, int64(len(blobs)-1); got != want {
		t.Errorf("count(Fetch()) = %v, want %v (the leaf nodes, not the root)", got, want)
	}
}

func TestVerify_VerifiedSetReuse(t *testing.T) {
	src := memory.New()

	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	generateManifest(descs[0], descs[1])                       // Blob 2

	ctx := context.Background()
	for i := range blobs {
		if err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i])); err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}
	root := descs[2]
	ref := "quickstart"
	if err := src.Tag(ctx, root, ref); err != nil {
		t.Fatal("fail to tag root node", err)
	}

	shared := oras.NewVerifiedSet()
	if _, _, err := oras.Verify(ctx, src, ref, oras.VerifyOptions{
		VerifyGraphOptions: oras.VerifyGraphOptions{KnownVerified: shared},
	}); err != nil {
		t.Fatalf("Verify() [populate] error = %v", err)
	}
	if got, want := shared.Len(), len(descs); got != want {
		t.Fatalf("shared.Len() = %v, want %v", got, want)
	}

	_, report, err := oras.Verify(ctx, src, ref, oras.VerifyOptions{
		VerifyGraphOptions: oras.VerifyGraphOptions{KnownVerified: shared},
	})
	if err != nil {
		t.Fatalf("Verify() [reuse] error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("report.OK() = false, want true; Failed = %v", report.Failed)
	}
	// the manifest must always be freshly re-read (it is the only way to
	// discover its children again); the leaf blobs it references, already
	// in the shared set, must be skipped instead.
	if got, want := len(report.Verified), 1; got != want {
		t.Errorf("len(report.Verified) = %v, want %v", got, want)
	}
	if got, want := len(report.Skipped), len(descs)-1; got != want {
		t.Errorf("len(report.Skipped) = %v, want %v", got, want)
	}
}

func TestVerify_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("nil source", func(t *testing.T) {
		_, _, err := oras.Verify(ctx, nil, "latest", oras.DefaultVerifyOptions)
		if err == nil {
			t.Fatal("Verify() error = nil, wantErr true")
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Verify() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})

	t.Run("resolve error", func(t *testing.T) {
		src := &resolver{ReadOnlyTarget: memory.New()}
		_, _, err := oras.Verify(ctx, src, "latest", oras.DefaultVerifyOptions)
		if err == nil {
			t.Fatal("Verify() error = nil, wantErr true")
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("Verify() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
		if !errors.Is(copyErr.Err, errResolve) {
			t.Errorf("Verify() error = %v, wantErr %v", copyErr.Err, errResolve)
		}
	})

	t.Run("nil source in VerifyGraph", func(t *testing.T) {
		_, err := oras.VerifyGraph(ctx, nil, ocispec.Descriptor{}, oras.DefaultVerifyGraphOptions)
		if err == nil {
			t.Fatal("VerifyGraph() error = nil, wantErr true")
		}
		copyErr, ok := err.(*oras.CopyError)
		if !ok {
			t.Fatalf("VerifyGraph() error is not a CopyError: %v", err)
		}
		if want := oras.CopyErrorOriginSource; copyErr.Origin != want {
			t.Errorf("CopyError origin = %v, want %v", copyErr.Origin, want)
		}
	})
}

func TestVerifyGraph_CorruptManifest(t *testing.T) {
	src := cas.NewMemory()
	ctx := context.Background()

	config := []byte("config")
	layer := []byte("foo")
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(config),
		Size:      int64(len(config)),
	}
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layer),
		Size:      int64(len(layer)),
	}
	manifestJSON, err := json.Marshal(ocispec.Manifest{
		Config: configDesc,
		Layers: []ocispec.Descriptor{layerDesc},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestJSON),
		Size:      int64(len(manifestJSON)),
	}
	pushes := []struct {
		desc ocispec.Descriptor
		blob []byte
	}{
		{configDesc, config},
		{layerDesc, layer},
		{root, manifestJSON},
	}
	for _, p := range pushes {
		if err := src.Push(ctx, p.desc, bytes.NewReader(p.blob)); err != nil {
			t.Fatalf("failed to push test content to src: %v", err)
		}
	}

	// the manifest is served with bytes that do not match its descriptor
	corrupt := &storage{Storage: src, digest: root.Digest}
	report, err := oras.VerifyGraph(ctx, corrupt, root, oras.DefaultVerifyGraphOptions)
	if err != nil {
		t.Fatalf("VerifyGraph() error = %v, wantErr %v", err, false)
	}
	if report.OK() {
		t.Fatal("VerifyGraph() report.OK() = true, want false")
	}
	if got, want := len(report.Failed), 1; got != want {
		t.Fatalf("len(report.Failed) = %v, want %v", got, want)
	}
	if got, want := report.Failed[0].Descriptor.Digest, root.Digest; got != want {
		t.Errorf("report.Failed[0].Descriptor.Digest = %v, want %v", got, want)
	}
	// the manifest could not be read, so its successors are not discovered
	if got := len(report.Verified); got != 0 {
		t.Errorf("len(report.Verified) = %v, want 0", got)
	}
}

func TestVerify_PartialReportOnAbort(t *testing.T) {
	src := memory.New()
	ctx := context.Background()

	config := []byte("config")
	layer := []byte("foo")
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(config),
		Size:      int64(len(config)),
	}
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layer),
		Size:      int64(len(layer)),
	}
	manifestJSON, err := json.Marshal(ocispec.Manifest{
		Config: configDesc,
		Layers: []ocispec.Descriptor{layerDesc},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestJSON),
		Size:      int64(len(manifestJSON)),
	}
	if err := src.Push(ctx, configDesc, bytes.NewReader(config)); err != nil {
		t.Fatal(err)
	}
	if err := src.Push(ctx, layerDesc, bytes.NewReader(layer)); err != nil {
		t.Fatal(err)
	}
	if err := src.Push(ctx, root, bytes.NewReader(manifestJSON)); err != nil {
		t.Fatal(err)
	}
	ref := "abort"
	if err := src.Tag(ctx, root, ref); err != nil {
		t.Fatal(err)
	}

	// abort the walk once the root manifest has been verified
	wantErr := errors.New("abort")
	opts := oras.VerifyOptions{
		VerifyGraphOptions: oras.VerifyGraphOptions{
			PostVerify: func(ctx context.Context, desc ocispec.Descriptor, verifyErr error) error {
				if desc.Digest != root.Digest {
					return wantErr
				}
				return nil
			},
		},
	}
	gotDesc, report, err := oras.Verify(ctx, src, ref, opts)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Verify() error = %v, want it to wrap %v", err, wantErr)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Verify() root = %v, want %v", gotDesc, root)
	}
	if report == nil {
		t.Fatal("Verify() report = nil, want the partial report")
	}
	if !reflect.DeepEqual(report.Node, root) {
		t.Errorf("report.Node = %v, want %v", report.Node, root)
	}
	// the root passed verification before the walk aborted; children whose
	// PostVerify aborted the walk are recorded too, so only require the root.
	found := false
	for _, d := range report.Verified {
		if d.Digest == root.Digest {
			found = true
		}
	}
	if !found {
		t.Errorf("report.Verified = %v, want it to contain the root %v", report.Verified, root)
	}
}

func TestVerifyGraph_ContextCanceledMidWalk(t *testing.T) {
	g := newReferrerGraph(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelFetch{ReadOnlyStorage: g.push(), cancel: cancel}

	report, err := oras.VerifyGraph(ctx, src, g.descs[3], oras.DefaultVerifyGraphOptions)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyGraph() error = %v, want %v", err, context.Canceled)
	}
	if report == nil {
		t.Fatal("VerifyGraph() report = nil, want the partial report")
	}
	for _, f := range report.Failed {
		if errors.Is(f.Err, context.Canceled) {
			t.Errorf("cancelled node %v recorded as Failed: %v", f.Descriptor.Digest, f.Err)
		}
	}
}
