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

package ioutil

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

func TestUnwrapNopCloser(t *testing.T) {
	tests := []struct {
		name string
		rc   io.Reader
		want io.Reader
	}{
		{
			name: "nil",
		},
		{
			name: "no-op closer",
			rc:   io.NopCloser(os.Stdin),
			want: os.Stdin,
		},
		{
			name: "any ReadCloser",
			rc:   os.Stdin,
			want: os.Stdin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnwrapNopCloser(tt.rc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnwrapNopCloser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCopyBuffer(t *testing.T) {
	type args struct {
		src  io.Reader
		buf  []byte
		desc ocispec.Descriptor
	}
	tests := []struct {
		name    string
		args    args
		wantDst string
		wantErr error
	}{
		{
			name:    "exact buffer size, no errors",
			args:    args{bytes.NewReader([]byte("foo")), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("foo"))},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "small buffer size, no errors",
			args:    args{bytes.NewReader([]byte("foo")), make([]byte, 1), content.NewDescriptorFromBytes("test", []byte("foo"))},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "big buffer size, no errors",
			args:    args{bytes.NewReader([]byte("foo")), make([]byte, 5), content.NewDescriptorFromBytes("test", []byte("foo"))},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "wrong digest",
			args:    args{bytes.NewReader([]byte("foo")), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("bar"))},
			wantDst: "foo",
			wantErr: content.ErrMismatchedDigest,
		},
		{
			name:    "wrong size",
			args:    args{bytes.NewReader([]byte("foo")), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("fo"))},
			wantDst: "foo",
			wantErr: content.ErrTrailingData,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &bytes.Buffer{}
			err := CopyBuffer(dst, tt.args.src, tt.args.buf, tt.args.desc)
			if err != tt.wantErr {
				t.Errorf("CopyBuffer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			gotDst := dst.String()
			if err == nil && gotDst != tt.wantDst {
				t.Errorf("CopyBuffer() = %v, want %v", gotDst, tt.wantDst)
			}
		})
	}
}
