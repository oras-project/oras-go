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
	"context"
	"io/ioutil"
	"os"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"oras.land/oras-go/pkg/content"
)

func TestFileStoreNoName(t *testing.T) {
	testContent := []byte("Hello World!")
	descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(testContent),
		Size:      int64(len(testContent)),
		// do NOT add the AnnotationTitle here; it is the essence of the test
	}

	tests := []struct {
		opts []content.WriterOpt
		err  error
	}{
		{nil, nil},
		{[]content.WriterOpt{content.WithErrorOnNoName()}, content.ErrNoName},
	}
	for _, tt := range tests {
		rootPath, err := ioutil.TempDir("", "oras_filestore_test")
		if err != nil {
			t.Fatalf("error creating tempdir: %v", err)
		}
		defer os.RemoveAll(rootPath)
		fileStore := content.NewFile(rootPath, tt.opts...)
		ctx := context.Background()
		pusher, _ := fileStore.Pusher(ctx, "")
		if _, err := pusher.Push(ctx, descriptor); err != tt.err {
			t.Errorf("mismatched error, actual '%v', expected '%v'", err, tt.err)
		}

	}
}
