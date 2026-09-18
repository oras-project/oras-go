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
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
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
		name    string
		args    args
		wantDst string
		wantErr error
	}{
		{
			name:    "exact buffer size, no errors",
			args:    args{bytes.NewReader(blob), make([]byte, 3), content.NewDescriptorFromBytes("test", blob)},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "small buffer size, no errors",
			args:    args{bytes.NewReader(blob), make([]byte, 1), content.NewDescriptorFromBytes("test", blob)},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "big buffer size, no errors",
			args:    args{bytes.NewReader(blob), make([]byte, 5), content.NewDescriptorFromBytes("test", blob)},
			wantDst: "foo",
			wantErr: nil,
		},
		{
			name:    "wrong digest",
			args:    args{bytes.NewReader(blob), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("bar"))},
			wantDst: "foo",
			wantErr: content.ErrMismatchedDigest,
		},
		{
			name:    "wrong size, descriptor size is smaller",
			args:    args{bytes.NewReader(blob), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("fo"))},
			wantDst: "foo",
			wantErr: content.ErrTrailingData,
		},
		{
			name:    "wrong size, descriptor size is larger",
			args:    args{bytes.NewReader(blob), make([]byte, 3), content.NewDescriptorFromBytes("test", []byte("fooo"))},
			wantDst: "foo",
			wantErr: io.ErrUnexpectedEOF,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &bytes.Buffer{}
			err := CopyBuffer(dst, tt.args.src, tt.args.buf, tt.args.desc)
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

// readRecorder records the size of the largest read served by the underlying
// reader. It deliberately implements io.Reader only, so it never offers a
// io.WriterTo fast path of its own.
type readRecorder struct {
	r       io.Reader
	maxRead int
}

func (rr *readRecorder) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	if n > rr.maxRead {
		rr.maxRead = n
	}
	return n, err
}

// writeRecorder records the size of the largest write it receives. It
// deliberately implements io.Writer only, so it never offers an io.ReaderFrom
// fast path of its own.
type writeRecorder struct {
	w        io.Writer
	maxWrite int
}

func (wr *writeRecorder) Write(p []byte) (int, error) {
	if len(p) > wr.maxWrite {
		wr.maxWrite = len(p)
	}
	return wr.w.Write(p)
}

// tempFileWithContent creates a temporary file holding content and returns it
// positioned at the start.
func tempFileWithContent(t *testing.T, content []byte) *os.File {
	t.Helper()
	fp, err := os.CreateTemp(t.TempDir(), "content")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() {
		if err := fp.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("failed to close temp file: %v", err)
		}
	})
	if _, err := fp.Write(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	if _, err := fp.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("failed to seek temp file: %v", err)
	}
	return fp
}

func TestCopyWithBuffer_bufferIsUsed(t *testing.T) {
	const bufSize = 128 * 1024
	blob := make([]byte, 4*bufSize)
	for i := range blob {
		blob[i] = byte(i)
	}

	t.Run("destination implements io.ReaderFrom", func(t *testing.T) {
		src := &readRecorder{r: bytes.NewReader(blob)}
		dst := tempFileWithContent(t, nil)
		buf := make([]byte, bufSize)

		n, err := CopyWithBuffer(dst, src, buf)
		if err != nil {
			t.Fatalf("CopyWithBuffer() error = %v", err)
		}
		if n != int64(len(blob)) {
			t.Errorf("CopyWithBuffer() = %d, want %d", n, len(blob))
		}
		if src.maxRead != bufSize {
			t.Errorf("largest read = %d, want %d: the provided buffer was not used", src.maxRead, bufSize)
		}
		got, err := os.ReadFile(dst.Name())
		if err != nil {
			t.Fatalf("failed to read destination: %v", err)
		}
		if !bytes.Equal(got, blob) {
			t.Error("destination content does not match the source content")
		}
	})

	t.Run("source implements io.WriterTo", func(t *testing.T) {
		src := tempFileWithContent(t, blob)
		var sink bytes.Buffer
		dst := &writeRecorder{w: &sink}
		buf := make([]byte, bufSize)

		n, err := CopyWithBuffer(dst, src, buf)
		if err != nil {
			t.Fatalf("CopyWithBuffer() error = %v", err)
		}
		if n != int64(len(blob)) {
			t.Errorf("CopyWithBuffer() = %d, want %d", n, len(blob))
		}
		if dst.maxWrite != bufSize {
			t.Errorf("largest write = %d, want %d: the provided buffer was not used", dst.maxWrite, bufSize)
		}
		if !bytes.Equal(sink.Bytes(), blob) {
			t.Error("destination content does not match the source content")
		}
	})
}

func TestCopyBuffer_bufferIsUsed(t *testing.T) {
	const bufSize = 128 * 1024
	blob := make([]byte, 4*bufSize)
	for i := range blob {
		blob[i] = byte(i)
	}

	src := &readRecorder{r: bytes.NewReader(blob)}
	dst := tempFileWithContent(t, nil)
	buf := make([]byte, bufSize)

	if err := CopyBuffer(dst, src, buf, content.NewDescriptorFromBytes("test", blob)); err != nil {
		t.Fatalf("CopyBuffer() error = %v", err)
	}
	if src.maxRead != bufSize {
		t.Errorf("largest read = %d, want %d: the provided buffer was not used", src.maxRead, bufSize)
	}
	got, err := os.ReadFile(dst.Name())
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if !bytes.Equal(got, blob) {
		t.Error("destination content does not match the source content")
	}
}
