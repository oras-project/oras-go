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
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
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
	"oras.land/oras-go/v2/internal/spec"
	"oras.land/oras-go/v2/registry"
)

func TestReadonlyStoreInterface(t *testing.T) {
	var store interface{} = &ReadOnlyStore{}
	if _, ok := store.(oras.ReadOnlyGraphTarget); !ok {
		t.Error("&ReadOnlyStore{} does not conform oras.ReadOnlyGraphTarget")
	}
	if _, ok := store.(registry.TagLister); !ok {
		t.Error("&ReadOnlyStore{} does not conform registry.TagLister")
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
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
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
				MediaType:   descs[2].MediaType,
				Size:        descs[2].Size,
				Digest:      descs[2].Digest,
				Annotations: map[string]string{ocispec.AnnotationRefName: subjectTag},
			},
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
	fsys["index.json"] = &fstest.MapFile{Data: indexJSON}

	// test read-only store
	ctx := context.Background()
	s, err := NewFromFS(ctx, fsys)
	if err != nil {
		t.Fatal("NewFromFS() error =", err)
	}

	// test resolving subject by digest
	gotDesc, err := s.Resolve(ctx, descs[2].Digest.String())
	if err != nil {
		t.Error("ReadOnlyStore.Resolve() error =", err)
	}
	if want := descs[2]; !reflect.DeepEqual(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// test resolving subject by tag
	gotDesc, err = s.Resolve(ctx, subjectTag)
	if err != nil {
		t.Error("ReadOnlyStore.Resolve() error =", err)
	}
	if want := descs[2]; !content.Equal(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// descriptor resolved by tag should have annotations
	if gotDesc.Annotations[ocispec.AnnotationRefName] != subjectTag {
		t.Errorf("ReadOnlyStore.Resolve() returned descriptor without annotations %v, want %v",
			gotDesc.Annotations,
			map[string]string{ocispec.AnnotationRefName: subjectTag})
	}

	// test resolving artifact by digest
	gotDesc, err = s.Resolve(ctx, descs[3].Digest.String())
	if err != nil {
		t.Error("ReadOnlyStore.Resolve() error =", err)
	}
	if want := descs[3]; !reflect.DeepEqual(gotDesc, want) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
	}

	// test resolving blob by digest
	gotDesc, err = s.Resolve(ctx, descs[0].Digest.String())
	if err != nil {
		t.Error("ReadOnlyStore.Resolve() error =", err)
	}
	if want := descs[0]; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
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
	if !content.Equal(gotDesc, indexRoot) {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, indexRoot)
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

	// test resolving blob by digest
	gotDesc, err = readonlyS.Resolve(ctx, descs[0].Digest.String())
	if err != nil {
		t.Fatal("ReadOnlyStore: Resolve() error =", err)
	}
	if want := descs[0]; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
		t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
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
=== Contents of testdata/hello-world.tar ===

blobs/

	blobs/sha256/
		blobs/sha256/2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54 // image layer
		blobs/sha256/f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4 // image manifest
		blobs/sha256/faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af // manifest list
		blobs/sha256/feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412 // config

index.json
manifest.json
oci-layout

=== Contents of testdata/hello-world-prefixed-path.tar ===

./
./blobs/

	./blobs/sha256/
		./blobs/sha256/2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54 // image layer
		./blobs/sha256/f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4 // image manifest
		./blobs/sha256/faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af // manifest list
		./blobs/sha256/feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412 // config

./index.json
./manifest.json
./oci-layout
*/

func TestReadOnlyStore_TarFS(t *testing.T) {
	tarPaths := []string{
		"testdata/hello-world.tar",
		"testdata/hello-world-prefixed-path.tar",
	}
	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			ctx := context.Background()
			s, err := NewFromTar(ctx, tarPath)
			if err != nil {
				t.Fatal("New() error =", err)
			}

			// test data in testdata/hello-world.tar
			descs := []ocispec.Descriptor{
				// desc 0: config
				{
					MediaType: "application/vnd.docker.container.image.v1+json",
					Size:      1469,
					Digest:    "sha256:feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412",
				},
				// desc 1: layer
				{
					MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip",
					Size:      2479,
					Digest:    "sha256:2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54",
				},
				// desc 2: image manifest
				{
					MediaType: "application/vnd.docker.distribution.manifest.v2+json",
					Digest:    "sha256:f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4",
					Size:      525,
					Platform: &ocispec.Platform{
						Architecture: "amd64",
						OS:           "linux",
					},
				},
				// desc 3: manifest list
				{
					MediaType: docker.MediaTypeManifestList,
					Size:      2561,
					Digest:    "sha256:faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af",
				},
			}

			// test resolving by tag
			for _, desc := range descs {
				gotDesc, err := s.Resolve(ctx, desc.Digest.String())
				if err != nil {
					t.Fatal("ReadOnlyStore: Resolve() error =", err)
				}
				if want := desc; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
					t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
				}
			}
			// test resolving by tag
			gotDesc, err := s.Resolve(ctx, "latest")
			if err != nil {
				t.Fatal("ReadOnlyStore: Resolve() error =", err)
			}
			if want := descs[3]; gotDesc.Size != want.Size || gotDesc.Digest != want.Digest {
				t.Errorf("ReadOnlyStore.Resolve() = %v, want %v", gotDesc, want)
			}

			// test Predecessors
			wantPredecessors := [][]ocispec.Descriptor{
				{descs[2]}, // desc 0
				{descs[2]}, // desc 1
				{descs[3]}, // desc 2
				{},         // desc 3
			}
			for i, want := range wantPredecessors {
				predecessors, err := s.Predecessors(ctx, descs[i])
				if err != nil {
					t.Errorf("ReadOnlyStore.Predecessors(%d) error = %v", i, err)
				}
				if !equalDescriptorSet(predecessors, want) {
					t.Errorf("ReadOnlyStore.Predecessors(%d) = %v, want %v", i, predecessors, want)
				}
			}
		})
	}
}

func TestReadOnlyStore_BadIndex(t *testing.T) {
	content := []byte("whatever")
	fsys := fstest.MapFS{
		"index.json": &fstest.MapFile{Data: content},
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
		manifest := spec.Artifact{
			MediaType: spec.MediaTypeArtifactManifest,
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
	fsys["index.json"] = &fstest.MapFile{Data: indexJSON}

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
	if !content.Equal(gotDesc, root) {
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
	if !content.Equal(gotDesc, root) {
		t.Errorf("dst.Resolve() = %v, want %v", gotDesc, root)
	}
}

func TestReadOnlyStore_Tags(t *testing.T) {
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
		// add annotation to make each manifest unique
		manifest.Annotations = map[string]string{
			"blob_index": strconv.Itoa(len(blobs)),
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
	generateManifest(descs[0], descs[1])                       // Blob 3
	generateManifest(descs[0], descs[1])                       // Blob 4
	generateManifest(descs[0], descs[1])                       // Blob 5
	generateManifest(descs[0], descs[1])                       // Blob 6

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
	}
	for _, desc := range descs[2:] {
		index.Manifests = append(index.Manifests, ocispec.Descriptor{
			MediaType: desc.MediaType,
			Size:      desc.Size,
			Digest:    desc.Digest,
		})
	}
	index.Manifests[1].Annotations = map[string]string{ocispec.AnnotationRefName: "v2"}
	index.Manifests[2].Annotations = map[string]string{ocispec.AnnotationRefName: "v3"}
	index.Manifests[3].Annotations = map[string]string{ocispec.AnnotationRefName: "v1"}
	index.Manifests[4].Annotations = map[string]string{ocispec.AnnotationRefName: "v4"}

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
	fsys["index.json"] = &fstest.MapFile{Data: indexJSON}

	// test read-only store
	ctx := context.Background()
	s, err := NewFromFS(ctx, fsys)
	if err != nil {
		t.Fatal("NewFromFS() error =", err)
	}

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
					t.Errorf("ReadOnlyStore.Tags() = %v, want %v", got, tt.want)
				}
				return nil
			}); err != nil {
				t.Errorf("ReadOnlyStore.Tags() error = %v", err)
			}
		})
	}

	wantErr := errors.New("expected error")
	if err := s.Tags(ctx, "", func(got []string) error {
		return wantErr
	}); err != wantErr {
		t.Errorf("ReadOnlyStore.Tags() error = %v, wantErr %v", err, wantErr)
	}
}

func Test_deleteAnnotationRefName(t *testing.T) {
	tests := []struct {
		name string
		desc ocispec.Descriptor
		want ocispec.Descriptor
	}{
		{
			name: "No annotation",
			desc: ocispec.Descriptor{},
			want: ocispec.Descriptor{},
		},
		{
			name: "Nil annotation",
			desc: ocispec.Descriptor{Annotations: nil},
			want: ocispec.Descriptor{},
		},
		{
			name: "Empty annotation",
			desc: ocispec.Descriptor{Annotations: map[string]string{}},
			want: ocispec.Descriptor{Annotations: map[string]string{}},
		},
		{
			name: "No RefName",
			desc: ocispec.Descriptor{Annotations: map[string]string{"foo": "bar"}},
			want: ocispec.Descriptor{Annotations: map[string]string{"foo": "bar"}},
		},
		{
			name: "Empty RefName",
			desc: ocispec.Descriptor{Annotations: map[string]string{
				"foo":                     "bar",
				ocispec.AnnotationRefName: "",
			}},
			want: ocispec.Descriptor{Annotations: map[string]string{"foo": "bar"}},
		},
		{
			name: "RefName only",
			desc: ocispec.Descriptor{Annotations: map[string]string{ocispec.AnnotationRefName: "foobar"}},
			want: ocispec.Descriptor{},
		},
		{
			name: "Multiple annotations with RefName",
			desc: ocispec.Descriptor{Annotations: map[string]string{
				"foo":                     "bar",
				ocispec.AnnotationRefName: "foobar",
			}},
			want: ocispec.Descriptor{Annotations: map[string]string{"foo": "bar"}},
		},
		{
			name: "Multiple annotations with empty RefName",
			desc: ocispec.Descriptor{Annotations: map[string]string{
				"foo":                     "bar",
				ocispec.AnnotationRefName: "",
			}},
			want: ocispec.Descriptor{Annotations: map[string]string{"foo": "bar"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deleteAnnotationRefName(tt.desc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("deleteAnnotationRefName() = %v, want %v", got, tt.want)
			}
		})
	}
}
