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
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strconv"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/spec"
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

var (
	// errEarlyVerify is returned by VerifyReader.Verify() when
	// Verify() is called before completing reading the entire content blob.
	errEarlyVerify = errors.New("early verify")
)

// VerifyReader reads the content described by its descriptor and verifies
// against its size and digest.
type VerifyReader struct {
	base     *io.LimitedReader
	verifier digest.Verifier
	verified bool
	err      error
	resume   bool
}

// Read reads up to len(p) bytes into p. It returns the number of bytes
// read (0 <= n <= len(p)) and any error encountered.
func (vr *VerifyReader) Read(p []byte) (n int, err error) {
	if vr.err != nil {
		return 0, vr.err
	}

	n, err = vr.base.Read(p)
	if err != nil {
		if err == io.EOF && vr.base.N > 0 && !vr.resume {
			err = io.ErrUnexpectedEOF
		}
		vr.err = err
	}
	return
}

// Verify checks for remaining unread content and verifies the read content against the digest
func (vr *VerifyReader) Verify() error {
	if vr.verified {
		return nil
	}
	if vr.err == nil {
		if vr.base.N > 0 {
			return errEarlyVerify
		}
	} else if vr.err != io.EOF {
		return vr.err
	}

	if err := ensureEOF(vr.base.R); err != nil {
		vr.err = err
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

// NewVerifyReader wraps r for reading content with verification against desc.
func NewVerifyReader(r io.Reader, desc ocispec.Descriptor) *VerifyReader {
	if err := desc.Digest.Validate(); err != nil {
		return &VerifyReader{
			err: fmt.Errorf("failed to validate %s: %w", desc.Digest, err),
		}
	}

	var verifier digest.Verifier

	// Ignore error, if we can't parse it assume zero
	offset, _ := strconv.ParseInt(desc.Annotations[spec.AnnotationResumeOffset], 10, 64)

	// All error cases below fall through to create a digest.Verifier
	if offset > 0 {
		// Attempt to resume
		newHash, err := DecodeHash(desc.Annotations[spec.AnnotationResumeHash], desc.Digest)
		if err == nil {
			// Create a verifier with our in-progress hash and the final digest
			verifier = hashVerifier{
				hash:   newHash,
				digest: desc.Digest,
			}
		}
	}
	if verifier == nil {
		// Did not get a verifier for resume, make a new empty one
		verifier = desc.Digest.Verifier()
	}

	lr := &io.LimitedReader{
		R: io.TeeReader(r, verifier),
		N: desc.Size,
	}
	return &VerifyReader{
		base:     lr,
		verifier: verifier,
		resume:   offset > 0,
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
	if n, err := io.ReadFull(vr, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			if err == io.ErrUnexpectedEOF && vr.base.N > 0 && vr.resume {
				// In resume mode the buffers may not be exact
				err = io.EOF
			}
			return nil, fmt.Errorf("read failed: expected content size of %d, got %d, for digest %s: %w", desc.Size, n, desc.Digest.String(), err)
		}
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if err := vr.Verify(); err != nil {
		return nil, err
	}
	return buf, nil
}

// ensureEOF ensures the read operation ends with an EOF and no
// trailing data is present.
func ensureEOF(r io.Reader) error {
	var peek [1]byte
	_, err := io.ReadFull(r, peek[:])
	if err != io.EOF {
		return ErrTrailingData
	}
	return nil
}

// DecodeHash recovers a Hash object from existing partial data
func DecodeHash(encHash string, d digest.Digest) (hash.Hash, error) {
	state, err := hex.DecodeString(encHash)
	if err == nil {
		// Recover Hash object
		newHash := d.Algorithm().Hash()
		unmarshaler, ok := newHash.(encoding.BinaryUnmarshaler)
		if ok {
			if err := unmarshaler.UnmarshalBinary(state); err == nil {
				return newHash, nil
			}
		}
	}
	// Return new empty Hash with error
	return sha256.New(), err
}

// EncodeHash serialzes a Hash object to pass to a Verifier
func EncodeHash(h hash.Hash) (string, error) {
	marshaler, ok := h.(encoding.BinaryMarshaler)
	if ok {
		state, err := marshaler.MarshalBinary()
		if err == nil {
			// Save the new Hash as an Annotation to pass to the Verifier
			buf := make([]byte, hex.EncodedLen(len(state)))
			hex.Encode(buf, state)
			return string(buf), nil
		}
	}
	return "", fmt.Errorf("error encoding Hash")
}
