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
	"errors"
	"fmt"
	"io"
	"reflect"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// CloserFunc is the basic Close method defined in io.Closer.
type CloserFunc func() error

// Close performs close operation by the CloserFunc.
func (fn CloserFunc) Close() error {
	return fn()
}

// CopyBuffer copies from src to dst through the provided buffer
// until either EOF is reached on src, or an error occurs.
// The copied content is verified against the size and the digest.
func CopyBuffer(dst io.Writer, src io.Reader, buf []byte, desc ocispec.Descriptor) error {
	// verify while copying
	verifier := desc.Digest.Verifier()
	lr := io.LimitReader(src, desc.Size)
	mw := io.MultiWriter(verifier, dst)

	if _, err := io.CopyBuffer(mw, lr, buf); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}

	if !verifier.Verified() {
		return errors.New("digest verification failed")
	}

	return EnsureEOF(lr)
}

// EnsureEOF checks if the read operation ends with an EOF and no
// trailing data is present.
func EnsureEOF(r io.Reader) error {
	var peek [1]byte
	_, err := io.ReadFull(r, peek[:])
	if err != io.EOF {
		return errors.New("trailing data")
	}

	return nil
}

// nopCloserType is the type of `io.NopCloser()`.
var nopCloserType = reflect.TypeOf(io.NopCloser(nil))

// UnwrapNopCloser unwraps the reader wrapped by `io.NopCloser()`.
// Similar implementation can be found in the built-in package `net/http`.
// Reference: https://github.com/golang/go/blob/go1.17.6/src/net/http/transfer.go#L423-L425
func UnwrapNopCloser(rc io.Reader) io.Reader {
	if reflect.TypeOf(rc) == nopCloserType {
		return reflect.ValueOf(rc).Field(0).Interface().(io.Reader)
	}
	return rc
}
