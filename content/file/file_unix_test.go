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

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
)

// Related issue: https://github.com/oras-project/oras-go/issues/402
func TestStore_Dir_ExtractSymlinkRel(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	// create symlink to a relative path
	symlinkName := "test_symlink"
	symlinkPath := filepath.Join(dirPath, symlinkName)
	if err := os.Symlink(fileName, symlinkPath); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	// pack a manifest
	opts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{desc},
	}
	manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "test/dir", opts)
	if err != nil {
		t.Fatal("oras.PackManifest() error =", err)
	}

	// copy to another file store created from an absolute root, to trigger extracting directory
	tempDir = t.TempDir()
	dstAbs, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dstAbs.Close()
	if err := oras.CopyGraph(ctx, src, dstAbs, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	extractedSymlink := filepath.Join(tempDir, dirName, symlinkName)
	symlinkDst, err := os.Readlink(extractedSymlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := fileName; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err := os.ReadFile(extractedSymlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}

	// copy to another file store created from a relative root, to trigger extracting directory
	tempDir = t.TempDir()
	t.Chdir(tempDir)
	dstRel, err := New(".")
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dstRel.Close()
	if err := oras.CopyGraph(ctx, src, dstRel, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	extractedSymlink = filepath.Join(tempDir, dirName, symlinkName)
	symlinkDst, err = os.Readlink(extractedSymlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := fileName; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err = os.ReadFile(extractedSymlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}
}

// Related issue: https://github.com/oras-project/oras-go/issues/402
func TestStore_Dir_ExtractSymlinkAbs(t *testing.T) {
	// prepare test content
	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("error calling filepath.EvalSymlinks(), error =", err)
	}
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	// create symlink to an absolute path
	symlink := filepath.Join(dirPath, "test_symlink")
	if err := os.Symlink(filePath, symlink); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	// pack a manifest
	opts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{desc},
	}
	manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "test/dir", opts)
	if err != nil {
		t.Fatal("oras.PackManifest() error =", err)
	}

	// remove the original testing directory and create a new store using an absolute root
	if err := os.RemoveAll(dirPath); err != nil {
		t.Fatal("error calling RemoveAll(), error =", err)
	}
	dstAbs, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dstAbs.Close()
	if err := oras.CopyGraph(ctx, src, dstAbs, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	symlinkDst, err := os.Readlink(symlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := filePath; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err := os.ReadFile(symlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}

	// remove the original testing directory and create a new store using a relative path
	if err := os.RemoveAll(dirPath); err != nil {
		t.Fatal("error calling RemoveAll(), error =", err)
	}
	t.Chdir(tempDir)
	dstRel, err := New(".")
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dstRel.Close()
	if err := oras.CopyGraph(ctx, src, dstRel, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	symlinkDst, err = os.Readlink(symlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := filePath; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err = os.ReadFile(symlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}

	// copy to another file store created from an outside root, to trigger extracting directory
	tempDir = t.TempDir()
	dstOutside, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dstOutside.Close()
	if err := oras.CopyGraph(ctx, src, dstOutside, manifestDesc, oras.DefaultCopyGraphOptions); err == nil {
		t.Error("oras.CopyGraph() error = nil, wantErr ", true)
	}
}

// Related issue: https://github.com/oras-project/oras-go/issues/865
func TestStore_Dir_OverwriteSymlinkRel(t *testing.T) {
	// prepare test content
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	// create symlink to a relative path
	symlinkName := "test_symlink"
	symlinkPath := filepath.Join(dirPath, symlinkName)
	if err := os.Symlink(fileName, symlinkPath); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	// pack a manifest
	opts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{desc},
	}
	manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "test/dir", opts)
	if err != nil {
		t.Fatal("oras.PackManifest() error =", err)
	}

	// copy to another file store created from an absolute root, to trigger extracting directory
	tempDir = t.TempDir()
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dst.Close()
	if err := oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// copy to another file store created from the same root again, to test overwriting symlink
	sameDst, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer sameDst.Close()
	if err := oras.CopyGraph(ctx, src, sameDst, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	extractedSymlink := filepath.Join(tempDir, dirName, symlinkName)
	symlinkDst, err := os.Readlink(extractedSymlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := fileName; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err := os.ReadFile(extractedSymlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}
}

// Related issue: https://github.com/oras-project/oras-go/issues/865
func TestStore_Dir_OverwriteSymlinkAbs(t *testing.T) {
	// prepare test content
	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("error calling filepath.EvalSymlinks(), error =", err)
	}
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, content, 0777); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	// create symlink to an absolute path
	symlink := filepath.Join(dirPath, "test_symlink")
	if err := os.Symlink(filePath, symlink); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	// pack a manifest
	opts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{desc},
	}
	manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "test/dir", opts)
	if err != nil {
		t.Fatal("oras.PackManifest() error =", err)
	}

	// create a new store from the same root, to test overwriting symlink
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dst.Close()
	if err := oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// verify extracted symlink
	symlinkDst, err := os.Readlink(symlink)
	if err != nil {
		t.Fatal("failed to get symlink destination, error =", err)
	}
	if want := filePath; symlinkDst != want {
		t.Errorf("symlink destination = %v, want %v", symlinkDst, want)
	}
	got, err := os.ReadFile(symlink)
	if err != nil {
		t.Fatal("failed to read symlink file, error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("symlink content = %v, want %v", got, content)
	}
}

func TestStore_Dir_OverwriteSymlink_RemovalFailed(t *testing.T) {
	// prepare test content
	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("error calling filepath.EvalSymlinks(), error =", err)
	}
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, content, 0666); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	// create symlink to an absolute path
	symlink := filepath.Join(dirPath, "test_symlink")
	if err := os.Symlink(filePath, symlink); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}
	// remove the link target
	if err := os.Remove(filePath); err != nil {
		t.Fatal("error calling Remove(), error =", err)
	}

	// new store based on the existing dir
	src, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	// pack a manifest
	opts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{desc},
	}
	manifestDesc, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "test/dir", opts)
	if err != nil {
		t.Fatal("oras.PackManifest() error =", err)
	}

	// create a new store from the same root, to test overwriting symlink
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer dst.Close()

	// disallow write on the directory so that the removal will fail
	if err := os.Chmod(dirPath, 0555); err != nil {
		t.Fatal("error calling Chmod(), error =", err)
	}
	if err := oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions); err == nil {
		t.Error("oras.CopyGraph() error = nil, wantErr = true")
	}
	// chmod back so that the directory can be cleaned up
	if err := os.Chmod(dirPath, 0777); err != nil {
		t.Fatal("error calling Chmod(), error =", err)
	}
}

// TestStore_Dir_SymlinkChainEscape reproduces GHSA-m37j-52j7-pjw7: a malicious
// unpack layer plants a chain of symlinks whose lexical target stays inside the
// extraction root but whose kernel-resolved target is an absolute path outside
// it, then writes a regular file with the same name to write through the link.
// The write must be contained inside the store's working directory.
func TestStore_Dir_SymlinkChainEscape(t *testing.T) {
	workDir := t.TempDir()
	// attacker's target lives outside the store working directory
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "PWNED")

	const title = "out"
	baseAbs := filepath.Join(workDir, title)
	depth := len(strings.Split(strings.Trim(filepath.ToSlash(baseAbs), "/"), "/"))

	// build the malicious tar.gz
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	dirs := make([]string, depth)
	for i := range dirs {
		dirs[i] = "d"
	}
	for i := 1; i <= depth; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     title + "/" + strings.Join(dirs[:i], "/"),
			Mode:     0o755,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// "up" symlink at the bottom, resolving back to baseAbs
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     title + "/" + strings.Join(dirs, "/") + "/up",
		Linkname: strings.Repeat("../", depth-1) + "..",
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	// "escape" symlink whose lexical target stays in-bounds but whose
	// kernel-resolved target is outsidePath
	escapeTarget := strings.Join(dirs, "/") + "/up/" + strings.Repeat("../", depth-1) + ".." + outsidePath
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     title + "/escape",
		Linkname: escapeTarget,
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	// regular file with the same name, written through the symlink
	payload := []byte("PWNED-BY-ORAS-TARSLIP")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     title + "/escape",
		Mode:     0o644,
		Size:     int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	blob := buf.Bytes()

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: title,
			AnnotationUnpack:        "true",
		},
	}

	s, err := New(workDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer s.Close()
	if s.AllowPathTraversalOnWrite {
		t.Fatal("expected AllowPathTraversalOnWrite to default to false")
	}

	// Push may succeed (write contained inside the store) or fail, but under no
	// circumstances may it write outside the working directory.
	_ = s.Push(context.Background(), desc, bytes.NewReader(blob))

	if _, err := os.Stat(outsidePath); err == nil {
		t.Fatalf("path traversal: file written outside working dir at %s", outsidePath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error stat-ing %s: %v", outsidePath, err)
	}
}

// plantEscapeSymlinkLayer returns an unpack layer titled title whose tar
// plants title+"/escape" as a symlink to outsidePath. The symlink target
// stays inside title lexically, but resolves through a chain of "up"
// symlinks to outsidePath. extra entries are appended to the tar.
func plantEscapeSymlinkLayer(t *testing.T, workDir, title, outsidePath string, extra ...*tar.Header) ([]byte, ocispec.Descriptor) {
	t.Helper()
	baseAbs := filepath.Join(workDir, title)
	depth := len(strings.Split(strings.Trim(filepath.ToSlash(baseAbs), "/"), "/"))
	dirs := make([]string, depth)
	for i := range dirs {
		dirs[i] = "d"
	}

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for i := 1; i <= depth; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     title + "/" + strings.Join(dirs[:i], "/"),
			Mode:     0o755,
		}); err != nil {
			t.Fatal(err)
		}
	}
	headers := []*tar.Header{
		{
			// "up" at the bottom of the chain resolves back to baseAbs
			Typeflag: tar.TypeSymlink,
			Name:     title + "/" + strings.Join(dirs, "/") + "/up",
			Linkname: strings.Repeat("../", depth-1) + "..",
			Mode:     0o777,
		},
		{
			Typeflag: tar.TypeSymlink,
			Name:     title + "/escape",
			Linkname: strings.Join(dirs, "/") + "/up/" + strings.Repeat("../", depth-1) + ".." + outsidePath,
			Mode:     0o777,
		},
	}
	for _, h := range append(headers, extra...) {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	blob := buf.Bytes()
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: title,
			AnnotationUnpack:        "true",
		},
	}
	return blob, desc
}

// assertPlantedSymlink fails the test unless path is a symlink resolving to
// want, i.e. unless the exploit precondition was actually established.
func assertPlantedSymlink(t *testing.T, path, want string) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink; exploit precondition not met", path)
	}
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", path, err)
	}
	wantReal, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", want, err)
	}
	if got != wantReal {
		t.Fatalf("%s resolves to %s, want %s", path, got, wantReal)
	}
}

// Pushing a named blob whose title is a symlink planted by an earlier unpacked
// layer must not write through that symlink. (GHSA-2hhj-vj64-hx3j)
func TestStore_File_PushThroughPlantedSymlink(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "victim")
	original := []byte("original content")
	if err := os.WriteFile(outsidePath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := New(workDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer s.Close()
	if s.AllowPathTraversalOnWrite {
		t.Fatal("expected AllowPathTraversalOnWrite to default to false")
	}
	ctx := context.Background()

	// push 1: the unpack layer plants out/escape -> outsidePath
	layer, layerDesc := plantEscapeSymlinkLayer(t, workDir, "out", outsidePath)
	if err := s.Push(ctx, layerDesc, bytes.NewReader(layer)); err != nil {
		t.Fatal("Store.Push(layer) error =", err)
	}
	escapePath := filepath.Join(workDir, "out", "escape")
	assertPlantedSymlink(t, escapePath, outsidePath)

	// push 2: a plain blob titled with the planted symlink path
	payload := []byte("PWNED-BY-ORAS-PUSHFILE")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "out/escape",
		},
	}
	if err := s.Push(ctx, blobDesc, bytes.NewReader(payload)); err != nil {
		t.Fatal("Store.Push(blob) error =", err)
	}

	got, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("path traversal: %s was overwritten with %q", outsidePath, got)
	}
	fi, err := os.Lstat(escapePath)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want regular file", escapePath, fi.Mode())
	}
	got, err = os.ReadFile(escapePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("%s content = %q, want %q", escapePath, got, payload)
	}
}

// Pushing an unpack layer whose title is a symlink planted by an earlier
// unpacked layer must not extract into the symlink's target directory.
// (GHSA-2hhj-vj64-hx3j)
func TestStore_Dir_PushThroughPlantedSymlink(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()

	s, err := New(workDir)
	if err != nil {
		t.Fatal("Store.New() error =", err)
	}
	defer s.Close()
	ctx := context.Background()

	// push 1: the unpack layer plants out/escape -> outsideDir
	layer, layerDesc := plantEscapeSymlinkLayer(t, workDir, "out", outsideDir)
	if err := s.Push(ctx, layerDesc, bytes.NewReader(layer)); err != nil {
		t.Fatal("Store.Push(layer) error =", err)
	}
	escapePath := filepath.Join(workDir, "out", "escape")
	assertPlantedSymlink(t, escapePath, outsideDir)

	// push 2: an unpack layer titled with the planted symlink path
	payload := []byte("PWNED-BY-ORAS-PUSHDIR")
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "out/escape/pwned",
		Mode:     0o644,
		Size:     int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	blob := buf.Bytes()
	dirDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "out/escape",
			AnnotationUnpack:        "true",
		},
	}
	if err := s.Push(ctx, dirDesc, bytes.NewReader(blob)); err != nil {
		t.Fatal("Store.Push(dir) error =", err)
	}

	if _, err := os.Lstat(filepath.Join(outsideDir, "pwned")); err == nil {
		t.Fatalf("path traversal: file written outside working dir in %s", outsideDir)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	fi, err := os.Lstat(escapePath)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s mode = %v, want directory", escapePath, fi.Mode())
	}
	got, err := os.ReadFile(filepath.Join(escapePath, "pwned"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("extracted content = %q, want %q", got, payload)
	}
}

// createNoFollow must refuse a symlink at the final path component, e.g. one
// planted between removeSymlink and the open. (GHSA-2hhj-vj64-hx3j)
func Test_createNoFollow_Symlink(t *testing.T) {
	dir := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "victim")
	original := []byte("original content")
	if err := os.WriteFile(outsidePath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatal(err)
	}

	if fp, err := createNoFollow(linkPath, 0o666); err == nil {
		fp.Close()
		t.Fatal("createNoFollow() error = nil, want error")
	}
	got, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func Test_removeSymlink(t *testing.T) {
	dir := t.TempDir()

	// non-existent path
	if err := removeSymlink(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("removeSymlink(missing) error = %v", err)
	}

	// regular file is kept
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeSymlink(regular); err != nil {
		t.Errorf("removeSymlink(regular) error = %v", err)
	}
	if _, err := os.Lstat(regular); err != nil {
		t.Errorf("regular file removed: %v", err)
	}

	// symlink is removed, its target is kept
	link := filepath.Join(dir, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if err := removeSymlink(link); err != nil {
		t.Errorf("removeSymlink(link) error = %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("symlink not removed: %v", err)
	}
	if _, err := os.Lstat(regular); err != nil {
		t.Errorf("symlink target removed: %v", err)
	}

	// failure to remove is reported
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roLink := filepath.Join(roDir, "link")
	if err := os.Symlink(regular, roLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(roDir, 0o755)
	if os.Geteuid() != 0 {
		if err := removeSymlink(roLink); err == nil {
			t.Error("removeSymlink(roLink) error = nil, want error")
		}
	}
}
