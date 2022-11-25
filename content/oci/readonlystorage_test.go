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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

func TestReadOnlyStorage_Exists(t *testing.T) {
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("", blob)
	fsys := fstest.MapFS{
		strings.Join([]string{"blobs", dgst.Algorithm().String(), dgst.Encoded()}, "/"): {},
	}
	s := NewStorageFromFS(fsys)
	ctx := context.Background()

	// test sha256
	got, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if want := true; got != want {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", got, want)
	}

	// test not found
	blob = []byte("whaterver")
	desc = content.NewDescriptorFromBytes("", blob)
	got, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if want := false; got != want {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", got, want)
	}

	// test invalid digest
	desc = ocispec.Descriptor{Digest: "not a digest"}
	_, err = s.Exists(ctx, desc)
	if err == nil {
		t.Fatalf("ReadOnlyStorage.Exists() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStorage_Fetch(t *testing.T) {
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("", blob)
	fsys := fstest.MapFS{
		strings.Join([]string{"blobs", dgst.Algorithm().String(), dgst.Encoded()}, "/"): {
			Data: blob,
		},
	}

	s := NewStorageFromFS(fsys)
	ctx := context.Background()

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("ReadOnlyStorage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("ReadOnlyStorage.Fetch() = %v, want %v", got, blob)
	}

	// test not found
	anotherBlob := []byte("whatever")
	desc = content.NewDescriptorFromBytes("", anotherBlob)
	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("ReadOnlyStorage.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test invalid digest
	desc = ocispec.Descriptor{Digest: "not a digest"}
	_, err = s.Fetch(ctx, desc)
	if err == nil {
		t.Fatalf("ReadOnlyStorage.Fetch() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStorage_DirFS(t *testing.T) {
	tempDir := t.TempDir()
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("test", blob)
	// write blob to disk
	path := filepath.Join(tempDir, "blobs", dgst.Algorithm().String(), dgst.Encoded())
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}
	if err := ioutil.WriteFile(path, blob, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := NewStorageFromFS(os.DirFS(tempDir))
	ctx := context.Background()

	// test Exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", exists, true)
	}

	// test Fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("ReadOnlyStorage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("ReadOnlyStorage.Fetch() = %v, want %v", got, blob)
	}
}
