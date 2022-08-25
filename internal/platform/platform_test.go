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

package platform

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/docker"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		got       ocispec.Platform
		want      ocispec.Platform
		isMatched bool
	}{{
		ocispec.Platform{Architecture: "amd64", OS: "linux"},
		ocispec.Platform{Architecture: "amd64", OS: "linux"},
		true,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "linux"},
		ocispec.Platform{Architecture: "amd64", OS: "LINUX"},
		false,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "linux"},
		ocispec.Platform{Architecture: "arm64", OS: "linux"},
		false,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux"},
		ocispec.Platform{Architecture: "arm", OS: "linux", Variant: "v7"},
		false,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", Variant: "v7"},
		ocispec.Platform{Architecture: "arm", OS: "linux"},
		true,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", Variant: "v7"},
		ocispec.Platform{Architecture: "arm", OS: "linux", Variant: "v7"},
		true,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.768"},
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.700"},
		false,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "windows"},
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.768"},
		false,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.768"},
		ocispec.Platform{Architecture: "amd64", OS: "windows"},
		true,
	}, {
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.768"},
		ocispec.Platform{Architecture: "amd64", OS: "windows", OSVersion: "10.0.20348.768"},
		true,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a", "d"}},
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a", "c"}},
		false,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux"},
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a"}},
		false,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a"}},
		ocispec.Platform{Architecture: "arm", OS: "linux"},
		true,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a", "b"}},
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a", "b"}},
		true,
	}, {
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"a", "d", "c", "b"}},
		ocispec.Platform{Architecture: "arm", OS: "linux", OSFeatures: []string{"d", "c", "a", "b"}},
		true,
	}}

	for _, tt := range tests {
		gotPlatformJSON, _ := json.Marshal(tt.got)
		wantPlatformJSON, _ := json.Marshal(tt.want)
		name := string(gotPlatformJSON) + string(wantPlatformJSON)
		t.Run(name, func(t *testing.T) {
			if actual := Match(&tt.got, &tt.want); actual != tt.isMatched {
				t.Errorf("Match() = %v, want %v", actual, tt.isMatched)
			}
		})
	}
}

func TestSelectManifest(t *testing.T) {
	storage := cas.NewMemory()
	arc_1 := "test-arc-1"
	os_1 := "test-os-1"
	variant_1 := "v1"
	arc_2 := "test-arc-2"
	os_2 := "test-os-2"
	variant_2 := "v2"

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
	appendManifest := func(arc, os, variant string, mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Platform: &ocispec.Platform{
				Architecture: arc,
				OS:           os,
				Variant:      variant,
			},
		})
	}
	generateManifest := func(arc, os, variant string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendManifest(arc, os, variant, ocispec.MediaTypeImageManifest, manifestJSON)
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

	appendBlob(ocispec.MediaTypeImageConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))            // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))            // Blob 2
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1:3]...) // Blob 3
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello1"))         // Blob 4
	generateManifest(arc_2, os_2, variant_1, descs[0], descs[4])      // Blob 5
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello2"))         // Blob 6
	generateManifest(arc_1, os_1, variant_2, descs[0], descs[6])      // Blob 7
	generateIndex(descs[3], descs[5], descs[7])                       // Blob 8

	ctx := context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test SelectManifest on image index, only one matching manifest found
	root := descs[8]
	targetPlatform := ocispec.Platform{
		Architecture: arc_2,
		OS:           os_2,
	}
	wantDesc := descs[5]
	gotDesc, err := SelectManifest(ctx, storage, root, &targetPlatform)
	if err != nil {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("SelectManifest() = %v, want %v", gotDesc, wantDesc)
	}

	// test SelectManifest on image index,
	// and multiple manifests match the required platform.
	// Should return the first matching entry.
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	wantDesc = descs[3]
	gotDesc, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if err != nil {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("SelectManifest() = %v, want %v", gotDesc, wantDesc)
	}

	// test SelectManifest on manifest
	root = descs[7]
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	wantDesc = descs[7]
	gotDesc, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if err != nil {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, false)
	}
	if !reflect.DeepEqual(gotDesc, wantDesc) {
		t.Errorf("SelectManifest() = %v, want %v", gotDesc, wantDesc)
	}

	// test SelectManifest on manifest, but there is no matching node.
	// Should return not found error.
	root = descs[7]
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
		Variant:      variant_2,
	}
	_, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test SelectManifest on manifest, but the node's media type is not
	// supported. Should return unsupported error
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	root = descs[1]
	_, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}

	// generate incorrect test content
	blobs = nil
	descs = nil
	appendBlob(docker.MediaTypeConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author 1",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo1"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test SelectManifest on manifest, but the manifest is
	// invalid by having docker mediaType config in the manifest and oci
	// mediaType in the image config. Should return error.
	root = descs[2]
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	_, err = SelectManifest(ctx, storage, root, &targetPlatform)
	expected := fmt.Sprintf("mismatch MediaType %s: expect %s", docker.MediaTypeConfig, ocispec.MediaTypeImageConfig)
	if err.Error() != expected {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, expected)
	}

	// generate test content with null config blob
	blobs = nil
	descs = nil
	appendBlob(ocispec.MediaTypeImageConfig, []byte("null"))     // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo2"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test SelectManifest on manifest with null config blob,
	// should return not found error.
	root = descs[2]
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	_, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// generate test content with empty config blob
	blobs = nil
	descs = nil
	appendBlob(ocispec.MediaTypeImageConfig, []byte(""))         // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo3"))      // Blob 1
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1]) // Blob 2
	generateIndex(descs[2])                                      // Blob 3

	ctx = context.Background()
	for i := range blobs {
		err := storage.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	// test SelectManifest on manifest with empty config blob
	// should return not found error
	root = descs[2]
	targetPlatform = ocispec.Platform{
		Architecture: arc_1,
		OS:           os_1,
	}
	_, err = SelectManifest(ctx, storage, root, &targetPlatform)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("SelectManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}
