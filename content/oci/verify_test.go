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

package oci

import (
	"context"
	"crypto/sha1"
	_ "crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
)

func blobFilePath(blob []byte) string {
	dgst := digest.FromBytes(blob)
	return path.Join(ocispec.ImageBlobsDir, dgst.Algorithm().String(), dgst.Encoded())
}

func entryPaths(entries []VerifyEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func mustContainPath(t *testing.T, entries []VerifyEntry, p string) VerifyEntry {
	t.Helper()
	for _, e := range entries {
		if e.Path == p {
			return e
		}
	}
	t.Fatalf("path %q not found in %v", p, entryPaths(entries))
	return VerifyEntry{}
}

func TestVerify_ValidBlob_MapFS(t *testing.T) {
	blob := []byte("valid-blob")
	p := blobFilePath(blob)
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Data: blob},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Verified) != 1 {
		t.Fatalf("Verified = %v, want 1 entry", entryPaths(report.Verified))
	}
	got := mustContainPath(t, report.Verified, p)
	if got.Reason != nil {
		t.Errorf("Verified reason = %v, want nil", got.Reason)
	}
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %v, want none", entryPaths(report.Failed))
	}
	if len(report.Unverifiable) != 0 {
		t.Errorf("Unverifiable = %v, want none", entryPaths(report.Unverifiable))
	}
}

func TestVerify_CorruptedBlob_MapFS(t *testing.T) {
	blob := []byte("expected-content")
	p := blobFilePath(blob)
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Data: []byte("corrupted-content")},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{Concurrency: 1})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Verified) != 0 {
		t.Errorf("Verified = %v, want none", entryPaths(report.Verified))
	}
	got := mustContainPath(t, report.Failed, p)
	if !errors.Is(got.Reason, content.ErrMismatchedDigest) {
		t.Errorf("Failed reason = %v, want %v", got.Reason, content.ErrMismatchedDigest)
	}
}

func TestVerify_TruncatedBlob_MapFS(t *testing.T) {
	blob := []byte("0123456789abcdef")
	p := blobFilePath(blob)
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Data: blob[:4]},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Failed, p)
	if !errors.Is(got.Reason, content.ErrMismatchedDigest) {
		t.Errorf("Failed reason = %v, want %v", got.Reason, content.ErrMismatchedDigest)
	}
}

func TestVerify_UnsupportedAlgorithmDir_MapFS(t *testing.T) {
	blob := []byte("sha1-blob")
	h := sha1.Sum(blob)
	dgst := digest.NewDigestFromBytes("sha1", h[:])
	p := path.Join(ocispec.ImageBlobsDir, dgst.Algorithm().String(), dgst.Encoded())
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Data: blob},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Unverifiable, p)
	if !errors.Is(got.Reason, errdef.ErrUnsupported) {
		t.Errorf("Unverifiable reason = %v, want %v", got.Reason, errdef.ErrUnsupported)
	}
	if len(report.Verified) != 0 || len(report.Failed) != 0 {
		t.Errorf("unexpected Verified=%v Failed=%v", entryPaths(report.Verified), entryPaths(report.Failed))
	}
}

func TestVerify_NonDigestFilename_MapFS(t *testing.T) {
	p := path.Join(ocispec.ImageBlobsDir, "sha256", "not-a-digest")
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Data: []byte("whatever")},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Unverifiable, p)
	if got.Reason == nil {
		t.Error("Unverifiable reason = nil, want invalid digest")
	}
}

func TestVerify_StrayDirectoryUnderAlg_MapFS(t *testing.T) {
	p := path.Join(ocispec.ImageBlobsDir, "sha256", "straydir")
	fsys := fstest.MapFS{
		p: &fstest.MapFile{Mode: fs.ModeDir},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Unverifiable, p)
	if got.Reason == nil {
		t.Error("Unverifiable reason = nil, want directory reason")
	}
}

func TestVerify_ResumeNextAndStrayFile(t *testing.T) {
	valid := []byte("keep-going")
	validPath := blobFilePath(valid)
	corrupt := []byte("original")
	corruptPath := blobFilePath(corrupt)
	stray := path.Join(ocispec.ImageBlobsDir, "not-an-algorithm")
	fsys := fstest.MapFS{
		validPath:   &fstest.MapFile{Data: valid},
		corruptPath: &fstest.MapFile{Data: []byte("changed")},
		stray:       &fstest.MapFile{Data: []byte("file-at-blobs-root")},
	}

	report, err := Verify(context.Background(), fsys, VerifyOptions{Concurrency: 2})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	mustContainPath(t, report.Verified, validPath)
	mustContainPath(t, report.Failed, corruptPath)
	mustContainPath(t, report.Unverifiable, stray)
}

func TestVerify_ContextCanceledBeforeStart(t *testing.T) {
	blob := []byte("valid-blob")
	fsys := fstest.MapFS{
		blobFilePath(blob): &fstest.MapFile{Data: blob},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := Verify(ctx, fsys, VerifyOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() error = %v, want %v", err, context.Canceled)
	}
	if report != nil {
		t.Errorf("report = %#v, want nil", report)
	}
}

func TestVerify_ContextCanceledDuringHash(t *testing.T) {
	blob := []byte("valid-blob")
	p := blobFilePath(blob)
	inner := fstest.MapFS{
		p: &fstest.MapFile{Data: blob},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	fsys := &stallOpenFS{
		FS:      inner,
		ctx:     ctx,
		started: started,
	}

	errCh := make(chan error, 1)
	var report *VerifyReport
	go func() {
		var err error
		report, err = Verify(ctx, fsys, VerifyOptions{Concurrency: 1})
		errCh <- err
	}()

	select {
	case <-started:
	case err := <-errCh:
		t.Fatal("Verify() returned before stalling:", err)
	}
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() error = %v, want %v", err, context.Canceled)
	}
	if report == nil {
		t.Fatal("report = nil, want partial report")
	}
}

func TestVerify_MissingBlobsDir(t *testing.T) {
	fsys := fstest.MapFS{}
	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err == nil {
		t.Fatal("Verify() error = nil, want unreadable blobs/")
	}
	if report != nil {
		t.Errorf("report = %#v, want nil", report)
	}
}

func TestVerify_UnreadableBlobFile(t *testing.T) {
	blob := []byte("cannot-read")
	p := blobFilePath(blob)
	inner := fstest.MapFS{
		p: &fstest.MapFile{Data: blob},
	}
	fsys := failOpenFS{FS: inner, fail: p}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Failed, p)
	if !errors.Is(got.Reason, os.ErrPermission) {
		t.Errorf("Failed reason = %v, want %v", got.Reason, os.ErrPermission)
	}
}

func TestVerify_UnreadableAlgorithmDir(t *testing.T) {
	blob := []byte("ok")
	validPath := blobFilePath(blob)
	algPath := path.Join(ocispec.ImageBlobsDir, "sha256")
	inner := fstest.MapFS{
		validPath: &fstest.MapFile{Data: blob},
	}
	fsys := failReadDirFS{FS: inner, fail: algPath}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	got := mustContainPath(t, report.Failed, algPath)
	if got.Reason == nil {
		t.Error("Failed reason = nil, want read error")
	}
}

func TestVerify_EmptyBlobsDir(t *testing.T) {
	fsys := fstest.MapFS{
		ocispec.ImageBlobsDir: &fstest.MapFile{Mode: fs.ModeDir},
	}
	report, err := Verify(context.Background(), fsys, VerifyOptions{Concurrency: 0})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Verified) != 0 || len(report.Failed) != 0 || len(report.Unverifiable) != 0 {
		t.Errorf("report = %+v, want empty", report)
	}
}

func TestVerify_TempDir_ReadOnly(t *testing.T) {
	root := t.TempDir()
	valid := []byte("temp-valid")
	validDigest := digest.FromBytes(valid)
	validPath := filepath.Join(root, ocispec.ImageBlobsDir, validDigest.Algorithm().String(), validDigest.Encoded())
	if err := os.MkdirAll(filepath.Dir(validPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, valid, 0o444); err != nil {
		t.Fatal(err)
	}

	corrupt := []byte("temp-original")
	corruptDigest := digest.FromBytes(corrupt)
	corruptPath := filepath.Join(root, ocispec.ImageBlobsDir, corruptDigest.Algorithm().String(), corruptDigest.Encoded())
	if err := os.WriteFile(corruptPath, []byte("temp-changed"), 0o444); err != nil {
		t.Fatal(err)
	}

	truncated := []byte("0123456789")
	truncatedDigest := digest.FromBytes(truncated)
	truncatedPath := filepath.Join(root, ocispec.ImageBlobsDir, truncatedDigest.Algorithm().String(), truncatedDigest.Encoded())
	if err := os.WriteFile(truncatedPath, truncated[:3], 0o444); err != nil {
		t.Fatal(err)
	}

	unsupportedDir := filepath.Join(root, ocispec.ImageBlobsDir, "sha1")
	if err := os.MkdirAll(unsupportedDir, 0o777); err != nil {
		t.Fatal(err)
	}
	unsupportedName := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	unsupportedPath := filepath.Join(unsupportedDir, unsupportedName)
	if err := os.WriteFile(unsupportedPath, []byte("sha1"), 0o444); err != nil {
		t.Fatal(err)
	}

	nondigestPath := filepath.Join(root, ocispec.ImageBlobsDir, "sha256", "not-a-digest")
	if err := os.WriteFile(nondigestPath, []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}

	strayDir := filepath.Join(root, ocispec.ImageBlobsDir, "sha256", "straydir")
	if err := os.Mkdir(strayDir, 0o777); err != nil {
		t.Fatal(err)
	}

	before := snapshotDir(t, root)
	report, err := Verify(context.Background(), os.DirFS(root), VerifyOptions{Concurrency: -1})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	after := snapshotDir(t, root)
	if len(before) != len(after) {
		t.Fatalf("layout mutated: before %d entries, after %d", len(before), len(after))
	}
	for p, meta := range before {
		got, ok := after[p]
		if !ok {
			t.Fatalf("path %s was removed", p)
		}
		if got != meta {
			t.Errorf("path %s mutated: before %#v after %#v", p, meta, got)
		}
	}

	slashValid := path.Join(ocispec.ImageBlobsDir, validDigest.Algorithm().String(), validDigest.Encoded())
	slashCorrupt := path.Join(ocispec.ImageBlobsDir, corruptDigest.Algorithm().String(), corruptDigest.Encoded())
	slashTruncated := path.Join(ocispec.ImageBlobsDir, truncatedDigest.Algorithm().String(), truncatedDigest.Encoded())
	slashUnsupported := path.Join(ocispec.ImageBlobsDir, "sha1", unsupportedName)
	slashNondigest := path.Join(ocispec.ImageBlobsDir, "sha256", "not-a-digest")
	slashStray := path.Join(ocispec.ImageBlobsDir, "sha256", "straydir")

	mustContainPath(t, report.Verified, slashValid)
	mustContainPath(t, report.Failed, slashCorrupt)
	mustContainPath(t, report.Failed, slashTruncated)
	mustContainPath(t, report.Unverifiable, slashUnsupported)
	mustContainPath(t, report.Unverifiable, slashNondigest)
	mustContainPath(t, report.Unverifiable, slashStray)
}

type fileMeta struct {
	size    int64
	mode    fs.FileMode
	modTime int64
	isDir   bool
}

func snapshotDir(t *testing.T, root string) map[string]fileMeta {
	t.Helper()
	out := make(map[string]fileMeta)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fileMeta{
			size:    info.Size(),
			mode:    info.Mode(),
			modTime: info.ModTime().UnixNano(),
			isDir:   d.IsDir(),
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type failOpenFS struct {
	fs.FS
	fail string
}

func (f failOpenFS) Open(name string) (fs.File, error) {
	if name == f.fail {
		return nil, os.ErrPermission
	}
	return f.FS.Open(name)
}

type failReadDirFS struct {
	fs.FS
	fail string
}

func (f failReadDirFS) Open(name string) (fs.File, error) {
	return f.FS.Open(name)
}

func (f failReadDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.fail {
		return nil, os.ErrPermission
	}
	return fs.ReadDir(f.FS, name)
}

type stallOpenFS struct {
	fs.FS
	ctx     context.Context
	started chan struct{}
}

func (s *stallOpenFS) Open(name string) (fs.File, error) {
	f, err := s.FS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		return f, nil
	}
	return &stallFile{File: f, ctx: s.ctx, started: s.started}, nil
}

type stallFile struct {
	fs.File
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (f *stallFile) Read(p []byte) (int, error) {
	f.once.Do(func() {
		close(f.started)
	})
	<-f.ctx.Done()
	return 0, f.ctx.Err()
}
