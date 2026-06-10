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

package oras

import (
	"errors"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
)

func TestIsMissingReferencedContentError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "single MANIFEST_BLOB_UNKNOWN",
			err:  errcode.Error{Code: errcode.ErrorCodeManifestBlobUnknown},
			want: true,
		},
		{
			name: "single BLOB_UNKNOWN",
			err:  errcode.Error{Code: errcode.ErrorCodeBlobUnknown},
			want: true,
		},
		{
			name: "single MANIFEST_UNKNOWN",
			err:  errcode.Error{Code: errcode.ErrorCodeManifestUnknown},
			want: true,
		},
		{
			name: "single unrelated code",
			err:  errcode.Error{Code: errcode.ErrorCodeNameUnknown},
			want: false,
		},
		{
			name: "multiple errors with a missing code",
			err: errcode.Errors{
				{Code: errcode.ErrorCodeNameUnknown},
				{Code: errcode.ErrorCodeManifestBlobUnknown},
			},
			want: true,
		},
		{
			name: "multiple errors without a missing code",
			err: errcode.Errors{
				{Code: errcode.ErrorCodeNameUnknown},
				{Code: errcode.ErrorCodeDenied},
			},
			want: false,
		},
		{
			name: "wrapped in CopyError",
			err: newCopyError("Push", CopyErrorOriginDestination, errcode.Errors{
				{Code: errcode.ErrorCodeManifestBlobUnknown},
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMissingReferencedContentError(tt.err); got != tt.want {
				t.Errorf("isMissingReferencedContentError() = %v, want %v", got, tt.want)
			}
		})
	}
}
