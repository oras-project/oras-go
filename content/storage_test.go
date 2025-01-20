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

package content

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestFetcherFunc_Fetch(t *testing.T) {
	data := []byte("test content")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}

	fetcherFunc := FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		if target.Digest != desc.Digest {
			return nil, errors.New("content not found")
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	})

	ctx := context.Background()
	rc, err := fetcherFunc.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("FetcherFunc.Fetch() error = %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("FetcherFunc.Fetch().Read() error = %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("FetcherFunc.Fetch() = %v, want %v", got, data)
	}
}
