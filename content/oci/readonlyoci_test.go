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
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/internal/docker"
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
			subjectWithRef,
			descs[3],
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

	// test resolve subject by digest
	gotDesc, err := s.Resolve(ctx, descs[2].Digest.String())
	if err != nil {
		t.Error("ReadOnlyReadOnlyStore.Resolve() error =", err)
	}
	if want := subjectWithRef; !reflect.DeepEqual(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// test resolve subject by tag
	gotDesc, err = s.Resolve(ctx, subjectTag)
	if err != nil {
		t.Error("ReadOnlyReadOnlyStore.Resolve() error =", err)
	}
	if want := subjectWithRef; !reflect.DeepEqual(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// test resolve artifact by digest
	gotDesc, err = s.Resolve(ctx, descs[3].Digest.String())
	if err != nil {
		t.Error("ReadOnlyReadOnlyStore.Resolve() error =", err)
	}
	if want := descs[3]; !reflect.DeepEqual(gotDesc, want) {
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
		{descs[2]}, // blob 0
		{descs[2]}, // blob 1
		{descs[3]}, // blob 2,
		{},         // blob 3
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

	// tag index root
	indexRoot := descs[10]
	tag := "latest"
	indexRootWithRef := indexRoot
	indexRootWithRef.Annotations = map[string]string{
		ocispec.AnnotationRefName: tag,
	}
	if err := s.Tag(ctx, indexRoot, tag); err != nil {
		t.Fatal("Tag() error =", err)
	}

	// test read-only store
	readonlyS, err := NewFromFS(ctx, os.DirFS(tempDir))
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// test resolving index root by tag
	gotDesc, err := readonlyS.Resolve(ctx, tag)
	if err != nil {
		t.Fatal("ReadOnlyStore: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexRootWithRef) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, indexRootWithRef)
	}

	// test resolving index root by digest
	gotDesc, err = readonlyS.Resolve(ctx, indexRoot.Digest.String())
	if err != nil {
		t.Fatal("ReadOnlyStore: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexRoot) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, indexRoot)
	}

	// test resolving artifact manifest by digest
	artifactRootDesc := descs[12]
	gotDesc, err = readonlyS.Resolve(ctx, artifactRootDesc.Digest.String())
	if err != nil {
		t.Fatal("ReadOnlyStore: Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, artifactRootDesc) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, artifactRootDesc)
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
		predecessors, err := readonlyS.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("ReadOnlyStore.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("ReadOnlyStore.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

/*
testdata/hello-world.tar contains:

	blobs/
		sha256/
			2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54
			f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4
			faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af
			feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412
	index.json
	manifest.json
	oci-layout
*/
func TestReadOnlyStore_TarFS(t *testing.T) {
	ctx := context.Background()
	s, err := NewFromTar(ctx, "testdata/hello-world.tar")
	if err != nil {
		t.Fatal("New() error =", err)
	}

	// test data in testdata/hello-world.tar
	wantDesc := ocispec.Descriptor{
		MediaType: docker.MediaTypeManifestList,
		Size:      2561,
		Digest:    "sha256:faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af",
		Annotations: map[string]string{
			"io.containerd.image.name": "docker.io/library/hello-world:latest",
			ocispec.AnnotationRefName:  "latest",
		},
	}

	// test Resolve by tag
	tag := "latest"
	gotDesc, err := s.Resolve(ctx, tag)
	if err != nil {
		t.Fatal("ReadOnlyStore.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// test Resolve by digest
	gotDesc, err = s.Resolve(ctx, "sha256:faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af")
	if err != nil {
		t.Fatal("ReadOnlyStore.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, wantDesc)
	}

	// test Predecessors
	gotSuccessors, err := content.Successors(ctx, s, gotDesc)
	if err != nil {
		t.Fatal("failed to get successors:", err)
	}
	wantPredecessors := []ocispec.Descriptor{gotDesc}
	for i, successor := range gotSuccessors {
		gotPredecessors, err := s.Predecessors(ctx, successor)
		if err != nil {
			t.Fatalf("ReadOnlyStore.Predecessor(%d) error = %v", i, err)
		}
		if !reflect.DeepEqual(gotPredecessors, wantPredecessors) {
			t.Errorf("ReadOnlyStore.Predecessor(%d) = %v, want %v", i, gotPredecessors, wantPredecessors)
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
