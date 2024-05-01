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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/spec"
)

func TestStorage_Success(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test fetch
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

func TestStorage_Success_Resume(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "4",
		},
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	if err := ensureDir(s.ingestRoot); err != nil {
		t.Fatal("failed to ensure ingest dir exists =", err)
	}
	ctx := context.Background()

	// Set up an existing ingest file
	ingestSize, err := strconv.ParseInt(desc.Annotations[spec.AnnotationResumeOffset], 10, 64)
	if err != nil {
		t.Fatal("error parsing resume offset =", err)
	}

	fp, err := os.CreateTemp(s.ingestRoot, desc.Digest.Encoded()+"_*")
	if err != nil {
		t.Fatal("error creating partial ingest file =", err)
	}
	defer fp.Close()

	desc.Annotations[spec.AnnotationResumeFilename] = fp.Name()

	// Fill the ingest buffer with the beginning of the content
	_, err = fp.Write(content[0:ingestSize])
	if err != nil {
		t.Fatal("error writing partial ingest file =", err)
	}

	// Test push the remainder of the content
	err = s.Push(ctx, desc, bytes.NewReader(content[ingestSize:]))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test fetch
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

func TestStorage_RelativeRoot_Success(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("error calling filepath.EvalSymlinks(), error =", err)
	}
	currDir, err := os.Getwd()
	if err != nil {
		t.Fatal("error calling Getwd(), error=", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
	s, err := NewStorage(".")
	if err != nil {
		t.Fatal("New() error =", err)
	}
	if want := tempDir; s.root != want {
		t.Errorf("Storage.root = %s, want %s", s.root, want)
	}
	// cd back to allow the temp directory to be removed
	if err := os.Chdir(currDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test fetch
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

func TestStorage_NotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
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

func TestStorage_AlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
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

func TestStorage_AlreadyExists_Resume(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "4",
		},
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	if err := ensureDir(s.ingestRoot); err != nil {
		t.Fatal("failed to ensure ingest dir exists =", err)
	}
	ctx := context.Background()

	// Set up an existing ingest file
	ingestSize, err := strconv.ParseInt(desc.Annotations[spec.AnnotationResumeOffset], 10, 64)
	if err != nil {
		t.Fatal("error parsing resume offset =", err)
	}

	fp, err := os.CreateTemp(s.ingestRoot, desc.Digest.Encoded()+"_*")
	if err != nil {
		t.Fatal("error creating partial ingest file =", err)
	}
	defer fp.Close()

	desc.Annotations[spec.AnnotationResumeFilename] = fp.Name()

	// Fill the ingest buffer with the beginning of the content
	_, err = fp.Write(content[0:ingestSize])
	if err != nil {
		t.Fatal("error writing partial ingest file =", err)
	}

	// Test push the remainder of the content
	err = s.Push(ctx, desc, bytes.NewReader(content[ingestSize:]))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Storage.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStorage_BadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Storage.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStorage_Push_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
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

func TestStorage_Fetch_ExistingBlobs(t *testing.T) {
	content := []byte("hello world")
	dgst := digest.FromBytes(content)
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    dgst,
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "blobs", dgst.Algorithm().String(), dgst.Encoded())
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}
	if err := os.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

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

func TestStorage_Fetch_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
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

func TestStorage_Delete(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()
	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}
	err = s.Delete(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Delete() error =", err)
	}
	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, false)
	}
	err = s.Delete(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("got error = %v, want %v", err, errdef.ErrNotFound)
	}
}
