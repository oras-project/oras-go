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

package content_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"oras.land/oras-go/pkg/content"
)

func TestDecompressStore(t *testing.T) {
	rawContent := []byte("Hello World!")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(rawContent); err != nil {
		t.Fatalf("unable to create gzip content for testing: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("unable to close gzip writer creating content for testing: %v", err)
	}
	gzipContent := buf.Bytes()
	gzipContentHash := digest.FromBytes(gzipContent)

	extensions := []string{"+gzip", ".gzip"}
	for _, ext := range extensions {
		gzipDescriptor := ocispec.Descriptor{
			MediaType: fmt.Sprintf("%s%s", ocispec.MediaTypeImageConfig, ext),
			Digest:    gzipContentHash,
			Size:      int64(len(gzipContent)),
		}

		memStore := content.NewMemory()
		ctx := context.Background()
		memPusher, _ := memStore.Pusher(ctx, "")
		decompressStore := content.NewDecompress(memPusher, content.WithBlocksize(0))
		decompressWriter, err := decompressStore.Push(ctx, gzipDescriptor)
		if err != nil {
			t.Fatalf("unable to get a decompress writer: %v", err)
		}
		n, err := decompressWriter.Write(gzipContent)
		if err != nil {
			t.Fatalf("failed to write to decompress writer: %v", err)
		}
		if n != len(gzipContent) {
			t.Fatalf("wrote %d instead of expected %d bytes", n, len(gzipContent))
		}
		if err := decompressWriter.Commit(ctx, int64(len(gzipContent)), gzipContentHash); err != nil {
			t.Fatalf("unexpected error committing decompress writer: %v", err)
		}

		// and now we should be able to get the decompressed data from the memory store
		_, b, found := memStore.Get(gzipDescriptor)
		if !found {
			t.Fatalf("failed to get data from underlying memory store: %v", err)
		}
		if string(b) != string(rawContent) {
			t.Errorf("mismatched data in underlying memory store, actual '%s', expected '%s'", b, rawContent)
		}
	}
}
