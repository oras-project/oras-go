//go:build !windows

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

package file

// The store creates the extraction root of an unpack layer before it reads the
// layer. While that directory is still empty, a symlink entry whose path is
// the root, in the same layer or in another layer whose tree contains the
// root, could replace it, and the rest of the layer was then written wherever
// the link resolved.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// unpackLayer is a gzipped tar blob with the descriptor of an unpack layer.
type unpackLayer struct {
	desc ocispec.Descriptor
	blob []byte
}

// newUnpackLayer builds a layer that the file store unpacks into the
// directory named title.
func newUnpackLayer(t *testing.T, title string, headers ...*tar.Header) unpackLayer {
	t.Helper()

	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte{'x'}, int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var blob bytes.Buffer
	zw := gzip.NewWriter(&blob)
	if _, err := zw.Write(tarball.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return unpackLayer{
		desc: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayerGzip,
			Digest:    digest.FromBytes(blob.Bytes()),
			Size:      int64(blob.Len()),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: title,
				AnnotationUnpack:        "true",
				AnnotationDigest:        digest.FromBytes(tarball.Bytes()).String(),
			},
		},
		blob: blob.Bytes(),
	}
}

func symlinkHeader(name, target string) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0o777}
}

func regularHeader(name string, size int64) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: size}
}

// rootReplacingLayers returns the two layers of the first form. The helper
// layer, titled "a", plants a/s -> ".". The payload layer, titled title, names
// its root as its first entry, a symlink to rootTarget: as text the target is
// the root itself, but the kernel resolves it through a/s elsewhere.
func rootReplacingLayers(t *testing.T, title, rootTarget string, padding int64) (helper, payload unpackLayer) {
	t.Helper()
	helper = newUnpackLayer(t, "a", symlinkHeader("a/s", "."))
	headers := []*tar.Header{
		symlinkHeader(title, rootTarget),
		regularHeader(title+"/created.txt", 8),
		regularHeader(title+"/existing.txt", 8),
	}
	if padding > 0 {
		headers = append(headers, regularHeader(title+"/padding.bin", padding))
	}
	return helper, newUnpackLayer(t, title, headers...)
}

// overlappingLayers returns the two layers of the second form. The outer
// layer, titled "a", plants a/s -> "." and a/b, a symlink that is a/b as text
// and two levels above the working directory for the kernel. The inner layer
// is titled "a/b" and holds regular files only.
func overlappingLayers(t *testing.T, outerRepeats int, padding int64) (outer, inner unpackLayer) {
	t.Helper()
	headers := []*tar.Header{symlinkHeader("a/s", ".")}
	for range max(outerRepeats, 1) {
		headers = append(headers, symlinkHeader("a/b", "s/s/s/../../../b"))
	}
	outer = newUnpackLayer(t, "a", headers...)
	innerHeaders := []*tar.Header{regularHeader("a/b/created.txt", 8)}
	if padding > 0 {
		innerHeaders = append(innerHeaders, regularHeader("a/b/padding.bin", padding))
	}
	return outer, newUnpackLayer(t, "a/b", innerHeaders...)
}

// sandbox returns a working directory and the directory named name two
// levels above it, which the layers target.
func sandbox(t *testing.T, name string) (workingDir, outside string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workingDir = filepath.Join(dir, "x", "y", "store")
	outside = filepath.Join(dir, "x", name)
	for _, d := range []string{workingDir, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "existing.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	return workingDir, outside
}

// assertOutsideUntouched fails the test if a layer created or changed a file
// in outside.
func assertOutsideUntouched(t *testing.T, outside string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "existing.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("files outside the working directory = %v, want [existing.txt]", names)
	}
	got, err := os.ReadFile(filepath.Join(outside, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("existing.txt outside the working directory = %q, want %q", got, "original")
	}
}

func newTestStore(t *testing.T, workingDir string) *Store {
	t.Helper()
	s, err := New(workingDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_Dir_RootReplacedBySymlink(t *testing.T) {
	roots := []string{"d", "d/", "./d", "d/.", "d/x/.."}
	for _, rootEntry := range roots {
		t.Run(rootEntry, func(t *testing.T) {
			workingDir, outside := sandbox(t, "d")
			s := newTestStore(t, workingDir)

			helper := newUnpackLayer(t, "a", symlinkHeader("a/s", "."))
			payload := newUnpackLayer(t, "d",
				symlinkHeader(rootEntry, "a/s/s/../../../d"),
				regularHeader("d/created.txt", 8),
				regularHeader("d/existing.txt", 8),
			)
			if err := s.Push(t.Context(), helper.desc, bytes.NewReader(helper.blob)); err != nil {
				t.Fatalf("Push(helper) error = %v", err)
			}
			if err := s.Push(t.Context(), payload.desc, bytes.NewReader(payload.blob)); err == nil {
				t.Error("Push(payload) error = nil, want an error")
			}
			assertOutsideUntouched(t, outside)

			fi, err := os.Lstat(filepath.Join(workingDir, "d"))
			if err != nil {
				t.Fatal(err)
			}
			if !fi.IsDir() {
				t.Errorf("extraction root mode = %v, want a directory", fi.Mode())
			}
		})
	}
}

// A directory entry for the root before the symlink entry does not help.
func TestStore_Dir_RootReplacedBySymlink_AfterDirEntry(t *testing.T) {
	workingDir, outside := sandbox(t, "d")
	s := newTestStore(t, workingDir)

	helper := newUnpackLayer(t, "a", symlinkHeader("a/s", "."))
	payload := newUnpackLayer(t, "d",
		&tar.Header{Typeflag: tar.TypeDir, Name: "d/", Mode: 0o755},
		symlinkHeader("d", "a/s/s/../../../d"),
		regularHeader("d/created.txt", 8),
	)
	if err := s.Push(t.Context(), helper.desc, bytes.NewReader(helper.blob)); err != nil {
		t.Fatalf("Push(helper) error = %v", err)
	}
	if err := s.Push(t.Context(), payload.desc, bytes.NewReader(payload.blob)); err == nil {
		t.Error("Push(payload) error = nil, want an error")
	}
	assertOutsideUntouched(t, outside)
}

// The symlink can name any existing directory by its absolute path: ".." does
// not go above "/", so a long enough chain ends there.
func TestStore_Dir_RootReplacedBySymlink_AbsoluteTarget(t *testing.T) {
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "existing.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	const hops = 24
	title := strings.TrimPrefix(filepath.ToSlash(outside), "/")
	target := strings.Repeat("../", strings.Count(title, "/")) +
		"a/" + strings.Repeat("s/", hops) +
		strings.Repeat("../", hops+1) +
		title

	s := newTestStore(t, workingDir)
	helper, payload := rootReplacingLayers(t, title, target, 0)
	if err := s.Push(t.Context(), helper.desc, bytes.NewReader(helper.blob)); err != nil {
		t.Fatalf("Push(helper) error = %v", err)
	}
	if err := s.Push(t.Context(), payload.desc, bytes.NewReader(payload.blob)); err == nil {
		t.Error("Push(payload) error = nil, want an error")
	}
	assertOutsideUntouched(t, outside)
}

// A pull that loses the race leaves a/s behind. Pulling the same artifact
// again into the same working directory must not write outside either.
func TestStore_Dir_RootReplacedBySymlink_SecondPull(t *testing.T) {
	workingDir, outside := sandbox(t, "d")
	helper, payload := rootReplacingLayers(t, "d", "a/s/s/../../../d", 0)
	for range 2 {
		s, err := New(workingDir)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Push(t.Context(), payload.desc, bytes.NewReader(payload.blob))
		_ = s.Push(t.Context(), helper.desc, bytes.NewReader(helper.blob))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	assertOutsideUntouched(t, outside)
}

// hookedReader calls hook once, before the first Read.
type hookedReader struct {
	r    io.Reader
	once sync.Once
	hook func()
}

func (h *hookedReader) Read(p []byte) (int, error) {
	h.once.Do(h.hook)
	return h.r.Read(p)
}

// The second form, with the outer layer unpacked after the store has created
// the inner layer's root and before it reads the inner layer.
func TestStore_Dir_RootReplacedByAnotherLayer(t *testing.T) {
	workingDir, outside := sandbox(t, "b")
	s := newTestStore(t, workingDir)

	outer, inner := overlappingLayers(t, 1, 0)
	content := &hookedReader{r: bytes.NewReader(inner.blob), hook: func() {
		if err := s.Push(t.Context(), outer.desc, bytes.NewReader(outer.blob)); err == nil {
			t.Error("Push(outer) error = nil, want an error")
		}
	}}
	if err := s.Push(t.Context(), inner.desc, content); err != nil {
		t.Errorf("Push(inner) error = %v", err)
	}
	assertOutsideUntouched(t, outside)

	got, err := os.ReadFile(filepath.Join(workingDir, "a", "b", "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "xxxxxxxx"; string(got) != want {
		t.Errorf("a/b/created.txt = %q, want %q", got, want)
	}
}

// The second form with the outer layer pushed first: the link is dangling
// while the outer layer is unpacked, and the inner layer must replace it with
// a directory instead of extracting through it.
func TestStore_Dir_RootReplacedByAnotherLayer_OuterFirst(t *testing.T) {
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, workingDir)

	outer, inner := overlappingLayers(t, 1, 0)
	_ = s.Push(t.Context(), outer.desc, bytes.NewReader(outer.blob))
	if err := s.Push(t.Context(), inner.desc, bytes.NewReader(inner.blob)); err != nil {
		t.Fatalf("Push(inner) error = %v", err)
	}
	fi, err := os.Lstat(filepath.Join(workingDir, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Errorf("a/b mode = %v, want a directory", fi.Mode())
	}
}

// pullLayers copies an artifact made of layers into a file store with the
// default copy options, as a pull does.
func pullLayers(t *testing.T, workingDir string, concurrency int, layers ...unpackLayer) {
	t.Helper()
	ctx := t.Context()
	src := memory.New()
	descs := make([]ocispec.Descriptor, 0, len(layers))
	for _, l := range layers {
		if err := src.Push(ctx, l.desc, bytes.NewReader(l.blob)); err != nil {
			t.Fatal(err)
		}
		descs = append(descs, l.desc)
	}
	manifest, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/vnd.test",
		oras.PackManifestOptions{Layers: descs})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Tag(ctx, manifest, "test"); err != nil {
		t.Fatal(err)
	}
	dst, err := New(workingDir)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	opts := oras.DefaultCopyOptions
	opts.Concurrency = concurrency
	_, _ = oras.Copy(ctx, src, "test", dst, "test", opts)
}

func TestCopy_RootReplacedBySymlink(t *testing.T) {
	for _, tt := range []struct {
		name        string
		concurrency int
		padding     int64
		payload     bool // list the payload first
	}{
		{name: "default concurrency", padding: 1 << 20, payload: true},
		{name: "sequential", concurrency: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for range 10 {
				workingDir, outside := sandbox(t, "d")
				helper, payload := rootReplacingLayers(t, "d", "a/s/s/../../../d", tt.padding)
				layers := []unpackLayer{helper, payload}
				if tt.payload {
					layers = []unpackLayer{payload, helper}
				}
				pullLayers(t, workingDir, tt.concurrency, layers...)
				assertOutsideUntouched(t, outside)
			}
		})
	}
}

func TestCopy_RootReplacedByAnotherLayer(t *testing.T) {
	for range 10 {
		workingDir, outside := sandbox(t, "b")
		outer, inner := overlappingLayers(t, 1, 1<<20)
		pullLayers(t, workingDir, 0, outer, inner)
		assertOutsideUntouched(t, outside)
	}
}

// A fix that only narrows the window in which a root can be replaced would
// pass the tests above. This test pushes the two layers of the second form at
// the same time, with the outer layer rewriting a/b many times, so that the
// inner push creates its root at a random point while the outer layer is
// being unpacked.
func TestStore_Dir_RootReplacedByAnotherLayer_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	outer, inner := overlappingLayers(t, 500, 0)

	// time the outer layer alone
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_ = s.Push(t.Context(), outer.desc, bytes.NewReader(outer.blob))
	outerTime := max(time.Since(began), time.Millisecond)
	_ = s.Close()

	for range 30 {
		workingDir, outside := sandbox(t, "b")
		s, err := New(workingDir)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() {
			_ = s.Push(t.Context(), outer.desc, bytes.NewReader(outer.blob))
		})
		wg.Go(func() {
			time.Sleep(rand.N(outerTime))
			_ = s.Push(t.Context(), inner.desc, bytes.NewReader(inner.blob))
		})
		wg.Wait()
		_ = s.Close()
		assertOutsideUntouched(t, outside)
	}
}

// Legitimate symlinks, relative, absolute and dangling, are still extracted.
func TestStore_Dir_SymlinksInsideRoot(t *testing.T) {
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, workingDir)
	layer := newUnpackLayer(t, "app",
		&tar.Header{Typeflag: tar.TypeDir, Name: "app/", Mode: 0o755},
		&tar.Header{Typeflag: tar.TypeDir, Name: "app/v1/", Mode: 0o755},
		regularHeader("app/v1/data.txt", 4),
		symlinkHeader("app/current", "v1"),
		symlinkHeader("app/abs", filepath.Join(workingDir, "app", "v1", "data.txt")),
		symlinkHeader("app/dangling", "v2/data.txt"),
	)
	if err := s.Push(t.Context(), layer.desc, bytes.NewReader(layer.blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	for _, name := range []string{"current/data.txt", "abs"} {
		got, err := os.ReadFile(filepath.Join(workingDir, "app", name))
		if err != nil {
			t.Errorf("ReadFile(%s) error = %v", name, err)
			continue
		}
		if string(got) != "xxxx" {
			t.Errorf("%s = %q, want %q", name, got, "xxxx")
		}
	}
	if _, err := os.Lstat(filepath.Join(workingDir, "app", "dangling")); err != nil {
		t.Errorf("Lstat(dangling) error = %v", err)
	}
}
