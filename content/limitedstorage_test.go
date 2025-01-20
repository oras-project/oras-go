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
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
)

func TestLimitedStorage_Push(t *testing.T) {
	data := []byte("test")
	size := int64(len(data))
	dgst := digest.FromBytes(data)
	mediaType := "application/vnd.test"

	tests := []struct {
		name    string
		desc    ocispec.Descriptor
		limit   int64
		wantErr error
	}{
		{
			name: "descriptor size matches actual size and is within limit",
			desc: ocispec.Descriptor{
				MediaType: mediaType,
				Size:      size,
				Digest:    dgst,
			},
			limit:   size,
			wantErr: nil,
		},
		{
			name: "descriptor size matches actual size but exeeds limit",
			desc: ocispec.Descriptor{
				MediaType: mediaType,
				Size:      size,
				Digest:    dgst,
			},
			limit:   size - 1,
			wantErr: errdef.ErrSizeExceedsLimit,
		},
		{
			name: "descriptor size mismatches actual size and is within limit",
			desc: ocispec.Descriptor{
				MediaType: mediaType,
				Size:      size - 1,
				Digest:    dgst,
			},
			limit:   size,
			wantErr: content.ErrMismatchedDigest,
		},
		{
			name: "descriptor size mismatches actual size and exceeds limit",
			desc: ocispec.Descriptor{
				MediaType: mediaType,
				Size:      size + 1,
				Digest:    dgst,
			},
			limit:   size,
			wantErr: errdef.ErrSizeExceedsLimit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ls := content.LimitStorage(cas.NewMemory(), tt.limit)

			// test Push
			err := ls.Push(ctx, tt.desc, bytes.NewReader(data))
			if err != nil {
				if errors.Is(err, tt.wantErr) {
					return
				}
				t.Errorf("LimitedStorage.Push() error = %v, wantErr %v", err, tt.wantErr)
			}

			// verify
			rc, err := ls.Storage.Fetch(ctx, tt.desc)
			if err != nil {
				t.Fatalf("LimitedStorage.Fetch() error = %v", err)
			}
			defer rc.Close()

			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("io.ReadAll() error = %v", err)
			}
			if !reflect.DeepEqual(got, data) {
				t.Errorf("LimitedStorage.Fetch() = %v, want %v", got, data)
			}
		})
	}
}
