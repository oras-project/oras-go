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
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
)

// TODO: add same content with file name

// // TODO: test manifest
// func Test_Push_File(t *testing.T) {
// 	content := []byte("hello world")
// 	desc := ocispec.Descriptor{
// 		MediaType: ocispec.MediaTypeImageManifest,
// 		Digest:    digest.FromBytes(content),
// 		Size:      int64(len(content)),
// 		Annotations: map[string]string{
// 			ocispec.AnnotationTitle: "hello",
// 		},
// 	}

// 	tempDir := os.TempDir()
// 	s, err := New(tempDir)
// 	// s.DisableOverwrite = true
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	defer s.Close()

// 	ctx := context.Background()
// 	err = s.Push(ctx, desc, bytes.NewReader(content))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	os.Remove(filepath.Join(tempDir, desc.Annotations[ocispec.AnnotationTitle]))

// 	rc, err := s.Fetch(ctx, desc)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	got, err := io.ReadAll(rc)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	err = rc.Close()
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	if !bytes.Equal(got, content) {
// 		t.Errorf("got = %v, want = %v", got, content)
// 	}
// }
func TestStorage_File_Push(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_Dir_Push(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	fileName := "test.txt"
	if err = ioutil.WriteFile(filepath.Join(dirPath, fileName), content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	desc, err := s.Add(dirName, "", dirPath)
	if err != nil {
		t.Fatal("Storage.Add() error=", err)
	}

	val, ok := s.dgstToPath.Load(desc.Digest)
	if !ok {
		t.Fatal("failed to find internal zip")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal zip")
	}
	zip, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal zip")
	}
	if err := zrc.Close(); err != nil {
		t.Fatal("failed to close internal zip reader")
	}

	anotherTempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(anotherTempDir)

	// test with another file store instance to mock push zip
	anotherS := newStorage(anotherTempDir)
	defer anotherS.Close()

	// test push
	if err := anotherS.Push(ctx, desc, bytes.NewReader(zip)); err != nil {
		t.Fatal("Storage.Push() error =", err)

	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, zip) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, zip)
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

func TestStorage_Manifest_Push(t *testing.T) {
	content := []byte(`{"layers":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()

	ctx := context.Background()
	// test push
	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_NoNameErr(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrNoName) {
		t.Errorf("Storage.Push() error = %v, want %v", err, errdef.ErrNoName)
	}
}

func TestStorage_IgnoreNoName_Push(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	s.IgnoreNoName = true
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_File_NotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Storage.Exists() error =", err)
	}
	if exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Storage.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStorage_File_AlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Storage.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStorage_File_BadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	err = s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Storage.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStorage_File_Add(t *testing.T) {
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

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, name)
	if err = ioutil.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(name, mediaType, path)
	if err != nil {
		t.Fatal("Storage.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc) != descriptor.FromOCI(desc) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_Dir_Add(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}

	content := []byte("hello world")
	if err = ioutil.WriteFile(filepath.Join(dirPath, "test.txt"), content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc, err := s.Add(dirName, "", dirPath)
	if err != nil {
		t.Fatal("Storage.Add() error=", err)
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	val, ok := s.dgstToPath.Load(gotDesc.Digest)
	if !ok {
		t.Fatal("failed to find internal zip")
	}
	tmpPath := val.(string)
	zrc, err := os.Open(tmpPath)
	if err != nil {
		t.Fatal("failed to open internal zip")
	}
	gotZip, err := io.ReadAll(zrc)
	if err != nil {
		t.Fatal("failed to read internal zip")
	}

	// test fetch
	rc, err := s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, gotZip) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, gotZip)
	}
}

func TestStorage_Pack(t *testing.T) {
	mediaType := "test"
	var files []FileRef

	content_1 := []byte("hello world")
	name_1 := "test_1.txt"
	desc_1 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content_1),
		Size:      int64(len(content_1)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_1,
		},
	}

	content_2 := []byte("goodbye world")
	name_2 := "test_2.txt"
	desc_2 := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content_2),
		Size:      int64(len(content_2)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name_2,
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	path_1 := filepath.Join(tempDir, name_1)
	if err = ioutil.WriteFile(path_1, content_1, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	files = append(files, FileRef{
		name:      name_1,
		mediaType: mediaType,
		path:      path_1,
	})

	path_2 := filepath.Join(tempDir, name_2)
	if err = ioutil.WriteFile(path_2, content_2, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	files = append(files, FileRef{
		name:      name_2,
		mediaType: mediaType,
		path:      path_2,
	})

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test pack
	manifestDesc, err := s.Pack(ctx, files, content.PackOpts{}, "", "config")
	if err != nil {
		t.Fatal("Storage.Pack() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc, err := s.Fetch(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}

	var manifest ocispec.Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatal("failed to decode manifest, err =", err)
	}
	if err = rc.Close(); err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}

	exists, err = s.Exists(ctx, manifest.Config)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}
}

func TestStorage_File_Add_SameContent(t *testing.T) {
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

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	path_1 := filepath.Join(tempDir, name_1)
	if err = ioutil.WriteFile(path_1, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	path_2 := filepath.Join(tempDir, name_2)
	if err = ioutil.WriteFile(path_2, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test add
	gotDesc_1, err := s.Add(name_1, mediaType, path_1)
	if err != nil {
		t.Fatal("Storage.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc_1) != descriptor.FromOCI(desc_1) {
		t.Fatal("got descriptor mismatch")
	}

	gotDesc_2, err := s.Add(name_2, mediaType, path_2)
	if err != nil {
		t.Fatal("Storage.Add() error =", err)
	}
	if descriptor.FromOCI(gotDesc_2) != descriptor.FromOCI(desc_2) {
		t.Fatal("got descriptor mismatch")
	}

	// test exists
	exists, err := s.Exists(ctx, gotDesc_1)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, gotDesc_2)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc_1, err := s.Fetch(ctx, gotDesc_1)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got_1, content)
	}

	rc_2, err := s.Fetch(ctx, gotDesc_2)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got_2, err := io.ReadAll(rc_2)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc_2.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_2, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got_2, content)
	}
}

func TestStorage_File_Push_SameContent(t *testing.T) {
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

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	// test push
	if err := s.Push(ctx, desc_1, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}
	if err := s.Push(ctx, desc_2, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test exists
	exists, err := s.Exists(ctx, desc_1)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	exists, err = s.Exists(ctx, desc_2)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	// test fetch
	rc_1, err := s.Fetch(ctx, desc_1)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got_1, err := io.ReadAll(rc_1)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc_1.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_1, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got_1, content)
	}

	rc_2, err := s.Fetch(ctx, desc_2)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got_2, err := io.ReadAll(rc_2)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc_2.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got_2, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got_2, content)
	}
}

// TODO: disable overwrite
// TODO: allow path traversal

func TestStorage_File_Push_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)
	for i := 0; i < concurrency; i++ {
		eg.Go(func(i int) func() error {
			return func() error {
				if err := s.Push(egCtx, desc, bytes.NewReader(content)); err != nil {
					if errors.Is(err, errdef.ErrAlreadyExists) {
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
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_File_Fetch_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "test.txt",
		},
	}

	tempDir, err := os.MkdirTemp("", "oras_oci_test_*")
	if err != nil {
		t.Fatal("error creating temp dir, error =", err)
	}
	defer os.RemoveAll(tempDir)

	s := newStorage(tempDir)
	defer s.Close()
	ctx := context.Background()

	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
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
					t.Fatal("Storage.Fetch().Read() error =", err)
				}
				err = rc.Close()
				if err != nil {
					t.Error("Storage.Fetch().Close() error =", err)
				}
				if !bytes.Equal(got, content) {
					t.Errorf("Storage.Fetch() = %v, want %v", got, content)
				}
				return nil
			}
		}(i))
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}
