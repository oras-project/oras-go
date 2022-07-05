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

package oras_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

func TestRegistry_Tag(t *testing.T) {
	target := memory.New()
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
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Tag
	err = oras.Tag(ctx, target, ref, "myTestingTag")
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}

	// verify tag
	gotDesc, err := target.Resolve(ctx, "myTestingTag")
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}
}
