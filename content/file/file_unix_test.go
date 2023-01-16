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
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// Related issue: https://github.com/oras-project/oras-go/issues/402
func TestStore_Dir_ExtractSymlink(t *testing.T) {
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
	symlink := filepath.Join(dirPath, "test_symlink")
	if err := os.Symlink(fileName, symlink); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src := New(tempDir)
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	val, ok := src.digestToPath.Load(desc.Digest)
	if !ok {
		t.Fatal("failed to find internal gz")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal gz, error =", err)
	}
	gotgz, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal gz, error =", err)
	}
	if err := zrc.Close(); err != nil {
		t.Error("failed to close internal gz, error =", err)
	}

	// pack a manifest
	manifestDesc, err := oras.Pack(ctx, src, "dir", []ocispec.Descriptor{desc}, oras.PackOptions{})
	if err != nil {
		t.Fatal("oras.Pack() error =", err)
	}

	// copy to another file store created from an absolute root, to trigger extracting directory
	tempDir = t.TempDir()
	dstAbs := New(tempDir)
	defer dstAbs.Close()
	if err := oras.CopyGraph(ctx, src, dstAbs, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// compare content
	rc, err := dstAbs.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, gotgz) {
		t.Errorf("Store.Fetch() = %v, want %v", got, gotgz)
	}

	// copy to another file store created from a relative root, to trigger extracting directory
	tempDir = t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
	dstRel := New(".")
	defer dstRel.Close()
	if err := oras.CopyGraph(ctx, src, dstRel, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// compare content
	rc, err = dstAbs.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, gotgz) {
		t.Errorf("Store.Fetch() = %v, want %v", got, gotgz)
	}
}

// Related issue: https://github.com/oras-project/oras-go/issues/402
func TestStore_Dir_ExtractSymlinkAbs(t *testing.T) {
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
	// create symlink to an absolute path
	symlink := filepath.Join(dirPath, "test_symlink")
	if err := os.Symlink(filePath, symlink); err != nil {
		t.Fatal("error calling Symlink(), error =", err)
	}

	src := New(tempDir)
	defer src.Close()
	ctx := context.Background()

	// add dir
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	val, ok := src.digestToPath.Load(desc.Digest)
	if !ok {
		t.Fatal("failed to find internal gz")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal gz, error =", err)
	}
	gotgz, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal gz, error =", err)
	}
	if err := zrc.Close(); err != nil {
		t.Error("failed to close internal gz, error =", err)
	}

	// pack a manifest
	manifestDesc, err := oras.Pack(ctx, src, "dir", []ocispec.Descriptor{desc}, oras.PackOptions{})
	if err != nil {
		t.Fatal("oras.Pack() error =", err)
	}

	// remove the original testing directory and create a new store using an absolute root
	if err := os.RemoveAll(dirPath); err != nil {
		t.Fatal("error calling RemoveAll(), error =", err)
	}
	dst := New(tempDir)
	defer dst.Close()
	if err := oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal("oras.CopyGraph() error =", err)
	}

	// compare content
	rc, err := dst.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, gotgz) {
		t.Errorf("Store.Fetch() = %v, want %v", got, gotgz)
	}

	// TODO: test relative root after https://github.com/oras-project/oras-go/issues/404 gets resolved
}
