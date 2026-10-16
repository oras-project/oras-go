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
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Test_extractTarDirectory_PreservePermissions(t *testing.T) {
	fileContent := "hello world"
	fileMode := os.FileMode(0771)
	tarData := createTar(t, []tarEntry{
		{name: "base/", mode: os.ModeDir | 0777},
		{name: "base/test.txt", content: fileContent, mode: fileMode},
	})

	tempDir := t.TempDir()
	dirName := "base"
	dirPath := filepath.Join(tempDir, dirName)
	buf := make([]byte, 1024)

	if err := extractTarDirectory(dirPath, dirName, bytes.NewReader(tarData), buf, true); err != nil {
		t.Fatalf("extractTarDirectory() error = %v", err)
	}

	filePath := filepath.Join(dirPath, "test.txt")
	fi, err := os.Lstat(filePath)
	if err != nil {
		t.Fatalf("failed to stat file %s: %v", filePath, err)
	}

	gotContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", filePath, err)
	}
	if string(gotContent) != fileContent {
		t.Errorf("file content = %s, want %s", gotContent, fileContent)
	}

	if fi.Mode() != fileMode {
		t.Errorf("file %q mode = %s, want %s", fi.Name(), fi.Mode(), fileMode)
	}
}

// Test_extractTarDirectory_SymlinkMetadata reproduces GHSA-jh75-xv39-vhvx
// variant A: a symlink entry whose target is lexically inside the extraction
// root but resolves, through a previously planted "up" -> ".." symlink, to a
// file outside it. Restoring the entry's times must not follow the symlink.
func Test_extractTarDirectory_SymlinkMetadata(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "base"
	dirPath := filepath.Join(tempDir, dirName)

	outsideDir := filepath.Join(tempDir, "outside")
	if err := os.Mkdir(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outsideDir, "victim")
	if err := os.WriteFile(victim, []byte("victim"), 0644); err != nil {
		t.Fatal(err)
	}
	victimTime := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(victim, victimTime, victimTime); err != nil {
		t.Fatal(err)
	}

	// The tar entries carry the zero modification time, which differs from
	// victimTime.
	tarData := createTar(t, []tarEntry{
		{name: "base/", mode: os.ModeDir | 0755},
		{name: "base/a/", mode: os.ModeDir | 0755},
		{name: "base/a/up", linkname: "..", mode: os.ModeSymlink | 0777},
		{name: "base/f", linkname: "a/up/../outside/victim", mode: os.ModeSymlink | 0777},
	})
	for _, preservePermissions := range []bool{false, true} {
		_ = extractTarDirectory(dirPath, dirName, bytes.NewReader(tarData), make([]byte, 1024), preservePermissions)

		fi, err := os.Stat(victim)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.ModTime(); !got.Equal(victimTime) {
			t.Fatalf("preservePermissions=%v: mtime of file outside the extraction root changed to %v, want %v", preservePermissions, got, victimTime)
		}
		if got := fi.Mode().Perm(); got != 0644 {
			t.Fatalf("preservePermissions=%v: mode of file outside the extraction root changed to %v, want %v", preservePermissions, got, os.FileMode(0644))
		}
	}
}

// Test_extractTarDirectory_DirOverSymlink reproduces GHSA-jh75-xv39-vhvx
// variant B: a symlink entry pointing at a directory outside the extraction
// root, followed by a directory entry with the same name. The directory entry
// must not reuse the symlink, and its mode and times must not be applied to
// the outside directory.
func Test_extractTarDirectory_DirOverSymlink(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "base"
	dirPath := filepath.Join(tempDir, dirName)

	outsideDir := filepath.Join(tempDir, "outside")
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideTime := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(outsideDir, outsideTime, outsideTime); err != nil {
		t.Fatal(err)
	}

	tarData := createTar(t, []tarEntry{
		{name: "base/", mode: os.ModeDir | 0755},
		{name: "base/a/", mode: os.ModeDir | 0755},
		{name: "base/a/up", linkname: "..", mode: os.ModeSymlink | 0777},
		{name: "base/d", linkname: "a/up/../outside", mode: os.ModeSymlink | 0777},
		{name: "base/d/", mode: os.ModeDir | 0777},
	})
	if err := extractTarDirectory(dirPath, dirName, bytes.NewReader(tarData), make([]byte, 1024), true); err != nil {
		t.Fatalf("extractTarDirectory() error = %v", err)
	}

	fi, err := os.Stat(outsideDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0700 {
		t.Errorf("mode of directory outside the extraction root changed to %v, want %v", got, os.FileMode(0700))
	}
	if got := fi.ModTime(); !got.Equal(outsideTime) {
		t.Errorf("mtime of directory outside the extraction root changed to %v, want %v", got, outsideTime)
	}

	// the directory entry replaces the symlink with a real directory
	fi, err = os.Lstat(filepath.Join(dirPath, "d"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatalf("%q mode = %v, want a directory", "base/d", fi.Mode())
	}
	if got := fi.Mode().Perm(); got != 0777 {
		t.Errorf("%q mode = %v, want %v", "base/d", got, os.FileMode(0777))
	}
}
