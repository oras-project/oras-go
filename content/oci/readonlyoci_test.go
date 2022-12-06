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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

func TestReadonlyStoreInterface(t *testing.T) {
	var store interface{} = &ReadOnlyStore{}
	if _, ok := store.(oras.ReadOnlyGraphTarget); !ok {
		t.Error("&Store{} does not conform oras.ReadOnlyGraphTarget")
	}
}

func TestReadOnlyStore(t *testing.T) {
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
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    config,
			Layers:    layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		manifest := ocispec.Artifact{
			MediaType: ocispec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foobar"))  // Blob 1
	generateManifest(descs[0], descs[1])                       // Blob 2
	generateArtifactManifest(descs[2])                         // Blob 3
	subjectTag := "subject"
	subjectWithRef := descs[2]
	subjectWithRef.Annotations = map[string]string{ocispec.AnnotationRefName: subjectTag}
	artifactTag := "foobar"
	artifactWithRef := descs[3]
	artifactWithRef.Annotations = map[string]string{ocispec.AnnotationRefName: artifactTag}

	layout := ocispec.ImageLayout{
		Version: ocispec.ImageLayoutVersion,
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		t.Fatalf("failed to marshal OCI layout: %v", err)
	}
	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value
		},
		Manifests: []ocispec.Descriptor{
			{
				MediaType: descs[2].MediaType,
				Digest:    descs[2].Digest,
				Size:      descs[2].Size,
				Annotations: map[string]string{
					ocispec.AnnotationRefName: subjectTag,
				},
			},
			{
				MediaType: descs[3].MediaType,
				Digest:    descs[3].Digest,
				Size:      descs[3].Size,
				Annotations: map[string]string{
					ocispec.AnnotationRefName: artifactTag,
				},
			},
		},
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}

	// build fs
	fsys := fstest.MapFS{}
	for i, desc := range descs {
		path := strings.Join([]string{"blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded()}, "/")
		fsys[path] = &fstest.MapFile{Data: blobs[i]}
	}
	fsys[ocispec.ImageLayoutFile] = &fstest.MapFile{Data: layoutJSON}
	fsys[ociImageIndexFile] = &fstest.MapFile{Data: indexJSON}

	// test read-only store
	ctx := context.Background()
	s, err := NewFromFS(ctx, fsys)
	if err != nil {
		t.Fatal("NewFromFS() error =", err)
	}

	// test resolve subject tag
	gotDesc, err := s.Resolve(ctx, subjectTag)
	if err != nil {
		t.Error("ReadOnlyReadOnlyStore.Resolve() error =", err)
	}
	if want := subjectWithRef; !reflect.DeepEqual(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// test fetching blobs
	eg, egCtx := errgroup.WithContext(ctx)
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				rc, err := s.Fetch(egCtx, descs[i])
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d) error = %v", i, err)
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d).Read() error = %v", i, err)
				}
				err = rc.Close()
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d).Close() error = %v", i, err)
				}
				if !bytes.Equal(got, blobs[i]) {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d) = %v, want %v", i, got, blobs[i])
				}
				return nil
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	// test predecessors
	wants := [][]ocispec.Descriptor{
		{subjectWithRef},  // blob 0
		{subjectWithRef},  // blob 1
		{artifactWithRef}, // blob 2,
		{},                // blob 3
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("ReadOnlyStore.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("ReadOnlyStore.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestReadOnlyStore_DirFS(t *testing.T) {
	tempDir := t.TempDir()
	// build an OCI layout on disk
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
		var manifest ocispec.Artifact
		manifest.Subject = &subject
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeArtifactManifest, manifestJSON)
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

	// test artifact root node
	artifactRootDesc := descs[12]
	// tag artifact manifest by digest
	dgstRef := "@" + artifactRootDesc.Digest.String()
	artifactRootDescWithRef := artifactRootDesc
	artifactRootDescWithRef.Annotations = map[string]string{
		ocispec.AnnotationRefName: dgstRef,
	}
	if err := s.Tag(ctx, artifactRootDesc, dgstRef); err != nil {
		t.Fatal("ReadOnlyStore.Tag(artifactRootDesc) error =", err)
	}

	// OCI root node
	ociRootRef := "root"
	ociRootIndex := ocispec.Index{
		Manifests: []ocispec.Descriptor{
			descs[7],  // can reach descs[0:6] and descs[7]
			descs[14], // can reach descs[0:10], descs[13] and descs[14]
		},
	}
	ociRootIndexJSON, err := json.Marshal(ociRootIndex)
	if err != nil {
		t.Fatal("json.Marshal() error =", err)
	}
	ociRootIndexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(ociRootIndexJSON),
		Size:      int64(len(ociRootIndexJSON)),
	}
	if err := s.Push(ctx, ociRootIndexDesc, bytes.NewReader(ociRootIndexJSON)); err != nil {
		t.Fatal("ReadOnlyStore.Push(ociRootIndex) error =", err)
	}
	if err := s.Tag(ctx, ociRootIndexDesc, ociRootRef); err != nil {
		t.Fatal("ReadOnlyStore.Tag(ociRootIndex) error =", err)
	}

	// test read-only store
	readonlyS, err := NewFromFS(ctx, os.DirFS(tempDir))
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// test resolving artifact manifest
	gotDesc, err := readonlyS.Resolve(ctx, dgstRef)
	if err != nil {
		t.Fatal("AnotherStore: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, artifactRootDescWithRef) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, artifactRootDescWithRef)
	}

	// verify OCI root index
	gotDesc, err = readonlyS.Resolve(ctx, ociRootRef)
	if err != nil {
		t.Fatal("AnotherStore: Resolve() error =", err)
	}

	rootIndexDescWithRef := ociRootIndexDesc
	rootIndexDescWithRef.Annotations = map[string]string{
		ocispec.AnnotationRefName: ociRootRef,
	}
	if !reflect.DeepEqual(gotDesc, rootIndexDescWithRef) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, rootIndexDescWithRef)
	}

	// test fetching OCI root index
	exists, err := readonlyS.Exists(ctx, rootIndexDescWithRef)
	if err != nil {
		t.Fatal("ReadOnlyStore.Exists() error =", err)
	}
	if !exists {
		t.Errorf("ReadOnlyStore.Exists() = %v, want %v", exists, true)
	}

	rc, err := readonlyS.Fetch(ctx, rootIndexDescWithRef)
	if err != nil {
		t.Fatal("ReadOnlyStore.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("ReadOnlyStore.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("ReadOnlyStore.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, ociRootIndexJSON) {
		t.Errorf("ReadOnlyStore.Fetch() = %v, want %v", got, ociRootIndexJSON)
	}

	// test fetching blobs
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				rc, err := s.Fetch(egCtx, descs[i])
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d) error = %v", i, err)
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d).Read() error = %v", i, err)
				}
				err = rc.Close()
				if err != nil {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d).Close() error = %v", i, err)
				}
				if !bytes.Equal(got, blobs[i]) {
					return fmt.Errorf("ReadOnlyStore.Fetch(%d) = %v, want %v", i, got, blobs[i])
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
		descs[4:7],                          // Blob 0
		{descs[4], descs[6]},                // Blob 1
		{descs[4], descs[6]},                // Blob 2
		{descs[5], descs[6]},                // Blob 3
		{descs[7]},                          // Blob 4
		{descs[7]},                          // Blob 5
		{descs[8], artifactRootDescWithRef}, // Blob 6
		{descs[10], rootIndexDescWithRef},   // Blob 7
		{descs[10]},                         // Blob 8
		{descs[10]},                         // Blob 9
		{descs[14]},                         // Blob 10
		{artifactRootDescWithRef},           // Blob 11
		nil,                                 // Blob 12, no predecessors
		{descs[14]},                         // Blob 13
		{rootIndexDescWithRef},              // Blob 14
	}
	for i, want := range wants {
		predecessors, err := readonlyS.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("ReadOnlyStore.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("ReadOnlyStore.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}

}

func TestReadOnlyStore_BadIndex(t *testing.T) {
	content := []byte("whatever")
	fsys := fstest.MapFS{
		ociImageIndexFile: &fstest.MapFile{Data: content},
	}

	ctx := context.Background()
	_, err := NewFromFS(ctx, fsys)
	if err == nil {
		t.Errorf("NewFromFS() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStore_BadLayout(t *testing.T) {
	content := []byte("whatever")
	fsys := fstest.MapFS{
		ocispec.ImageLayoutFile: &fstest.MapFile{Data: content},
	}

	ctx := context.Background()
	_, err := NewFromFS(ctx, fsys)
	if err == nil {
		t.Errorf("NewFromFS() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStore_Copy_OCIToMemory(t *testing.T) {
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
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    config,
			Layers:    layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		manifest := ocispec.Artifact{
			MediaType: ocispec.MediaTypeArtifactManifest,
			Subject:   &subject,
			Blobs:     blobs,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(manifest.MediaType, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foobar"))  // Blob 1
	generateManifest(descs[0], descs[1])                       // Blob 2
	generateArtifactManifest(descs[2])                         // Blob 3
	tag := "foobar"
	root := descs[3]
	root.Annotations = map[string]string{ocispec.AnnotationRefName: tag}

	layout := ocispec.ImageLayout{
		Version: ocispec.ImageLayoutVersion,
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		t.Fatalf("failed to marshal OCI layout: %v", err)
	}
	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value
		},
		Manifests: []ocispec.Descriptor{
			{
				MediaType: descs[3].MediaType,
				Digest:    descs[3].Digest,
				Size:      descs[3].Size,
				Annotations: map[string]string{
					ocispec.AnnotationRefName: tag,
				},
			},
		},
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}
	// build fs
	fsys := fstest.MapFS{}
	for i, desc := range descs {
		path := strings.Join([]string{"blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded()}, "/")
		fsys[path] = &fstest.MapFile{Data: blobs[i]}
	}
	fsys[ocispec.ImageLayoutFile] = &fstest.MapFile{Data: layoutJSON}
	fsys[ociImageIndexFile] = &fstest.MapFile{Data: indexJSON}

	// test read-only store
	ctx := context.Background()
	src, err := NewFromFS(ctx, fsys)
	if err != nil {
		t.Fatal("NewFromFS() error =", err)
	}

	// test copy
	dst := memory.New()
	gotDesc, err := oras.Copy(ctx, src, tag, dst, "", oras.DefaultCopyOptions)
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
	gotDesc, err = dst.Resolve(ctx, tag)
	if err != nil {
		t.Fatal("dst.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}

}
