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
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
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
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
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
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
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
