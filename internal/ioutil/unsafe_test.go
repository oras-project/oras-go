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
	_ "crypto/sha256"
	_ "crypto/sha512"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
)

func TestCopyUnsafe(t *testing.T) {
	fail := errors.New("I/O failed")
	tests := []struct {
		name      string
		src       io.Reader
		dst       io.Writer
		expected  ocispec.Descriptor
		limit     int64
		wantErr   error
		wantRead  int
		wantWrite string
	}{
		{name: "unknown", expected: ocispec.Descriptor{Size: -1}, limit: 8, wantRead: 6, wantWrite: "foobar"},
		{name: "known", expected: ocispec.Descriptor{Digest: digest.FromString("foobar"), Size: 6}, limit: 8, wantRead: 6, wantWrite: "foobar"},
		{name: "sha512", expected: ocispec.Descriptor{Digest: digest.SHA512.FromString("foobar"), Size: -1}, limit: 8, wantRead: 6, wantWrite: "foobar"},
		{name: "limit", expected: ocispec.Descriptor{Size: -1}, limit: 4, wantErr: errdef.ErrSizeExceedsLimit, wantRead: 5, wantWrite: "foob"},
		{name: "known too small", expected: ocispec.Descriptor{Size: 3}, limit: 8, wantErr: content.ErrTrailingData, wantRead: 4, wantWrite: "foo"},
		{name: "known too large", expected: ocispec.Descriptor{Size: 7}, limit: 8, wantErr: io.ErrUnexpectedEOF, wantRead: 6, wantWrite: "foobar"},
		{name: "known exceeds limit", expected: ocispec.Descriptor{Size: 9}, limit: 8, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "zero limit unknown", expected: ocispec.Descriptor{Size: -1}, wantErr: errdef.ErrSizeExceedsLimit, wantRead: 1},
		{name: "zero size", expected: ocispec.Descriptor{Size: 0}, limit: 8, wantErr: content.ErrTrailingData, wantRead: 1},
		{name: "negative effective limit", expected: ocispec.Descriptor{Size: -1}, limit: -1, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "max limit", expected: ocispec.Descriptor{Size: -1}, limit: math.MaxInt64, wantRead: 6, wantWrite: "foobar"},
		{name: "max size", expected: ocispec.Descriptor{Size: math.MaxInt64}, limit: math.MaxInt64, wantErr: io.ErrUnexpectedEOF, wantRead: 6, wantWrite: "foobar"},
		{name: "digest mismatch", expected: ocispec.Descriptor{Digest: digest.FromString("wrong!"), Size: -1}, limit: 8, wantErr: content.ErrMismatchedDigest, wantRead: 6, wantWrite: "foobar"},
		{name: "invalid digest", expected: ocispec.Descriptor{Digest: "invalid", Size: -1}, limit: 8, wantErr: errdef.ErrInvalidDigest},
		{name: "unsupported algorithm", expected: ocispec.Descriptor{Digest: digest.Digest("sha1:" + strings.Repeat("a", 40)), Size: -1}, limit: 8, wantErr: errdef.ErrUnsupported},
		{name: "read failure", src: io.MultiReader(strings.NewReader("foo"), unsafeErrorReader{fail}), expected: ocispec.Descriptor{Size: -1}, limit: 8, wantErr: fail, wantRead: 3, wantWrite: "foo"},
		{name: "probe failure", src: io.MultiReader(strings.NewReader("foobar"), unsafeErrorReader{fail}), expected: ocispec.Descriptor{Size: 6}, limit: 8, wantErr: fail, wantRead: 6, wantWrite: "foobar"},
		{name: "write failure", dst: unsafeErrorWriter{fail}, expected: ocispec.Descriptor{Size: -1}, limit: 8, wantErr: fail, wantRead: 2},
		{name: "short write", dst: unsafeShortWriter{}, expected: ocispec.Descriptor{Size: -1}, limit: 8, wantErr: io.ErrShortWrite, wantRead: 2},
		{name: "empty zero limit", src: strings.NewReader(""), expected: ocispec.Descriptor{Size: -1}},
		{name: "empty known zero", src: strings.NewReader(""), expected: ocispec.Descriptor{Size: 0}, limit: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst bytes.Buffer
			writer := tt.dst
			if writer == nil {
				writer = &dst
			}
			src := tt.src
			if src == nil {
				src = strings.NewReader("foobar")
			}
			reader := &unsafeCountReader{Reader: src}
			expected := tt.expected
			expected.MediaType = "test"
			expected.Annotations = map[string]string{"test": "preserved"}
			got, err := CopyUnsafe(writer, reader, make([]byte, 2), expected, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CopyUnsafe() error = %v, want %v", err, tt.wantErr)
			}
			if reader.n != tt.wantRead {
				t.Errorf("read %d bytes, want %d", reader.n, tt.wantRead)
			}
			if tt.wantRead == 0 && err != nil && reader.calls != 0 {
				t.Errorf("validation called reader %d times", reader.calls)
			}
			if dst.String() != tt.wantWrite {
				t.Errorf("wrote %q, want %q", dst.String(), tt.wantWrite)
			}
			if err != nil {
				if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
					t.Errorf("error descriptor = %+v, want empty", got)
				}
			} else {
				want := expected
				if want.Digest == "" {
					want.Digest = digest.FromBytes(dst.Bytes())
				}
				want.Size = int64(dst.Len())
				if !reflect.DeepEqual(got, want) {
					t.Errorf("descriptor = %+v, want %+v", got, want)
				}
			}
		})
	}
}

type unsafeCountReader struct {
	io.Reader
	n     int
	calls int
}

func (r *unsafeCountReader) Read(p []byte) (int, error) {
	r.calls++
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}

// WriterTo must not bypass bounded reads or the supplied copy buffer.
func (r *unsafeCountReader) WriteTo(io.Writer) (int64, error) { panic("unexpected WriterTo") }

type unsafeErrorReader struct{ err error }

func (r unsafeErrorReader) Read([]byte) (int, error) { return 0, r.err }

type unsafeErrorWriter struct{ err error }

func (w unsafeErrorWriter) Write([]byte) (int, error) { return 0, w.err }

type unsafeShortWriter struct{}

func (unsafeShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
