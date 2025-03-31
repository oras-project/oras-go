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
	"crypto/sha1"
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/spec"
	"oras.land/oras-go/v2/registry"
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
	if _, ok := store.(oras.GraphTarget); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(registry.TagLister); !ok {
		t.Error("&Store{} does not conform registry.TagLister")
	}
}

func TestStore_Success(t *testing.T) {
	blob := []byte("test")
	blobDesc := content.NewDescriptorFromBytes("test", blob)
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
	ref := "foobar"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// validate layout
	layoutFilePath := filepath.Join(tempDir, ocispec.ImageLayoutFile)
	layoutFile, err := os.Open(layoutFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer layoutFile.Close()

	var layout *ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		t.Fatal("error decoding layout, error =", err)
	}
	if want := ocispec.ImageLayoutVersion; layout.Version != want {
		t.Errorf("layout.Version = %s, want %s", layout.Version, want)
	}

	// validate index.json
	indexFilePath := filepath.Join(tempDir, "index.json")
	indexFile, err := os.Open(indexFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer indexFile.Close()

	var index *ocispec.Index
	err = json.NewDecoder(indexFile).Decode(&index)
	if err != nil {
		t.Fatal("error decoding index.json, error =", err)
	}
	if want := 2; index.SchemaVersion != want {
		t.Errorf("index.SchemaVersion = %v, want %v", index.SchemaVersion, want)
	}
	if want := "application/vnd.oci.image.index.v1+json"; index.MediaType != want {
		t.Errorf("index.MediaType = %s, want %s", index.MediaType, want)
	}

	// test push blob
	err = s.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	internalResolver := s.tagResolver
	if got, want := len(internalResolver.Map()), 0; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test push manifest
	err = s.Push(ctx, manifestDesc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving blob by digest
	gotDesc, err := s.Resolve(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if want := blobDesc; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, blobDesc)
	}

	// test resolving manifest by digest
	gotDesc, err = s.Resolve(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test tag
	err = s.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 2; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving manifest by tag
	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test fetch
	exists, err := s.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, manifest) {
		t.Errorf("Store.Fetch() = %v, want %v", got, manifest)
	}
}

func TestStore_RelativeRoot_Success(t *testing.T) {
	blob := []byte("test")
	blobDesc := content.NewDescriptorFromBytes("test", blob)
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
	ref := "foobar"

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
	s, err := New(".")
	if err != nil {
		t.Fatal("New() error =", err)
	}
	if want := tempDir; s.root != want {
		t.Errorf("Store.root = %s, want %s", s.root, want)
	}
	// cd back to allow the temp directory to be removed
	if err := os.Chdir(currDir); err != nil {
		t.Fatal("error calling Chdir(), error=", err)
	}
	ctx := context.Background()

	// validate layout
	layoutFilePath := filepath.Join(tempDir, ocispec.ImageLayoutFile)
	layoutFile, err := os.Open(layoutFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer layoutFile.Close()

	var layout *ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		t.Fatal("error decoding layout, error =", err)
	}
	if want := ocispec.ImageLayoutVersion; layout.Version != want {
		t.Errorf("layout.Version = %s, want %s", layout.Version, want)
	}

	// test push blob
	err = s.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	internalResolver := s.tagResolver
	if got, want := len(internalResolver.Map()), 0; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test push manifest
	err = s.Push(ctx, manifestDesc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving blob by digest
	gotDesc, err := s.Resolve(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if want := blobDesc; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, blobDesc)
	}

	// test resolving manifest by digest
	gotDesc, err = s.Resolve(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test tag
	err = s.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 2; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving manifest by tag
	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test fetch
	exists, err := s.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, manifest) {
		t.Errorf("Store.Fetch() = %v, want %v", got, manifest)
	}
}

func TestStore_NotExistingRoot(t *testing.T) {
	tempDir := t.TempDir()
	root := filepath.Join(tempDir, "rootDir")
	_, err := New(root)
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// validate layout
	layoutFilePath := filepath.Join(root, ocispec.ImageLayoutFile)
	layoutFile, err := os.Open(layoutFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer layoutFile.Close()

	var layout *ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		t.Fatal("error decoding layout, error =", err)
	}
	if want := ocispec.ImageLayoutVersion; layout.Version != want {
		t.Errorf("layout.Version = %s, want %s", layout.Version, want)
	}

	// validate index.json
	indexFilePath := filepath.Join(root, "index.json")
	indexFile, err := os.Open(indexFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer indexFile.Close()

	var index *ocispec.Index
	err = json.NewDecoder(indexFile).Decode(&index)
	if err != nil {
		t.Fatal("error decoding index.json, error =", err)
	}
	if want := 2; index.SchemaVersion != want {
		t.Errorf("index.SchemaVersion = %v, want %v", index.SchemaVersion, want)
	}
}

func TestStore_ContentNotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
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

func TestStore_ContentAlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStore_ContentBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStore_ResolveByTagReturnsFullDescriptor(t *testing.T) {
	content := []byte("hello world")
	ref := "hello-world:0.0.1"
	annotations := map[string]string{"name": "Hello"}
	desc := ocispec.Descriptor{
		MediaType:   "test",
		Digest:      digest.FromBytes(content),
		Size:        int64(len(content)),
		Annotations: annotations,
	}

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	resolvedDescr, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	if !reflect.DeepEqual(resolvedDescr, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", resolvedDescr, desc)
	}
}

func TestStore_ResolveByDigestReturnsPlainDescriptor(t *testing.T) {
	content := []byte("hello world")
	ref := "hello-world:0.0.1"
	desc := ocispec.Descriptor{
		MediaType:   "test",
		Digest:      digest.FromBytes(content),
		Size:        int64(len(content)),
		Annotations: map[string]string{"name": "Hello"},
	}
	plainDescriptor := descriptor.Plain(desc)

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	resolvedDescr, err := s.Resolve(ctx, string(desc.Digest))
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	if !reflect.DeepEqual(resolvedDescr, plainDescriptor) {
		t.Errorf("Store.Resolve() = %v, want %v", resolvedDescr, plainDescriptor)
	}
}

func TestStore_TagNotFound(t *testing.T) {
	ref := "foobar"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	_, err = s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_TagUnknownContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Tag(ctx, desc, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStore_DisableAutoSaveIndex(t *testing.T) {
	content := []byte(`{"layers":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref0 := "foobar"
	ref1 := "barfoo"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	// disable auto save
	s.AutoSaveIndex = false
	ctx := context.Background()

	// validate layout
	layoutFilePath := filepath.Join(tempDir, ocispec.ImageLayoutFile)
	layoutFile, err := os.Open(layoutFilePath)
	if err != nil {
		t.Errorf("error opening layout file, error = %v", err)
	}
	defer layoutFile.Close()

	var layout *ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		t.Fatal("error decoding layout, error =", err)
	}
	if want := ocispec.ImageLayoutVersion; layout.Version != want {
		t.Errorf("layout.Version = %s, want %s", layout.Version, want)
	}

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	internalResolver := s.tagResolver
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving by digest
	gotDesc, err := s.Resolve(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// test tag
	err = s.Tag(ctx, desc, ref0)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	err = s.Tag(ctx, desc, ref1)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 3; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}

	// test resolving by digest
	gotDesc, err = s.Resolve(ctx, ref0)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// test index file
	if got, want := len(s.index.Manifests), 0; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	if err := s.saveIndex(); err != nil {
		t.Fatal("Store.SaveIndex() error =", err)
	}
	// test index file again
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	if _, err := os.Stat(s.indexPath); err != nil {
		t.Errorf("error: %s does not exist", s.indexPath)
	}

	// test untag
	err = s.Untag(ctx, ref0)
	if err != nil {
		t.Fatal("Store.Untag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 2; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	if err := s.saveIndex(); err != nil {
		t.Fatal("Store.SaveIndex() error =", err)
	}
	// test index file again
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
}

func TestStore_RepeatTag(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()
	ref := "foobar"

	// get internal resolver
	internalResolver := s.tagResolver

	// first tag a manifest
	manifest := []byte(`{"layers":[]}`)
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
	err = s.Push(ctx, desc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("len(resolver.Map()) = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 2; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	gotDesc, err := s.Resolve(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// tag another manifest
	manifest = []byte(`{"layers":[], "annotations":{}}`)
	desc = content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
	err = s.Push(ctx, desc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 3; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 3; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	gotDesc, err = s.Resolve(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// tag a blob
	blob := []byte("foobar")
	desc = content.NewDescriptorFromBytes("test", blob)
	err = s.Push(ctx, desc, bytes.NewReader(blob))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 3; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 4; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 3; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	gotDesc, err = s.Resolve(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	// tag another blob
	blob = []byte("barfoo")
	desc = content.NewDescriptorFromBytes("test", blob)
	err = s.Push(ctx, desc, bytes.NewReader(blob))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 4; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 3; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 5; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 4; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}

	gotDesc, err = s.Resolve(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
}

// Related bug: https://github.com/oras-project/oras-go/issues/461
func TestStore_TagByDigest(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// get internal resolver
	internalResolver := s.tagResolver

	manifest := []byte(`{"layers":[]}`)
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)

	// push a manifest
	err = s.Push(ctx, manifestDesc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("len(resolver.Map()) = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	gotDesc, err := s.Resolve(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// tag manifest by digest
	err = s.Tag(ctx, manifestDesc, manifestDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("len(resolver.Map()) = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	gotDesc, err = s.Resolve(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// push a blob
	blob := []byte("foobar")
	blobDesc := content.NewDescriptorFromBytes("test", blob)
	err = s.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	if got, want := len(internalResolver.Map()), 1; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 1; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	gotDesc, err = s.Resolve(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, blobDesc)
	}

	// tag blob by digest
	err = s.Tag(ctx, blobDesc, blobDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	if got, want := len(internalResolver.Map()), 2; got != want {
		t.Errorf("resolver.Map() = %v, want %v", got, want)
	}
	if got, want := len(s.index.Manifests), 2; got != want {
		t.Errorf("len(index.Manifests) = %v, want %v", got, want)
	}
	gotDesc, err = s.Resolve(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, blobDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, blobDesc)
	}
}

func TestStore_BadIndex(t *testing.T) {
	tempDir := t.TempDir()
	content := []byte("whatever")
	path := filepath.Join(tempDir, "index.json")
	os.WriteFile(path, content, 0666)

	_, err := New(tempDir)
	if err == nil {
		t.Errorf("New() error = nil, want: error")
	}
}

func TestStore_BadLayout(t *testing.T) {
	tempDir := t.TempDir()
	content := []byte("whatever")
	path := filepath.Join(tempDir, ocispec.ImageLayoutFile)
	os.WriteFile(path, content, 0666)

	_, err := New(tempDir)
	if err == nil {
		t.Errorf("New() error = nil, want: error")
	}
}

func TestStore_Predecessors(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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
		var manifest spec.Artifact
		manifest.Subject = &subject
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
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

	// verify predecessors
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
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestStore_ExistingStore(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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
		var manifest spec.Artifact
		manifest.Subject = &subject
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
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

	ctx := context.Background()
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

	// tag index root
	indexRoot := descs[10]
	tag := "latest"
	if err := s.Tag(ctx, indexRoot, tag); err != nil {
		t.Fatal("Tag() error =", err)
	}
	// tag index root by digest
	// related bug: https://github.com/oras-project/oras-go/issues/461
	if err := s.Tag(ctx, indexRoot, indexRoot.Digest.String()); err != nil {
		t.Fatal("Tag() error =", err)
	}

	// test with another OCI store instance to mock loading from an existing store
	anotherS, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// test resolving index root by tag
	gotDesc, err := anotherS.Resolve(ctx, tag)
	if err != nil {
		t.Fatal("Store: Resolve() error =", err)
	}
	if !content.Equal(gotDesc, indexRoot) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, indexRoot)
	}

	// test resolving index root by digest
	gotDesc, err = anotherS.Resolve(ctx, indexRoot.Digest.String())
	if err != nil {
		t.Fatal("Store: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexRoot) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, indexRoot)
	}

	// test resolving artifact manifest by digest
	artifactRootDesc := descs[12]
	gotDesc, err = anotherS.Resolve(ctx, artifactRootDesc.Digest.String())
	if err != nil {
		t.Fatal("Store: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, artifactRootDesc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, artifactRootDesc)
	}

	// test resolving blob by digest
	gotDesc, err = anotherS.Resolve(ctx, descs[0].Digest.String())
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if want := descs[0]; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, want)
	}

	// test fetching OCI root index
	exists, err := anotherS.Exists(ctx, indexRoot)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	// test fetching blobs
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				rc, err := s.Fetch(egCtx, descs[i])
				if err != nil {
					return fmt.Errorf("Store.Fetch(%d) error = %v", i, err)
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					return fmt.Errorf("Store.Fetch(%d).Read() error = %v", i, err)
				}
				err = rc.Close()
				if err != nil {
					return fmt.Errorf("Store.Fetch(%d).Close() error = %v", i, err)
				}
				if !bytes.Equal(got, blobs[i]) {
					return fmt.Errorf("Store.Fetch(%d) = %v, want %v", i, got, blobs[i])
				}
				return nil
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	// verify predecessors
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
		nil,                   // Blob 12, no predecessors
		{descs[14]},           // Blob 13
		nil,                   // Blob 14, no predecessors
	}
	for i, want := range wants {
		predecessors, err := anotherS.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func Test_ExistingStore_Retag(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	manifest_1 := []byte(`{"layers":[]}`)
	manifestDesc_1 := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest_1)
	manifestDesc_1.Annotations = map[string]string{"key1": "val1"}

	// push a manifest
	err = s.Push(ctx, manifestDesc_1, bytes.NewReader(manifest_1))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	// tag manifest by digest
	err = s.Tag(ctx, manifestDesc_1, manifestDesc_1.Digest.String())
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}
	// tag manifest by tag
	ref := "foobar"
	err = s.Tag(ctx, manifestDesc_1, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	// verify index
	want := []ocispec.Descriptor{
		{
			MediaType: manifestDesc_1.MediaType,
			Digest:    manifestDesc_1.Digest,
			Size:      manifestDesc_1.Size,
			Annotations: map[string]string{
				"key1":                    "val1",
				ocispec.AnnotationRefName: ref,
			},
		},
	}
	if got := s.index.Manifests; !equalDescriptorSet(got, want) {
		t.Errorf("index.Manifests = %v, want %v", got, want)
	}

	// test with another OCI store instance to mock loading from an existing store
	anotherS, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	manifest_2 := []byte(`{"layers":[], "annotations":{}}`)
	manifestDesc_2 := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest_2)
	manifestDesc_2.Annotations = map[string]string{"key2": "val2"}

	err = anotherS.Push(ctx, manifestDesc_2, bytes.NewReader(manifest_2))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	err = anotherS.Tag(ctx, manifestDesc_2, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	// verify index
	want = []ocispec.Descriptor{
		{
			MediaType: manifestDesc_1.MediaType,
			Digest:    manifestDesc_1.Digest,
			Size:      manifestDesc_1.Size,
			Annotations: map[string]string{
				"key1": "val1",
			},
		},
		{
			MediaType: manifestDesc_2.MediaType,
			Digest:    manifestDesc_2.Digest,
			Size:      manifestDesc_2.Size,
			Annotations: map[string]string{
				"key2":                    "val2",
				ocispec.AnnotationRefName: ref,
			},
		},
	}
	if got := anotherS.index.Manifests; !equalDescriptorSet(got, want) {
		t.Errorf("index.Manifests = %v, want %v", got, want)
	}
}

func TestCopy_MemoryToOCI_FullCopy(t *testing.T) {
	src := memory.New()

	tempDir := t.TempDir()
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("OCI.New() error =", err)
	}

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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
	err = src.Tag(ctx, root, ref)
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

func TestCopyGraph_MemoryToOCI_FullCopy(t *testing.T) {
	src := cas.NewMemory()

	tempDir := t.TempDir()
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("OCI.New() error =", err)
	}

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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

func TestCopyGraph_MemoryToOCI_PartialCopy(t *testing.T) {
	src := cas.NewMemory()

	tempDir := t.TempDir()
	dst, err := New(tempDir)
	if err != nil {
		t.Fatal("OCI.New() error =", err)
	}

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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

func TestCopyGraph_OCIToMemory_FullCopy(t *testing.T) {
	tempDir := t.TempDir()
	src, err := New(tempDir)
	if err != nil {
		t.Fatal("OCI.New() error =", err)
	}

	dst := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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

func TestCopyGraph_OCIToMemory_PartialCopy(t *testing.T) {
	tempDir := t.TempDir()
	src, err := New(tempDir)
	if err != nil {
		t.Fatal("OCI.New() error =", err)
	}

	dst := cas.NewMemory()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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

func TestStore_Tags(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		// add annotation to make each manifest unique
		manifest.Annotations = map[string]string{
			"blob_index": strconv.Itoa(len(blobs)),
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	tagManifest := func(desc ocispec.Descriptor, ref string) {
		if err := s.Tag(ctx, desc, ref); err != nil {
			t.Fatal("Store.Tag() error =", err)
		}
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foobar"))  // Blob 1
	generateManifest(descs[0], descs[1])                       // Blob 2
	generateManifest(descs[0], descs[1])                       // Blob 3
	generateManifest(descs[0], descs[1])                       // Blob 4
	generateManifest(descs[0], descs[1])                       // Blob 5
	generateManifest(descs[0], descs[1])                       // Blob 6

	for i := range blobs {
		err := s.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content: %d: %v", i, err)
		}
	}

	tagManifest(descs[3], "v2")
	tagManifest(descs[4], "v3")
	tagManifest(descs[5], "v1")
	tagManifest(descs[6], "v4")

	// test tags
	tests := []struct {
		name string
		last string
		want []string
	}{
		{
			name: "list all tags",
			want: []string{"v1", "v2", "v3", "v4"},
		},
		{
			name: "list from middle",
			last: "v2",
			want: []string{"v3", "v4"},
		},
		{
			name: "list from end",
			last: "v4",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Tags(ctx, tt.last, func(got []string) error {
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("Store.Tags() = %v, want %v", got, tt.want)
				}
				return nil
			}); err != nil {
				t.Errorf("Store.Tags() error = %v", err)
			}
		})
	}

	wantErr := errors.New("expected error")
	if err := s.Tags(ctx, "", func(got []string) error {
		return wantErr
	}); err != wantErr {
		t.Errorf("Store.Tags() error = %v, wantErr %v", err, wantErr)
	}
}

func TestStore_BasicDelete(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test-delete",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "latest"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("NewDeletableStore() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	resolvedDescr, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	if !reflect.DeepEqual(resolvedDescr, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", resolvedDescr, desc)
	}

	err = s.Delete(ctx, desc)
	if err != nil {
		t.Errorf("Store.Delete() = %v, wantErr %v", err, nil)
	}

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}
}

func TestStore_FetchAndDelete(t *testing.T) {
	// create a store
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("error =", err)
	}

	// push a content
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test-delete",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	err = s.Push(context.Background(), desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("error =", err)
	}

	// fetch a content
	rc, err := s.Fetch(context.Background(), desc)
	if err != nil {
		t.Fatal("error =", err)
	}

	// read and verify the content
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", string(got), string(content))
	}
	rc.Close()

	// delete. If rc is not closed, Delete would fail on some systems.
	err = s.Delete(context.Background(), desc)
	if err != nil {
		t.Fatal("error =", err)
	}
}

func TestStore_PredecessorsAndDelete(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	s.AutoGC = false
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
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

	// verify predecessors
	wants := [][]ocispec.Descriptor{
		descs[4:7],           // Blob 0
		{descs[4], descs[6]}, // Blob 1
		{descs[4], descs[6]}, // Blob 2
		{descs[5], descs[6]}, // Blob 3
		{descs[7]},           // Blob 4
		{descs[7]},           // Blob 5
		{descs[8]},           // Blob 6
		nil,                  // Blob 7
		nil,                  // Blob 8
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}

	// delete a node and verify the result
	s.Delete(egCtx, descs[6])
	// verify predecessors
	wants = [][]ocispec.Descriptor{
		descs[4:6], // Blob 0
		{descs[4]}, // Blob 1
		{descs[4]}, // Blob 2
		{descs[5]}, // Blob 3
		{descs[7]}, // Blob 4
		{descs[7]}, // Blob 5
		{descs[8]}, // Blob 6
		nil,        // Blob 7
		nil,        // Blob 8
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}

	// delete a node and verify the result
	s.Delete(egCtx, descs[8])
	// verify predecessors
	wants = [][]ocispec.Descriptor{
		descs[4:6], // Blob 0
		{descs[4]}, // Blob 1
		{descs[4]}, // Blob 2
		{descs[5]}, // Blob 3
		{descs[7]}, // Blob 4
		{descs[7]}, // Blob 5
		nil,        // Blob 6
		nil,        // Blob 7
		nil,        // Blob 8
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}

	// delete a node and verify the result
	s.Delete(egCtx, descs[5])
	// verify predecessors
	wants = [][]ocispec.Descriptor{
		{descs[4]}, // Blob 0
		{descs[4]}, // Blob 1
		{descs[4]}, // Blob 2
		nil,        // Blob 3
		{descs[7]}, // Blob 4
		{descs[7]}, // Blob 5
		nil,        // Blob 6
		nil,        // Blob 7
		nil,        // Blob 8
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestStore_DeleteWithAutoGC(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config:  config,
			Subject: subject,
			Layers:  layers,
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
	generateManifest(descs[0], nil, descs[1])                  // Blob 4
	generateManifest(descs[0], nil, descs[2])                  // Blob 5
	generateManifest(descs[0], nil, descs[3])                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	appendBlob(ocispec.MediaTypeImageLayer, []byte("world"))   // Blob 9
	generateManifest(descs[0], &descs[6], descs[9])            // Blob 10
	generateManifest(descs[0], &descs[10], descs[2])           // Blob 11

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

	// delete blob 4 and verify the result
	if err := s.Delete(egCtx, descs[4]); err != nil {
		t.Fatal(err)
	}

	// blob 1 and 4 are now deleted, and other blobs are still present
	notPresent := []ocispec.Descriptor{descs[1], descs[4]}
	for _, node := range notPresent {
		if exists, _ := s.Exists(egCtx, node); exists {
			t.Errorf("%v should not exist in store", node)
		}
	}
	stillPresent := []ocispec.Descriptor{descs[0], descs[2], descs[3], descs[5], descs[6], descs[7], descs[8], descs[9], descs[10], descs[11]}
	for _, node := range stillPresent {
		if exists, _ := s.Exists(egCtx, node); !exists {
			t.Errorf("%v should exist in store", node)
		}
	}

	// delete blob 8 and verify the result
	if err := s.Delete(egCtx, descs[8]); err != nil {
		t.Fatal(err)
	}

	// blob 1, 4 and 8 are now deleted, and other blobs are still present
	notPresent = []ocispec.Descriptor{descs[1], descs[4], descs[8]}
	for _, node := range notPresent {
		if exists, _ := s.Exists(egCtx, node); exists {
			t.Errorf("%v should not exist in store", node)
		}
	}
	stillPresent = []ocispec.Descriptor{descs[0], descs[2], descs[3], descs[5], descs[6], descs[7], descs[9], descs[10], descs[11]}
	for _, node := range stillPresent {
		if exists, _ := s.Exists(egCtx, node); !exists {
			t.Errorf("%v should exist in store", node)
		}
	}

	// delete blob 6 and verify the result
	if err := s.Delete(egCtx, descs[6]); err != nil {
		t.Fatal(err)
	}

	// blob 1, 3, 4, 6, 8, 9, 10, 11 are now deleted, and other blobs are still present
	notPresent = []ocispec.Descriptor{descs[1], descs[3], descs[4], descs[6], descs[8], descs[9], descs[10], descs[11]}
	for _, node := range notPresent {
		if exists, _ := s.Exists(egCtx, node); exists {
			t.Errorf("%v should not exist in store", node)
		}
	}
	stillPresent = []ocispec.Descriptor{descs[0], descs[2], descs[5], descs[7]}
	for _, node := range stillPresent {
		if exists, _ := s.Exists(egCtx, node); !exists {
			t.Errorf("%v should exist in store", node)
		}
	}

	// verify predecessors information
	wants := [][]ocispec.Descriptor{
		{descs[5]}, // Blob 0
		nil,        // Blob 1
		{descs[5]}, // Blob 2
		nil,        // Blob 3
		{descs[7]}, // Blob 4's predecessor is descs[7], even though blob 4 no longer exist
		{descs[7]}, // Blob 5
		nil,        // Blob 6
		nil,        // Blob 7
		nil,        // Blob 8
		nil,        // Blob 9
		nil,        // Blob 10
		nil,        // Blob 11
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestStore_Untag(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test-delete",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "latest"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("NewDeletableStore() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	resolvedDescr, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	if !reflect.DeepEqual(resolvedDescr, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", resolvedDescr, desc)
	}

	err = s.Untag(ctx, ref)
	if err != nil {
		t.Errorf("Store.Untag() = %v, wantErr %v", err, nil)
	}

	_, err = s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}
}

func TestStore_UntagErrorPath(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test-delete",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "latest"

	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("NewDeletableStore() error =", err)
	}
	ctx := context.Background()

	err = s.Untag(ctx, "")
	if !errors.Is(err, errdef.ErrMissingReference) {
		t.Errorf("Store.Untag() error = %v, wantErr %v", err, errdef.ErrMissingReference)
	}

	err = s.Untag(ctx, "foobar")
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Untag() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	resolvedDescr, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	err = s.Untag(ctx, resolvedDescr.Digest.String())
	if !errors.Is(err, errdef.ErrInvalidReference) {
		t.Errorf("Store.Untag() error = %v, wantErr %v", err, errdef.ErrInvalidReference)
	}
}

func TestStore_GC(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config:  config,
			Subject: subject,
			Layers:  layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateImageIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	generateArtifactManifest := func(blobs ...ocispec.Descriptor) {
		var manifest spec.Artifact
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config"))          // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob"))             // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("dangling layer"))   // Blob 2, dangling layer
	generateManifest(descs[0], nil, descs[1])                           // Blob 3, valid manifest
	generateManifest(descs[0], &descs[3], descs[1])                     // Blob 4, referrer of a valid manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("dangling layer 2")) // Blob 5, dangling layer
	generateArtifactManifest(descs[4])                                  // blob 6, dangling artifact
	generateManifest(descs[0], &descs[5], descs[1])                     // Blob 7, referrer of a dangling manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("dangling layer 3")) // Blob 8, dangling layer
	generateArtifactManifest(descs[6])                                  // blob 9, dangling artifact
	generateImageIndex(descs[7], descs[5])                              // blob 10, dangling image index
	appendBlob(ocispec.MediaTypeImageLayer, []byte("garbage layer 1"))  // Blob 11, garbage layer 1
	generateManifest(descs[0], nil, descs[4])                           // Blob 12, garbage manifest 1
	appendBlob(ocispec.MediaTypeImageConfig, []byte("garbage config"))  // Blob 13, garbage config
	appendBlob(ocispec.MediaTypeImageLayer, []byte("garbage layer 2"))  // Blob 14, garbage layer 2
	generateManifest(descs[6], nil, descs[7])                           // Blob 15, garbage manifest 2
	generateManifest(descs[0], &descs[13], descs[1])                    // Blob 16, referrer of a garbage manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("another layer"))    // Blob 17, untagged manifest
	generateManifest(descs[0], nil, descs[17])                          // Blob 18, valid untagged manifest

	// push blobs 0 - blobs 10 into s
	for i := 0; i <= 10; i++ {
		err := s.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// push blobs 17 - blobs 18 into s
	for i := 17; i <= 18; i++ {
		err := s.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// remove blobs 5 - blobs 10 from index.json
	for i := 5; i <= 10; i++ {
		s.tagResolver.Untag(string(descs[i].Digest))
	}
	s.SaveIndex()

	// push blobs 11 - blobs 16 into s.storage, making them garbage as their metadata
	// doesn't exist in s
	for i := 11; i < 17; i++ {
		err := s.storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// confirm that all the blobs are in the storage
	for i := 0; i < len(blobs); i++ {
		exists, err := s.Exists(ctx, descs[i])
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("descs[%d] should exist", i)
		}
	}

	// tag manifest blob 3
	s.Tag(ctx, descs[3], "latest")

	// perform double GC
	if err = s.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.GC(ctx); err != nil {
		t.Fatal(err)
	}

	// verify existence
	wantExistence := []bool{true, true, false, true, true, false, false, false,
		false, false, false, false, false, false, false, false, false, false, false}
	for i, wantValue := range wantExistence {
		exists, err := s.Exists(ctx, descs[i])
		if err != nil {
			t.Fatal(err)
		}
		if exists != wantValue {
			t.Fatalf("want existence %d to be %v, got %v", i, wantValue, exists)
		}
	}
}

func TestStore_GCAndDeleteOnIndex(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, subject *ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config:  config,
			Subject: subject,
			Layers:  layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateImageIndex := func(manifests ...ocispec.Descriptor) {
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
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob1"))   // Blob 1
	generateManifest(descs[0], nil, descs[1])                  // Blob 2, manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob2"))   // Blob 3
	generateManifest(descs[0], nil, descs[3])                  // Blob 4, manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob3"))   // Blob 5
	generateManifest(descs[0], nil, descs[5])                  // Blob 6, manifest
	appendBlob(ocispec.MediaTypeImageLayer, []byte("blob4"))   // Blob 7
	generateManifest(descs[0], nil, descs[7])                  // Blob 8, manifest
	generateImageIndex(descs[2], descs[4], descs[6], descs[8]) // blob 9, image index

	// push all blobs into the store
	for i := 0; i < len(blobs); i++ {
		err := s.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Errorf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// confirm that all the blobs are in the storage
	for i := 0; i < len(blobs); i++ {
		exists, err := s.Exists(ctx, descs[i])
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("descs[%d] should exist", i)
		}
	}

	// tag manifest blob 8
	s.Tag(ctx, descs[8], "latest")

	// delete the image index
	if err := s.Delete(ctx, descs[9]); err != nil {
		t.Fatal(err)
	}

	// verify existence
	wantExistence := []bool{true, false, false, false, false, false, false, true, true, false}
	for i, wantValue := range wantExistence {
		exists, err := s.Exists(ctx, descs[i])
		if err != nil {
			t.Fatal(err)
		}
		if exists != wantValue {
			t.Fatalf("want existence %d to be %v, got %v", i, wantValue, exists)
		}
	}
}

func TestStore_GCErrorPath(t *testing.T) {
	tempDir := t.TempDir()
	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	appendBlob(ocispec.MediaTypeImageLayer, []byte("valid blob")) // Blob 0

	// push the valid blob
	err = s.Push(ctx, descs[0], bytes.NewReader(blobs[0]))
	if err != nil {
		t.Error("failed to push test content to src")
	}

	// write random contents
	algPath := path.Join(tempDir, "blobs")
	dgstPath := path.Join(algPath, "sha256")
	if err := os.WriteFile(path.Join(algPath, "other"), []byte("random"), 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	if err := os.WriteFile(path.Join(dgstPath, "other2"), []byte("random2"), 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	// perform GC
	if err = s.GC(ctx); err != nil {
		t.Fatal(err)
	}

	appendBlob(ocispec.MediaTypeImageLayer, []byte("valid blob 2")) // Blob 1

	// push the valid blob
	err = s.Push(ctx, descs[1], bytes.NewReader(blobs[1]))
	if err != nil {
		t.Error("failed to push test content to src")
	}

	// unknown algorithm
	if err := os.Mkdir(path.Join(algPath, "sha666"), 0777); err != nil {
		t.Fatal(err)
	}
	if err = s.GC(ctx); err != nil {
		t.Fatal("this error should be silently ignored")
	}

	// os.Remove() error
	badDigest := digest.FromBytes([]byte("bad digest")).Encoded()
	badPath := path.Join(algPath, "sha256", badDigest)
	if err := os.Mkdir(badPath, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path.Join(badPath, "whatever"), []byte("extra content"), 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	if err = s.GC(ctx); err == nil {
		t.Fatal("expect an error when os.Remove()")
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

func Test_isContextDone(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	if err := isContextDone(ctx); err != nil {
		t.Errorf("expect error = %v, got %v", nil, err)
	}
	cancel()
	if err := isContextDone(ctx); err != context.Canceled {
		t.Errorf("expect error = %v, got %v", context.Canceled, err)
	}
}

func TestStore_BadDigest(t *testing.T) {
	data := []byte("hello world")
	ref := "foobar"

	t.Run("invalid digest", func(t *testing.T) {
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    "invalid-digest",
		}

		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal("Store.New() error =", err)
		}
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Exists(ctx, desc); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})

	t.Run("unsupported digest (sha1)", func(t *testing.T) {
		h := sha1.New()
		h.Write(data)
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		}

		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal("Store.New() error =", err)
		}
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Exists(ctx, desc); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrInvalidDigest) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrInvalidDigest)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})
}
