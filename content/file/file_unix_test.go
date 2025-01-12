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
	"context"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

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
	// chmod to read-only so that the removal will fail
	if err := os.Chmod(symlink, 0444); err != nil {
		t.Fatal("error calling Chmod(), error =", err)
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
	if err := oras.CopyGraph(ctx, src, dst, manifestDesc, oras.DefaultCopyGraphOptions); err == nil {
		t.Error("oras.CopyGraph() error = nil, wantErr = true")
	}
}
