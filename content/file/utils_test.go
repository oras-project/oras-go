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
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func Test_tarDirectory(t *testing.T) {
	setup := func(t *testing.T) (tmpdir string, gz *os.File, gw *gzip.Writer) {
		tmpdir = t.TempDir()

		paths := []string{
			filepath.Join(tmpdir, "file1.txt"),
			filepath.Join(tmpdir, "file2.txt"),
		}

		for _, p := range paths {
			err := os.WriteFile(p, []byte("test content"), 0644)
			if err != nil {
				t.Fatal(err)
			}
		}

		gz, err := os.CreateTemp(tmpdir, "tarDirectory-*")
		if err != nil {
			t.Fatal(err)
		}

		return tmpdir, gz, gzip.NewWriter(gz)
	}

	t.Run("success", func(t *testing.T) {
		tmpdir, gz, gw := setup(t)
		defer func() {
			if err := gw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
		}()

		err := tarDirectory(context.Background(), tmpdir, "prefix", gw, false, nil)
		if err != nil {
			t.Fatal(err)
		}

		_, err = gz.Stat()
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		tmpdir, gz, gw := setup(t)
		defer func() {
			if err := gw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
		}()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := tarDirectory(ctx, tmpdir, "prefix", gw, false, nil)
		if err == nil {
			t.Fatal("expected context cancellation error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got %v", err)
		}

		_, err = gz.Stat()
		if err != nil {
			t.Fatal(err)
		}
	})
}

func Test_ensureBasePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hello world", "foo", "bar"), 0700); err != nil {
		t.Fatal("failed to create temp folders:", err)
	}
	baseRel := "hello world/foo"
	baseAbs := filepath.Join(root, baseRel)

	tests := []struct {
		name    string
		target  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid case (depth 0)",
			target: "hello world/foo",
			want:   ".",
		},
		{
			name:   "valid case (depth 1)",
			target: "hello world/foo/bar",
			want:   "bar",
		},
		{
			name:   "valid case (depth 2)",
			target: "hello world/foo/bar/fun",
			want:   filepath.Join("bar", "fun"),
		},
		{
			name:    "invalid prefix",
			target:  "hello world/fun",
			wantErr: true,
		},
		{
			name:    "invalid prefix",
			target:  "hello/foo",
			wantErr: true,
		},
		{
			name:    "bad traversal",
			target:  "hello world/foo/..",
			wantErr: true,
		},
		{
			name:   "valid traversal",
			target: "hello world/foo/../foo/bar/../bar",
			want:   "bar",
		},
		{
			name:    "complex traversal",
			target:  "hello world/foo/../foo/bar/../..",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRelToBase(baseAbs, baseRel, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("ensureBasePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ensureBasePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ensureLinkPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hello world", "foo", "bar"), 0700); err != nil {
		t.Fatal("failed to create temp folders:", err)
	}
	baseRel := "hello world/foo"
	baseAbs := filepath.Join(root, baseRel)

	tests := []struct {
		name    string
		link    string
		target  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid case (depth 1)",
			link:   "hello world/foo/bar",
			target: "fun",
			want:   "fun",
		},
		{
			name:   "valid case (depth 2)",
			link:   "hello world/foo/bar/fun",
			target: "../fun",
			want:   "../fun",
		},
		{
			name:    "invalid prefix",
			link:    "hello world/foo",
			target:  "fun",
			wantErr: true,
		},
		{
			name:    "bad traversal",
			link:    "hello world/foo/bar",
			target:  "../fun",
			wantErr: true,
		},
		{
			name:   "valid traversal",
			link:   "hello world/foo/../foo/bar/../bar", // hello world/foo/bar
			target: "../foo/../foo/fun",
			want:   "../foo/../foo/fun",
		},
		{
			name:    "complex traversal",
			link:    "hello world/foo/bar",
			target:  "../foo/../../fun",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ensureLinkPath(baseAbs, baseRel, tt.link, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("ensureLinkPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ensureLinkPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractTarGzip_Error(t *testing.T) {
	t.Run("Non-existing file", func(t *testing.T) {
		err := extractTarGzip("", "", "non-existing-file", "", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
