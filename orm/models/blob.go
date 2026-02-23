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

package models

import (
	"bytes"
	"context"
	"io"
	"maps"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
)

// Blob represents a binary content object (layer, config, arbitrary data).
// Blobs are immutable and content-addressable.
type Blob struct {
	descriptor ocispec.Descriptor
	fetcher    content.Fetcher
	pusher     content.Pusher

	// Lazy-loaded content.
	// Uses lazy[T] for thread-safe loading with retry on transient errors.
	contentData lazy[[]byte]
}

// NewBlob creates a new Blob from a descriptor.
// The content is not loaded until accessed via Read() or Bytes().
func NewBlob(desc ocispec.Descriptor, fetcher content.Fetcher, pusher content.Pusher) *Blob {
	return &Blob{
		descriptor: desc,
		fetcher:    fetcher,
		pusher:     pusher,
	}
}

// NewBlobFromBytes creates a new Blob from raw bytes.
// The descriptor is computed from the content.
// Optional fetcher and pusher can be provided for storage operations.
func NewBlobFromBytes(mediaType string, data []byte, opts ...func(*Blob)) *Blob {
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	b := &Blob{
		descriptor: desc,
	}
	b.contentData.set(data)
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// WithStorage returns a Blob option that sets the fetcher and pusher.
func WithStorage(fetcher content.Fetcher, pusher content.Pusher) func(*Blob) {
	return func(b *Blob) {
		b.fetcher = fetcher
		b.pusher = pusher
	}
}

// Descriptor returns the OCI descriptor for this blob.
func (b *Blob) Descriptor() ocispec.Descriptor {
	return b.descriptor
}

// Digest returns the digest of the blob content.
func (b *Blob) Digest() digest.Digest {
	return b.descriptor.Digest
}

// MediaType returns the media type of the blob.
func (b *Blob) MediaType() string {
	return b.descriptor.MediaType
}

// Size returns the size of the blob in bytes.
func (b *Blob) Size() int64 {
	return b.descriptor.Size
}

// Annotations returns a copy of the annotations associated with this blob.
// The returned map is safe to modify without affecting the blob.
func (b *Blob) Annotations() map[string]string {
	return maps.Clone(b.descriptor.Annotations)
}

// Read returns a ReadCloser for streaming the blob content.
// This is useful for large blobs that should not be loaded entirely into memory.
// The returned reader verifies the content digest on read. Call
// VerifyReader.Verify() after reading to confirm integrity, or use Bytes()
// which verifies automatically.
func (b *Blob) Read(ctx context.Context) (io.ReadCloser, error) {
	// If content is already loaded in memory, return it
	if data, ok := b.contentData.peek(); ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	// Otherwise, fetch from storage with digest verification
	if b.fetcher == nil {
		return nil, ErrNoFetcher
	}
	rc, err := b.fetcher.Fetch(ctx, b.descriptor)
	if err != nil {
		return nil, err
	}

	// Wrap with VerifyReader if descriptor has a valid digest
	if b.descriptor.Digest.Validate() == nil {
		vr := content.NewVerifyReader(rc, b.descriptor)
		return &verifyReadCloser{vr: vr, closer: rc}, nil
	}

	return rc, nil
}

// verifyReadCloser wraps a content.VerifyReader with a Closer.
type verifyReadCloser struct {
	vr     *content.VerifyReader
	closer io.Closer
}

func (v *verifyReadCloser) Read(p []byte) (int, error) {
	return v.vr.Read(p)
}

func (v *verifyReadCloser) Close() error {
	return v.closer.Close()
}

// Bytes returns the blob content as a byte slice.
// The content is lazily loaded and cached for subsequent calls.
// On transient errors, the result is NOT cached, allowing retry.
// Use Read() for streaming large blobs to avoid memory pressure.
func (b *Blob) Bytes(ctx context.Context) ([]byte, error) {
	return b.contentData.get(func() ([]byte, error) {
		if b.fetcher == nil {
			return nil, ErrNoFetcher
		}
		return content.FetchAll(ctx, b.fetcher, b.descriptor)
	})
}

// Push pushes this blob to the target storage.
func (b *Blob) Push(ctx context.Context) error {
	if b.pusher == nil {
		return ErrNoPusher
	}

	var reader io.Reader
	if data, ok := b.contentData.peek(); ok {
		reader = bytes.NewReader(data)
	} else if b.fetcher != nil {
		// Stream from fetcher to pusher
		rc, err := b.fetcher.Fetch(ctx, b.descriptor)
		if err != nil {
			return err
		}
		defer rc.Close()
		reader = rc
	} else {
		return ErrNoContent
	}

	return b.pusher.Push(ctx, b.descriptor, reader)
}

// WithAnnotation returns a new Blob with the given annotation added.
// The original blob is not modified.
func (b *Blob) WithAnnotation(key, value string) *Blob {
	desc := b.descriptor
	// Deep-copy the annotations map to avoid mutating the original.
	newAnnotations := make(map[string]string, len(desc.Annotations)+1)
	maps.Copy(newAnnotations, desc.Annotations)
	newAnnotations[key] = value
	desc.Annotations = newAnnotations

	newBlob := &Blob{
		descriptor: desc,
		fetcher:    b.fetcher,
		pusher:     b.pusher,
	}
	// Copy cached content if available.
	if data, ok := b.contentData.peek(); ok {
		newBlob.contentData.set(data)
	}
	return newBlob
}
