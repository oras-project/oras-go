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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	_ "crypto/sha256"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
)

// storageTracker tracks storage API counts.
type storageTracker struct {
	content.Storage
	fetch  int64
	push   int64
	exists int64
}

func (t *storageTracker) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	atomic.AddInt64(&t.fetch, 1)
	return t.Storage.Fetch(ctx, target)
}

func (t *storageTracker) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	atomic.AddInt64(&t.push, 1)
	return t.Storage.Push(ctx, expected, content)
}

func (t *storageTracker) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	atomic.AddInt64(&t.exists, 1)
	return t.Storage.Exists(ctx, target)
}

func TestStoreInterface(t *testing.T) {
	var store interface{} = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(content.UpEdgeFinder); !ok {
		t.Error("&Store{} does not conform content.UpEdgeFinder")
	}
}

func TestStore_Success(t *testing.T) {
	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	blob := []byte("hello world")
	name := "test.txt"
	mediaType := "test"
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, blob, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	// test blob add
	gotDesc, err := s.Add(ctx, name, mediaType, path)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc) != descriptor.FromOCI(desc) {
		t.Fatal("got descriptor mismatch")
	}

	// test blob exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test blob fetch
	rc, err := s.Fetch(ctx, gotDesc)
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
	if !bytes.Equal(got, blob) {
		t.Errorf("Store.Fetch() = %v, want %v", got, blob)
	}

	// test push config
	config := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: "config",
		Digest:    digest.FromBytes(config),
		Size:      int64(len(config)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "config.blob",
		},
	}
	if err := s.Push(ctx, configDesc, bytes.NewReader(config)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test push manifest
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers: []ocispec.Descriptor{
			gotDesc,
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("json.Marshal() error =", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestJSON),
		Size:      int64(len(manifestJSON)),
	}
	if err := s.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test tag
	ref := "foobar"
	if err := s.Tag(ctx, manifestDesc, ref); err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	// test resolve
	gotManifestDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotManifestDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotManifestDesc, manifestDesc)
	}

	// test fetch
	exists, err = s.Exists(ctx, gotManifestDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	mrc, err := s.Fetch(ctx, gotManifestDesc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err = io.ReadAll(mrc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = mrc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, manifestJSON) {
		t.Errorf("Store.Fetch() = %v, want %v", got, manifestJSON)
	}
}

func TestStore_Close(t *testing.T) {
	content := []byte("hello world")
	name := "test.txt"
	mediaType := "test"
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}
	ref := "foobar"

	tempDir := t.TempDir()
	s := New(tempDir)
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}

	// test close
	if err := s.Close(); err != nil {
		t.Error("Store.Close() error =", err)
	}
	// test close twice
	if err := s.Close(); err != nil {
		t.Error("Store.Close() error =", err)
	}

	// test add after closed
	if _, err := s.Add(ctx, name, mediaType, ""); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Add() = %v, want %v", err, ErrStoreClosed)
	}

	// test push after closed
	if err = s.Push(ctx, desc, bytes.NewReader(content)); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Push() = %v, want %v", err, ErrStoreClosed)
	}

	// test exists after closed
	if _, err := s.Exists(ctx, desc); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Exists() = %v, want %v", err, ErrStoreClosed)
	}

	// test tag after closed
	if err := s.Tag(ctx, desc, ref); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Tag() = %v, want %v", err, ErrStoreClosed)
	}

	// test resolve after closed
	if _, err := s.Resolve(ctx, ref); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Resolve() = %v, want %v", err, ErrStoreClosed)
	}

	// test fetch after closed
	if _, err := s.Fetch(ctx, desc); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.Fetch() = %v, want %v", err, ErrStoreClosed)
	}

	// test UpEdges after closed
	if _, err := s.UpEdges(ctx, desc); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.UpEdges() = %v, want %v", err, ErrStoreClosed)
	}

	// test PackFiles after close
	if _, err := s.PackFiles(ctx, []string{}); !errors.Is(err, ErrStoreClosed) {
		t.Errorf("Store.PackFiles() = %v, want %v", err, ErrStoreClosed)
	}
}

func TestStore_File_Push(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}
	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
}

func TestStore_Dir_Push(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	if err := ioutil.WriteFile(filepath.Join(dirPath, fileName), content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	desc, err := s.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error=", err)
	}

	val, ok := s.digestToPath.Load(desc.Digest)
	if !ok {
		t.Fatal("failed to find internal gz")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal gz")
	}
	gz, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal gz")
	}
	if err := zrc.Close(); err != nil {
		t.Fatal("failed to close internal gz reader")
	}

	anotherTempDir := t.TempDir()
	// test with another file store instance to mock push gz
	anotherS := New(anotherTempDir)
	defer anotherS.Close()

	// test push
	if err := anotherS.Push(ctx, desc, bytes.NewReader(gz)); err != nil {
		t.Fatal("Store.Push() error =", err)

	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, gz) {
		t.Errorf("Store.Fetch() = %v, want %v", got, gz)
	}

	// test file content
	path := filepath.Join(s.workingDir, dirName, fileName)
	fp, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open file %s:%v", path, err)
	}
	fc, err := io.ReadAll(fp)
	if err != nil {
		t.Fatalf("failed to read file %s:%v", path, err)
	}
	if err := fp.Close(); err != nil {
		t.Fatalf("failed to close file %s:%v", path, err)
	}

	anotherFilePath := filepath.Join(anotherS.workingDir, dirName, fileName)
	anotherFp, err := os.Open(anotherFilePath)
	if err != nil {
		t.Fatalf("failed to open file %s:%v", anotherFilePath, err)
	}
	anotherFc, err := io.ReadAll(anotherFp)
	if err != nil {
		t.Fatalf("failed to read file %s:%v", anotherFilePath, err)
	}
	if err := anotherFp.Close(); err != nil {
		t.Fatalf("failed to close file %s:%v", anotherFilePath, err)
	}

	if !bytes.Equal(fc, anotherFc) {
		t.Errorf("file content mismatch")
	}
}

func TestStore_Push_NoName(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
}

func TestStore_Push_NoName_ExceedLimit(t *testing.T) {
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	tempDir := t.TempDir()
	s := NewWithFallbackLimit(tempDir, 1)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(blob))
	if !errors.Is(err, content.ErrSizeExceedLimit) {
		t.Errorf("Store.Push() error = %v, want %v", err, content.ErrSizeExceedLimit)
	}
}

func TestStore_Push_NoName_SizeNotMatch(t *testing.T) {
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      1,
	}

	tempDir := t.TempDir()
	s := NewWithFallbackLimit(tempDir, 1)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(blob))
	if err == nil {
		t.Errorf("Store.Push() error = nil, want: error")
	}
}

func TestStore_File_NotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_File_ContentBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	err := s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStore_File_Add(t *testing.T) {
	content := []byte("hello world")
	name := "test.txt"
	mediaType := "test"
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(ctx, name, mediaType, path)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc) != descriptor.FromOCI(desc) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
}

func TestStore_Dir_Add(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	if err := ioutil.WriteFile(filepath.Join(dirPath, "test.txt"), content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error=", err)
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	val, ok := s.digestToPath.Load(gotDesc.Digest)
	if !ok {
		t.Fatal("failed to find internal gz")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal gz")
	}
	gotgz, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal gz")
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
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
}
func TestStore_File_SameContent_DuplicateName(t *testing.T) {
	content := []byte("hello world")
	name := "test.txt"
	mediaType := "test"
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(ctx, name, mediaType, path)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc) != descriptor.FromOCI(desc) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}

	// test duplicate name
	if _, err := s.Add(ctx, name, mediaType, path); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Store.Add() = %v, want %v", err, ErrDuplicateName)
	}
}

func TestStore_File_DifferentContent_DuplicateName(t *testing.T) {
	content_1 := []byte("hello world")
	content_2 := []byte("goodbye world")

	name_1 := "test_1.txt"
	name_2 := "test_2.txt"

	mediaType_1 := "test"
	mediaType_2 := "test_2"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType_1,
		Digest:    digest.FromBytes(content_1),
		Size:      int64(len(content_1)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	tempDir := t.TempDir()
	path_1 := filepath.Join(tempDir, name_1)
	if err := ioutil.WriteFile(path_1, content_1, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(ctx, name_1, mediaType_1, path_1)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc) != descriptor.FromOCI(desc_1) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
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
	if !bytes.Equal(got, content_1) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content_1)
	}

	// test add duplicate name
	path_2 := filepath.Join(tempDir, name_2)
	if err := ioutil.WriteFile(path_2, content_2, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	if _, err := s.Add(ctx, name_1, mediaType_2, path_2); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Store.Add() = %v, want %v", err, ErrDuplicateName)
	}
}

func TestStore_File_Add_MissingName(t *testing.T) {
	content := []byte("hello world")
	name := "test.txt"
	mediaType := "test"

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add with empty name
	_, err := s.Add(ctx, "", mediaType, path)
	if !errors.Is(err, ErrMissingName) {
		t.Errorf("Store.Add() error = %v, want %v", err, ErrMissingName)
	}
}

func TestStore_File_Add_SameContent(t *testing.T) {
	mediaType := "test"
	content := []byte("hello world")

	name_1 := "test_1.txt"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	name_2 := "test_2.txt"
	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_2,
		},
	}

	tempDir := t.TempDir()
	path_1 := filepath.Join(tempDir, name_1)
	if err := ioutil.WriteFile(path_1, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	path_2 := filepath.Join(tempDir, name_2)
	if err := ioutil.WriteFile(path_2, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc_1, err := s.Add(ctx, name_1, mediaType, path_1)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc_1) != descriptor.FromOCI(desc_1) {
		t.Fatal("got descriptor mismatch")
	}

	gotDesc_2, err := s.Add(ctx, name_2, mediaType, path_2)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc_2) != descriptor.FromOCI(desc_2) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, gotDesc_2)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc_1, err := s.Fetch(ctx, gotDesc_1)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_1, content)
	}

	rc_2, err := s.Fetch(ctx, gotDesc_2)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_2, err := io.ReadAll(rc_2)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_2.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_2, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_2, content)
	}
}

func TestStore_PackFiles(t *testing.T) {
	var names []string

	content_1 := []byte("hello world")
	name_1 := "test_1.txt"
	names = append(names, name_1)
	desc_1 := ocispec.Descriptor{
		MediaType: defaultBlobMediaType,
		Digest:    digest.FromBytes(content_1),
		Size:      int64(len(content_1)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	content_2 := []byte("goodbye world")
	name_2 := "test_2.txt"
	names = append(names, name_2)
	desc_2 := ocispec.Descriptor{
		MediaType: defaultBlobMediaType,
		Digest:    digest.FromBytes(content_2),
		Size:      int64(len(content_2)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_2,
		},
	}

	tempDir := t.TempDir()
	if err := ioutil.WriteFile(filepath.Join(tempDir, name_1), content_1, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	if err := ioutil.WriteFile(filepath.Join(tempDir, name_2), content_2, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test pack
	manifestDesc, err := s.PackFiles(ctx, names)
	if err != nil {
		t.Fatal("Store.Pack() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}

	var manifest ocispec.Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatal("failed to decode manifest, err =", err)
	}
	if err = rc.Close(); err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}

	exists, err = s.Exists(ctx, manifest.Config)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}
}

func TestStore_File_Push_SameContent(t *testing.T) {
	mediaType := "test"
	content := []byte("hello world")

	name_1 := "test_1.txt"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	name_2 := "test_2.txt"
	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_2,
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	if err := s.Push(ctx, desc_1, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if err := s.Push(ctx, desc_2, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc_1, err := s.Fetch(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_1, content)
	}

	rc_2, err := s.Fetch(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_2, err := io.ReadAll(rc_2)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_2.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_2, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_2, content)
	}
}

func TestStore_File_Push_DuplicateName(t *testing.T) {
	mediaType := "test"
	name := "test.txt"
	content_1 := []byte("hello world")
	content_2 := []byte("goodbye world")
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content_1),
		Size:      int64(len(content_1)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}
	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content_2),
		Size:      int64(len(content_2)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc_1, bytes.NewReader(content_1))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc_1)
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
	if !bytes.Equal(got, content_1) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content_1)
	}

	// test push with duplicate name
	err = s.Push(ctx, desc_2, bytes.NewBuffer(content_2))
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Store.Push() error = %v, want %v", err, ErrDuplicateName)
	}
}

func TestStore_File_Fetch_SameDigest_NoName(t *testing.T) {
	mediaType := "test"
	content := []byte("hello world")

	name_1 := "test_1.txt"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	if err := s.Push(ctx, desc_1, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if err := s.Push(ctx, desc_2, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc_1, err := s.Fetch(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_1, content)
	}

	rc_2, err := s.Fetch(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_2, err := io.ReadAll(rc_2)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_2.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_2, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_2, content)
	}
}

func TestStore_File_Fetch_SameDigest_DifferentName(t *testing.T) {
	mediaType := "test"
	content := []byte("hello world")

	name_1 := "test_1.txt"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	name_2 := "test_2.txt"
	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_2,
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test desc_1
	if err := s.Push(ctx, desc_1, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc_1, err := s.Fetch(ctx, desc_1)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got_1, content)
	}

	// test desc_2
	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc_2)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_File_Push_Overwrite(t *testing.T) {
	mediaType := "test"
	name := "test.txt"
	old_content := []byte("hello world")
	new_content := []byte("goodbye world")
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(new_content),
		Size:      int64(len(new_content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, old_content, 0666); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err := s.Push(ctx, desc, bytes.NewReader(new_content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, new_content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, new_content)
	}

}

func TestStore_File_Push_DisableOverwrite(t *testing.T) {
	content := []byte("hello world")
	name := "test.txt"
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, name)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	s.DisableOverwrite = true

	ctx := context.Background()
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, ErrOverwriteDisallowed) {
		t.Errorf("Store.Push() error = %v, want %v", err, ErrOverwriteDisallowed)
	}
}

func TestStore_File_Push_DisallowPathTraversal(t *testing.T) {
	content := []byte("hello world")
	name := "../test.txt"
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()

	ctx := context.Background()
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, ErrPathTraversalDisallowed) {
		t.Errorf("Store.Push() error = %v, want %v", err, ErrPathTraversalDisallowed)
	}
}

func TestStore_Dir_Push_DisallowPathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "../testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	if err := ioutil.WriteFile(filepath.Join(dirPath, fileName), content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	desc, err := s.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error=", err)
	}

	val, ok := s.digestToPath.Load(desc.Digest)
	if !ok {
		t.Fatal("failed to find internal gz")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal gz")
	}
	gz, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal gz")
	}
	if err := zrc.Close(); err != nil {
		t.Fatal("failed to close internal gz reader")
	}

	anotherTempDir := t.TempDir()
	// test with another file store instance to mock push gz
	anotherS := New(anotherTempDir)
	defer anotherS.Close()

	// test push
	err = anotherS.Push(ctx, desc, bytes.NewReader(gz))
	if !errors.Is(err, ErrPathTraversalDisallowed) {
		t.Errorf("Store.Push() error = %v, want %v", err, ErrPathTraversalDisallowed)
	}
}

func TestStore_File_Push_PathTraversal(t *testing.T) {
	content := []byte("hello world")
	name := "../test.txt"
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}

	tempDir := t.TempDir()
	subTempDir, err := os.MkdirTemp(tempDir, "oras_filestore_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}

	s := New(subTempDir)
	defer s.Close()
	s.AllowPathTraversalOnWrite = true

	ctx := context.Background()
	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
}

func TestStore_File_Push_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)
	for i := 0; i < concurrency; i++ {
		eg.Go(func(i int) func() error {
			return func() error {
				if err := s.Push(egCtx, desc, bytes.NewReader(content)); err != nil {
					if errors.Is(err, ErrDuplicateName) {
						return nil
					}
					return fmt.Errorf("failed to push content: %v", err)
				}
				return nil
			}
		}(i))
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
}

func TestStore_File_Fetch_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)

	for i := 0; i < concurrency; i++ {
		eg.Go(func(i int) func() error {
			return func() error {
				rc, err := s.Fetch(egCtx, desc)
				if err != nil {
					return fmt.Errorf("failed to fetch content: %v", err)
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					t.Fatal("Store.Fetch().Read() error =", err)
				}
				err = rc.Close()
				if err != nil {
					t.Error("Store.Fetch().Close() error =", err)
				}
				if !bytes.Equal(got, content) {
					t.Errorf("Store.Fetch() = %v, want %v", got, content)
				}
				return nil
			}
		}(i))
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestStore_TagNotFound(t *testing.T) {
	ref := "foobar"

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	_, err := s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_TagUnknownContent(t *testing.T) {
	content := []byte(`{"layers":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	err := s.Tag(ctx, desc, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_RepeatTag(t *testing.T) {
	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	generate := func(content []byte) ocispec.Descriptor {
		dgst := digest.FromBytes(content)
		desc := ocispec.Descriptor{
			MediaType: "test",
			Digest:    dgst,
			Size:      int64(len(content)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		}
		return desc
	}
	ref := "foobar"

	// initial tag
	content := []byte("hello world")
	desc := generate(content)
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// repeat tag
	content = []byte("foo")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// repeat tag
	content = []byte("bar")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
}

func TestStore_UpEdges(t *testing.T) {
	tempDir := t.TempDir()
	s := New(tempDir)
	defer s.Close()
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		var manifest artifactspec.Manifest
		manifest.Subject = descriptor.OCIToArtifact(subject)
		for _, blob := range blobs {
			manifest.Blobs = append(manifest.Blobs, descriptor.OCIToArtifact(blob))
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(artifactspec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateManifest(descs[0], descs[1:4]...)                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	generateIndex()                                            // Blob 9
	generateIndex(descs[7:10]...)                              // Blob 10
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 11
	generateArtifactManifest(descs[6], descs[11])              // Blob 12
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 13
	generateArtifactManifest(descs[10], descs[13])             // Blob 14

	eg, egCtx := errgroup.WithContext(ctx)
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				err := s.Push(egCtx, descs[i], bytes.NewReader(blobs[i]))
				if err != nil {
					return fmt.Errorf("failed to push test content to src: %d: %v", i, err)
				}
				return nil
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	// verify up edges
	wants := [][]ocispec.Descriptor{
		descs[4:7],            // Blob 0
		{descs[4], descs[6]},  // Blob 1
		{descs[4], descs[6]},  // Blob 2
		{descs[5], descs[6]},  // Blob 3
		{descs[7]},            // Blob 4
		{descs[7]},            // Blob 5
		{descs[8], descs[12]}, // Blob 6
		{descs[10]},           // Blob 7
		{descs[10]},           // Blob 8
		{descs[10]},           // Blob 9
		{descs[14]},           // Blob 10
		{descs[12]},           // Blob 11
		nil,                   // Blob 12
		{descs[14]},           // Blob 13
		nil,                   // Blob 14
	}
	for i, want := range wants {
		upEdges, err := s.UpEdges(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.UpEdges(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(upEdges, want) {
			t.Errorf("Store.UpEdges(%d) = %v, want %v", i, upEdges, want)
		}
	}
}

func TestCopy_File_MemoryToFile_FullCopy(t *testing.T) {
	src := memory.New()

	tempDir := t.TempDir()
	dst := New(tempDir)
	defer dst.Close()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "foobar"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", oras.CopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
}

func TestCopyGraph_MemoryToFile_FullCopy(t *testing.T) {
	src := memory.New()

	tempDir := t.TempDir()
	dst := New(tempDir)
	defer dst.Close()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateManifest(descs[0], descs[1:4]...)                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	generateIndex()                                            // Blob 9
	generateIndex(descs[7:10]...)                              // Blob 10

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root := descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(len(blobs)); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopyGraph_MemoryToFile_PartialCopy(t *testing.T) {
	src := memory.New()

	tempDir := t.TempDir()
	dst := New(tempDir)
	defer dst.Close()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 4
	generateManifest(descs[0], descs[4])                       // Blob 5
	generateIndex(descs[3], descs[5])                          // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// initial copy
	root := descs[3]
	if err := oras.CopyGraph(ctx, src, dst, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// verify contents
	for i := range blobs[:4] {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Fatalf("content[%d] error = %v, wantErr %v", i, err, false)
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Fatalf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root = descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(3); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(3); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(5); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopy_File_FileToMemory_FullCopy(t *testing.T) {
	tempDir := t.TempDir()
	src := New(tempDir)
	defer src.Close()

	dst := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	root := descs[3]
	ref := "foobar"
	err := src.Tag(ctx, root, ref)
	if err != nil {
		t.Fatal("fail to tag root node", err)
	}

	// test copy
	gotDesc, err := oras.Copy(ctx, src, ref, dst, "", oras.CopyOptions{})
	if err != nil {
		t.Fatalf("Copy() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("Copy() = %v, want %v", gotDesc, root)
	}

	// verify contents
	for i, desc := range descs {
		exists, err := dst.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("dst.Exists(%d) error = %v", i, err)
		}
		if !exists {
			t.Errorf("dst.Exists(%d) = %v, want %v", i, exists, true)
		}
	}

	// verify tag
	gotDesc, err = dst.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
}

func TestCopyGraph_FileToMemory_FullCopy(t *testing.T) {
	tempDir := t.TempDir()
	src := New(tempDir)
	defer src.Close()

	dst := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateManifest(descs[0], descs[1:4]...)                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	generateIndex()                                            // Blob 9
	generateIndex(descs[7:10]...)                              // Blob 10

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root := descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(len(blobs)); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(len(blobs)); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func TestCopyGraph_FileToMemory_PartialCopy(t *testing.T) {
	tempDir := t.TempDir()
	src := New(tempDir)
	defer src.Close()

	dst := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		dgst := digest.FromBytes(blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    dgst,
			Size:      int64(len(blob)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: dgst.Encoded() + ".blob",
			},
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 4
	generateManifest(descs[0], descs[4])                       // Blob 5
	generateIndex(descs[3], descs[5])                          // Blob 6

	ctx := context.Background()
	for i := range blobs {
		err := src.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// initial copy
	root := descs[3]
	if err := oras.CopyGraph(ctx, src, dst, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}
	// verify contents
	for i := range blobs[:4] {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Fatalf("content[%d] error = %v, wantErr %v", i, err, false)
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Fatalf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// test copy
	srcTracker := &storageTracker{Storage: src}
	dstTracker := &storageTracker{Storage: dst}
	root = descs[len(descs)-1]
	if err := oras.CopyGraph(ctx, srcTracker, dstTracker, root, oras.CopyGraphOptions{}); err != nil {
		t.Fatalf("CopyGraph() error = %v, wantErr %v", err, false)
	}

	// verify contents
	for i := range blobs {
		got, err := content.FetchAll(ctx, dst, descs[i])
		if err != nil {
			t.Errorf("content[%d] error = %v, wantErr %v", i, err, false)
			continue
		}
		if want := blobs[i]; !bytes.Equal(got, want) {
			t.Errorf("content[%d] = %v, want %v", i, got, want)
		}
	}

	// verify API counts
	if got, want := srcTracker.fetch, int64(3); got != want {
		t.Errorf("count(src.Fetch()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.push, int64(0); got != want {
		t.Errorf("count(src.Push()) = %v, want %v", got, want)
	}
	if got, want := srcTracker.exists, int64(0); got != want {
		t.Errorf("count(src.Exists()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.fetch, int64(0); got != want {
		t.Errorf("count(dst.Fetch()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.push, int64(3); got != want {
		t.Errorf("count(dst.Push()) = %v, want %v", got, want)
	}
	if got, want := dstTracker.exists, int64(5); got != want {
		t.Errorf("count(dst.Exists()) = %v, want %v", got, want)
	}
}

func equalDescriptorSet(actual []ocispec.Descriptor, expected []ocispec.Descriptor) bool {
	if len(actual) != len(expected) {
		return false
	}
	contains := func(node ocispec.Descriptor) bool {
		for _, candidate := range actual {
			if reflect.DeepEqual(candidate, node) {
				return true
			}
		}
		return false
	}
	for _, node := range expected {
		if !contains(node) {
			return false
		}
	}
	return true
}

// TODO: Fetch same content/same name
