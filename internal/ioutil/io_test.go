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
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/spec"
)

func TestUnwrapNopCloser(t *testing.T) {
	var reader struct {
		io.Reader
	}
	var readerWithWriterTo struct {
		io.Reader
		io.WriterTo
	}

	tests := []struct {
		name string
		rc   io.Reader
		want io.Reader
	}{
		{
			name: "nil",
		},
		{
			name: "no-op closer with plain io.Reader",
			rc:   io.NopCloser(reader),
			want: reader,
		},
		{
			name: "no-op closer with io.WriteTo",
			rc:   io.NopCloser(readerWithWriterTo),
			want: readerWithWriterTo,
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
	blob := []byte("foo")
	type args struct {
		src  io.Reader
		buf  []byte
		desc ocispec.Descriptor
	}
	tests := []struct {
		name         string
		args         args
		wantDst      string
		wantErr      error
		blob         []byte
		bufSize      int64
		resumeOffset int64
	}{
		{
			name:    "exact buffer size, no errors",
			wantDst: "foo",
			wantErr: nil,
			blob:    blob,
			bufSize: 3,
		},
		{
			name:         "exact buffer size, no errors, resume",
			wantDst:      "foo",
			wantErr:      nil,
			blob:         blob,
			bufSize:      3,
			resumeOffset: 1,
		},
		{
			name:    "small buffer size, no errors",
			wantDst: "foo",
			wantErr: nil,
			blob:    blob,
			bufSize: 1,
		},
		{
			name:         "small buffer size, no errors, resume",
			wantDst:      "foo",
			wantErr:      nil,
			blob:         blob,
			bufSize:      1,
			resumeOffset: 1,
		},
		{
			name:    "big buffer size, no errors",
			wantDst: "foo",
			wantErr: nil,
			blob:    blob,
			bufSize: 5,
		},
		{
			name:         "big buffer size, no errors, resume",
			wantDst:      "foo",
			wantErr:      nil,
			blob:         blob,
			bufSize:      5,
			resumeOffset: 1,
		},
		{
			name:    "wrong digest",
			wantDst: "foo",
			wantErr: content.ErrMismatchedDigest,
			blob:    []byte("bar"),
			bufSize: 3,
		},
		{
			name:         "wrong digest, resume",
			wantDst:      "foo",
			wantErr:      content.ErrMismatchedDigest,
			blob:         []byte("bar"),
			bufSize:      3,
			resumeOffset: 1,
		},
		{
			name:    "wrong size, descriptor size is smaller",
			wantDst: "foo",
			wantErr: content.ErrTrailingData,
			blob:    []byte("fo"),
			bufSize: 3,
		},
		{
			name:         "wrong size, descriptor size is smaller, resume",
			wantDst:      "foo",
			wantErr:      content.ErrTrailingData,
			blob:         []byte("fo"),
			bufSize:      3,
			resumeOffset: 1,
		},
		{
			name:    "wrong size, descriptor size is larger",
			wantDst: "foo",
			wantErr: io.ErrUnexpectedEOF,
			blob:    []byte("fooo"),
			bufSize: 3,
		},
		{
			name:         "wrong size, descriptor size is larger, resume",
			wantDst:      "foo",
			wantErr:      content.ErrMismatchedDigest,
			blob:         []byte("fooo"),
			bufSize:      3,
			resumeOffset: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &bytes.Buffer{}
			args := args{
				bytes.NewReader(blob),
				make([]byte, tt.bufSize),
				content.NewDescriptorFromBytes("test", tt.blob),
			}
			if tt.resumeOffset > 0 {
				// Make the starting Hash and run over the starting content
				h := args.desc.Digest.Algorithm().Hash()
				h.Write(tt.blob[0 : tt.resumeOffset-1])
				eh, _ := content.EncodeHash(h)

				// Add the Annotations for resume
				args.desc.Annotations = map[string]string{
					spec.AnnotationResumeDownload: "true",
					spec.AnnotationResumeOffset:   strconv.FormatInt(tt.resumeOffset, 10),
					spec.AnnotationResumeHash:     eh,
				}
			}
			err := CopyBuffer(dst, args.src, args.buf, args.desc)
			if !errors.Is(err, tt.wantErr) {
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
