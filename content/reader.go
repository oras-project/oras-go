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
	"errors"
	"fmt"
	"io"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	// ErrInvalidDescriptorSize is returned by ReadAll() when
	// the descriptor has an invalid size.
	ErrInvalidDescriptorSize = errors.New("invalid descriptor size")

	// ErrMismatchedDigest is returned by ReadAll() when
	// the descriptor has an invalid digest.
	ErrMismatchedDigest = errors.New("mismatched digest")

	// ErrTrailingData is returned by ReadAll() when
	// there exists trailing data unread when the read terminates.
	ErrTrailingData = errors.New("trailing data")
)

// VerifyReader reads the content described by its descriptor and verifies
// against its size and digest.
type VerifyReader struct {
	base     *io.LimitedReader
	verifier digest.Verifier
	verified bool
	err      error
}

// Read reads up to len(p) bytes into p. It returns the number of bytes
// read (0 <= n <= len(p)) and any error encountered.
func (vr *VerifyReader) Read(p []byte) (n int, err error) {
	if vr.err != nil {
		return 0, vr.err
	}

	n, err = vr.base.Read(p)
	if err != nil {
		if err == io.EOF && vr.base.N > 0 {
			err = io.ErrUnexpectedEOF
		}
		vr.err = err
	}
	return
}

// Verify verifies the read content against the size and the digest.
func (vr *VerifyReader) Verify() error {
	if vr.verified {
		return nil
	}
	if vr.err == nil {
		if vr.base.N > 0 {
			return errors.New("early verify")
		}
	} else if vr.err != io.EOF {
		return vr.err
	}

	if err := EnsureEOF(vr.base.R); err != nil {
		vr.err = ErrTrailingData
		return vr.err
	}
	if !vr.verifier.Verified() {
		vr.err = ErrMismatchedDigest
		return vr.err
	}

	vr.verified = true
	vr.err = io.EOF
	return nil
}

// NewVerifyReader returns a pointer to a new VerifyReader.
func NewVerifyReader(r io.Reader, desc ocispec.Descriptor) *VerifyReader {
	verifier := desc.Digest.Verifier()
	lr := &io.LimitedReader{
		R: io.TeeReader(r, verifier),
		N: desc.Size,
	}
	return &VerifyReader{
		base:     lr,
		verifier: verifier,
	}
}

// ReadAll safely reads the content described by the descriptor.
// The read content is verified against the size and the digest
// using a VerifyReader.
func ReadAll(r io.Reader, desc ocispec.Descriptor) ([]byte, error) {
	if desc.Size < 0 {
		return nil, ErrInvalidDescriptorSize
	}
	buf := make([]byte, desc.Size)

	vr := NewVerifyReader(r, desc)
	if _, err := io.ReadFull(vr, buf); err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if err := vr.Verify(); err != nil {
		return nil, err
	}
	return buf, nil
}

// EnsureEOF ensures the read operation ends with an EOF and no
// trailing data is present.
func EnsureEOF(r io.Reader) error {
	var peek [1]byte
	_, err := io.ReadFull(r, peek[:])
	if err != io.EOF {
		return errors.New("trailing data")
	}

	return nil
}
