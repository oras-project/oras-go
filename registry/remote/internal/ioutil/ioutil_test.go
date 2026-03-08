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

package ioutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngest_success(t *testing.T) {
	dir := t.TempDir()
	content := "hello world"

	path, err := Ingest(dir, strings.NewReader(content))
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	defer os.Remove(path)

	// file should exist and contain the content
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != content {
		t.Errorf("Ingest() content = %q, want %q", string(got), content)
	}

	// file should be in the specified directory
	if filepath.Dir(path) != dir {
		t.Errorf("Ingest() dir = %q, want %q", filepath.Dir(path), dir)
	}

	// file should have 0600 permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("Ingest() perm = %04o, want 0600", perm)
	}
}

func TestIngest_namePrefix(t *testing.T) {
	dir := t.TempDir()

	path, err := Ingest(dir, strings.NewReader(""))
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	defer os.Remove(path)

	if base := filepath.Base(path); !strings.HasPrefix(base, "oras_credstore_temp_") {
		t.Errorf("Ingest() filename = %q, want prefix %q", base, "oras_credstore_temp_")
	}
}

func TestIngest_invalidDir(t *testing.T) {
	path, err := Ingest("/nonexistent/dir", strings.NewReader("data"))
	if err == nil {
		os.Remove(path)
		t.Fatal("Ingest() expected error for invalid dir, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create ingest file") {
		t.Errorf("Ingest() error = %v, want 'failed to create ingest file'", err)
	}
}

func TestIngest_readerError_cleansUpTempFile(t *testing.T) {
	dir := t.TempDir()
	readErr := errors.New("read failure")

	path, err := Ingest(dir, &errorReader{err: readErr})
	if err == nil {
		os.Remove(path)
		t.Fatal("Ingest() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to ingest") {
		t.Errorf("Ingest() error = %v, want 'failed to ingest'", err)
	}

	// temp file must have been cleaned up
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "oras_credstore_temp_") {
			t.Errorf("Ingest() left temp file %q after read error", e.Name())
		}
	}
}

func TestIngest_emptyContent(t *testing.T) {
	dir := t.TempDir()

	path, err := Ingest(dir, strings.NewReader(""))
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("Ingest() size = %d, want 0", info.Size())
	}
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

// compile-time check
var _ io.Reader = (*errorReader)(nil)
