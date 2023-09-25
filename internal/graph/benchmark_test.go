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

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/cas"
)

func testSetUp(ctx context.Context) ([]ocispec.Descriptor, content.Fetcher) {
	// set up for the test, prepare the test content and
	// return the Fetcher
	testFetcher := cas.NewMemory()

	var blobs [][]byte
	var descriptors []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) ocispec.Descriptor {
		blobs = append(blobs, blob)
		descriptors = append(descriptors, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
		return descriptors[len(descriptors)-1]
	}
	generateManifest := func(layers ...ocispec.Descriptor) ocispec.Descriptor {
		manifest := ocispec.Manifest{
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			panic(err)
		}
		return appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) ocispec.Descriptor {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			panic(err)
		}
		return appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	descE := appendBlob("layer node E", []byte("Node E is a layer")) // blobs[0], layer "E"
	descF := appendBlob("layer node F", []byte("Node F is a layer")) // blobs[1], layer "F"
	descB := generateManifest(descriptors[0:1]...)                   // blobs[2], manifest "B"
	descC := generateManifest(descriptors[0:2]...)                   // blobs[3], manifest "C"
	descD := generateManifest(descriptors[1:2]...)                   // blobs[4], manifest "D"
	descA := generateIndex(descriptors[2:5]...)                      // blobs[5], index "A"
	testFetcher.Push(ctx, descA, bytes.NewReader(blobs[5]))
	testFetcher.Push(ctx, descB, bytes.NewReader(blobs[2]))
	testFetcher.Push(ctx, descC, bytes.NewReader(blobs[3]))
	testFetcher.Push(ctx, descD, bytes.NewReader(blobs[4]))
	testFetcher.Push(ctx, descE, bytes.NewReader(blobs[0]))
	testFetcher.Push(ctx, descF, bytes.NewReader(blobs[1]))

	return []ocispec.Descriptor{descA, descB, descC, descD, descE, descF}, testFetcher
}

func BenchmarkMemoryIndex(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testMemory := NewMemory()
		for _, desc := range descs {
			testMemory.Index(ctx, testFetcher, desc)
		}
	}
}

func BenchmarkDeletableMemoryIndex(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testDeletableMemory := NewDeletableMemory()
		for _, desc := range descs {
			testDeletableMemory.Index(ctx, testFetcher, desc)
		}
	}
}

func BenchmarkMemoryIndexAll(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testMemory := NewMemory()
		testMemory.IndexAll(ctx, testFetcher, descs[0])
	}
}

func BenchmarkDeletableMemoryIndexAll(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testDeletableMemory := NewDeletableMemory()
		testDeletableMemory.IndexAll(ctx, testFetcher, descs[0])
	}
}

func BenchmarkMemoryPredecessors(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	testMemory := NewMemory()
	testMemory.IndexAll(ctx, testFetcher, descs[0])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testMemory.Predecessors(ctx, descs[4])
	}
}

func BenchmarkDeletableMemoryPredecessors(b *testing.B) {
	ctx := context.Background()
	descs, testFetcher := testSetUp(ctx)
	testDeletableMemory := NewDeletableMemory()
	testDeletableMemory.IndexAll(ctx, testFetcher, descs[0])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testDeletableMemory.Predecessors(ctx, descs[4])
	}
}
