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
	"context"
	"errors"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/ioutil"
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

	// ErrSizeExceedLimit is returned when the content size exceeds a limit.
	ErrSizeExceedLimit = errors.New("size exceeds limit")
)

// ReadAll safely reads the content described by the descriptor.
// The read content is verified against the size and the digest.
func ReadAll(r io.Reader, desc ocispec.Descriptor) ([]byte, error) {
	if desc.Size < 0 {
		return nil, ErrInvalidDescriptorSize
	}
	buf := make([]byte, desc.Size)

	// verify while reading
	verifier := desc.Digest.Verifier()
	r = io.TeeReader(r, verifier)
	// verify the size of the read content
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if err := ioutil.EnsureEOF(r); err != nil {
		return nil, ErrTrailingData
	}
	// verify the digest of the read content
	if !verifier.Verified() {
		return nil, ErrMismatchedDigest
	}
	return buf, nil
}

// FetchAll safely fetches the content described by the descriptor.
// The fetched content is verified against the size and the digest.
func FetchAll(ctx context.Context, fetcher Fetcher, desc ocispec.Descriptor) ([]byte, error) {
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return ReadAll(rc, desc)
}

// FetchAllWithLimit safely fetches the content described by the descriptor.
// The fetched content is verified against the size and the digest.
// The size of the fetched content cannot exceed the given size limit.
func FetchAllWithLimit(ctx context.Context, fetcher Fetcher, desc ocispec.Descriptor, limit int64) ([]byte, error) {
	if limit <= 0 {
		return FetchAll(ctx, fetcher, desc)
	}

	if desc.Size > limit {
		return nil, fmt.Errorf("content size %v exceeds size limit %v: %w",
			desc.Size,
			limit,
			ErrSizeExceedLimit)
	}
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return ReadAll(io.LimitReader(rc, limit), desc)
}
