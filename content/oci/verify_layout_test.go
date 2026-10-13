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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/errdef"
)

const (
	validLayoutJSON = `{"imageLayoutVersion":"1.0.0"}`
	validIndexJSON  = `{"schemaVersion":2,"manifests":[]}`
)

func validLayoutFS() fstest.MapFS {
	return fstest.MapFS{
		ocispec.ImageLayoutFile:                 &fstest.MapFile{Data: []byte(validLayoutJSON)},
		ocispec.ImageIndexFile:                  &fstest.MapFile{Data: []byte(validIndexJSON)},
		ocispec.ImageBlobsDir + "/sha256/.keep": &fstest.MapFile{},
	}
}

func layoutPaths(findings []LayoutFinding) []string {
	out := make([]string, len(findings))
	for i, f := range findings {
		out[i] = f.Path
	}
	return out
}

func mustContainFinding(t *testing.T, findings []LayoutFinding, p string) LayoutFinding {
	t.Helper()
	for _, f := range findings {
		if f.Path == p {
			if f.Reason == nil {
				t.Errorf("finding for %q has a nil reason", p)
			}
			return f
		}
	}
	t.Fatalf("no layout finding for %q in %v", p, layoutPaths(findings))
	return LayoutFinding{}
}

func tempFileName(base string) string {
	return base + "_" + strings.Repeat("A", tempFileSuffixMinLen)
}

func TestVerify_Layout_WellFormed(t *testing.T) {
	report, err := Verify(context.Background(), validLayoutFS(), VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Layout) != 0 {
		t.Errorf("Layout = %v, want none", layoutPaths(report.Layout))
	}
}

func TestVerify_Layout_OCILayoutFile(t *testing.T) {
	tests := []struct {
		name    string
		file    *fstest.MapFile
		wantErr error
	}{
		{name: "missing", wantErr: fs.ErrNotExist},
		{name: "not JSON", file: &fstest.MapFile{Data: []byte("not json")}},
		{name: "wrong version", file: &fstest.MapFile{Data: []byte(`{"imageLayoutVersion":"2.0.0"}`)}, wantErr: errdef.ErrUnsupportedVersion},
		{name: "empty version", file: &fstest.MapFile{Data: []byte(`{}`)}, wantErr: errdef.ErrUnsupportedVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := validLayoutFS()
			if tt.file == nil {
				delete(fsys, ocispec.ImageLayoutFile)
			} else {
				fsys[ocispec.ImageLayoutFile] = tt.file
			}
			report, err := Verify(context.Background(), fsys, VerifyOptions{})
			if err != nil {
				t.Fatal("Verify() error =", err)
			}
			if len(report.Layout) != 1 {
				t.Fatalf("Layout = %v, want only %s", layoutPaths(report.Layout), ocispec.ImageLayoutFile)
			}
			got := mustContainFinding(t, report.Layout, ocispec.ImageLayoutFile)
			if tt.wantErr != nil && !errors.Is(got.Reason, tt.wantErr) {
				t.Errorf("Reason = %v, want %v", got.Reason, tt.wantErr)
			}
			if !got.ModTime.IsZero() {
				t.Errorf("ModTime = %v, want zero", got.ModTime)
			}
		})
	}
}

func TestVerify_Layout_IndexFile(t *testing.T) {
	tests := []struct {
		name    string
		file    *fstest.MapFile
		wantErr error
	}{
		{name: "missing", wantErr: fs.ErrNotExist},
		{name: "not JSON", file: &fstest.MapFile{Data: []byte("{")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := validLayoutFS()
			if tt.file == nil {
				delete(fsys, ocispec.ImageIndexFile)
			} else {
				fsys[ocispec.ImageIndexFile] = tt.file
			}
			report, err := Verify(context.Background(), fsys, VerifyOptions{})
			if err != nil {
				t.Fatal("Verify() error =", err)
			}
			if len(report.Layout) != 1 {
				t.Fatalf("Layout = %v, want only %s", layoutPaths(report.Layout), ocispec.ImageIndexFile)
			}
			got := mustContainFinding(t, report.Layout, ocispec.ImageIndexFile)
			if tt.wantErr != nil && !errors.Is(got.Reason, tt.wantErr) {
				t.Errorf("Reason = %v, want %v", got.Reason, tt.wantErr)
			}
		})
	}
}

func TestVerify_Layout_MissingIndexDoesNotStopBlobSweep(t *testing.T) {
	blob := []byte("sweep-continues")
	fsys := validLayoutFS()
	delete(fsys, ocispec.ImageIndexFile)
	fsys[blobFilePath(blob)] = &fstest.MapFile{Data: blob}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	mustContainFinding(t, report.Layout, ocispec.ImageIndexFile)
	mustContainPath(t, report.Verified, blobFilePath(blob))
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %v, want none", entryPaths(report.Failed))
	}
}

func TestVerify_Layout_Leftovers(t *testing.T) {
	modTime := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ingestFile := ingestDirName + "/" + strings.Repeat("a", 64) + "_123456"
	indexTemp := tempFileName(ocispec.ImageIndexFile)
	layoutTemp := tempFileName(ocispec.ImageLayoutFile)

	fsys := validLayoutFS()
	fsys[ingestFile] = &fstest.MapFile{Data: []byte("partial"), ModTime: modTime}
	fsys[indexTemp] = &fstest.MapFile{Data: []byte("{"), ModTime: modTime}
	fsys[layoutTemp] = &fstest.MapFile{Data: []byte("{"), ModTime: modTime}
	// not leftovers of ours: a kept copy of index.json, an unrelated file, and
	// a directory inside ingest/
	fsys["index.json_backup"] = &fstest.MapFile{Data: []byte(validIndexJSON)}
	fsys["README"] = &fstest.MapFile{Data: []byte("hello")}
	fsys[ingestDirName+"/subdir/file"] = &fstest.MapFile{Data: []byte("x")}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	if len(report.Layout) != 3 {
		t.Fatalf("Layout = %v, want the ingest file and the two temporary files", layoutPaths(report.Layout))
	}
	for _, p := range []string{ingestFile, indexTemp, layoutTemp} {
		got := mustContainFinding(t, report.Layout, p)
		if !got.ModTime.Equal(modTime) {
			t.Errorf("%s: ModTime = %v, want %v", p, got.ModTime, modTime)
		}
	}
}

func TestVerify_Layout_NoIngestDirIsNotAFinding(t *testing.T) {
	report, err := Verify(context.Background(), validLayoutFS(), VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	for _, f := range report.Layout {
		if strings.HasPrefix(f.Path, ingestDirName) {
			t.Errorf("unexpected finding %v for a layout without %s/", f, ingestDirName)
		}
	}
}

func TestVerify_Layout_UnreadableIngestDir(t *testing.T) {
	fsys := validLayoutFS()
	fsys[ingestDirName] = &fstest.MapFile{Data: []byte("not a directory")}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	mustContainFinding(t, report.Layout, ingestDirName)
}

func TestVerify_Layout_StrayFileUnderBlobs(t *testing.T) {
	fsys := validLayoutFS()
	stray := ocispec.ImageBlobsDir + "/stray.txt"
	fsys[stray] = &fstest.MapFile{Data: []byte("stray")}

	report, err := Verify(context.Background(), fsys, VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	mustContainPath(t, report.Unverifiable, stray)
	if len(report.Failed) != 0 {
		t.Errorf("Failed = %v, want none", entryPaths(report.Failed))
	}
}

type unreadableRootFS struct{ fstest.MapFS }

func (f unreadableRootFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "." {
		return nil, errors.New("root unreadable")
	}
	return f.MapFS.ReadDir(name)
}

func TestVerify_Layout_UnreadableRoot(t *testing.T) {
	report, err := Verify(context.Background(), unreadableRootFS{validLayoutFS()}, VerifyOptions{})
	if err == nil {
		t.Fatal("Verify() error = nil, want unreadable root")
	}
	if report != nil {
		t.Errorf("report = %#v, want nil", report)
	}
}

func TestVerify_Layout_TempDir_ReadOnly(t *testing.T) {
	root := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write(ocispec.ImageLayoutFile, validLayoutJSON)
	write(ocispec.ImageIndexFile, validIndexJSON)
	write(ocispec.ImageBlobsDir+"/sha256/.keep", "")
	ingestFile := ingestDirName + "/leftover_1"
	write(ingestFile, "partial")
	indexTemp := tempFileName(ocispec.ImageIndexFile)
	write(indexTemp, "{")

	before := snapshotDir(t, root)
	report, err := Verify(context.Background(), os.DirFS(root), VerifyOptions{})
	if err != nil {
		t.Fatal("Verify() error =", err)
	}
	after := snapshotDir(t, root)
	if len(before) != len(after) {
		t.Fatalf("layout mutated: before %d entries, after %d", len(before), len(after))
	}
	for p, meta := range before {
		if got, ok := after[p]; !ok || got != meta {
			t.Errorf("path %s mutated: before %#v after %#v (present: %v)", p, meta, got, ok)
		}
	}

	if len(report.Layout) != 2 {
		t.Fatalf("Layout = %v, want the ingest file and the temporary index.json", layoutPaths(report.Layout))
	}
	for _, p := range []string{ingestFile, indexTemp} {
		if got := mustContainFinding(t, report.Layout, p); got.ModTime.IsZero() {
			t.Errorf("%s: ModTime is zero, want the file modification time", p)
		}
	}
}
