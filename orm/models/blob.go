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
	"sync"

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

	// Lazy-loaded content
	contentBytes []byte
	contentOnce  sync.Once
	contentErr   error
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
func NewBlobFromBytes(mediaType string, data []byte) *Blob {
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	return &Blob{
		descriptor:   desc,
		contentBytes: data,
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

// Annotations returns the annotations associated with this blob.
func (b *Blob) Annotations() map[string]string {
	return b.descriptor.Annotations
}

// Read returns a ReadCloser for streaming the blob content.
// This is useful for large blobs that should not be loaded entirely into memory.
func (b *Blob) Read(ctx context.Context) (io.ReadCloser, error) {
	// If content is already loaded in memory, return it
	if b.contentBytes != nil {
		return io.NopCloser(bytes.NewReader(b.contentBytes)), nil
	}

	// Otherwise, fetch from storage
	if b.fetcher == nil {
		return nil, ErrNoFetcher
	}
	return b.fetcher.Fetch(ctx, b.descriptor)
}

// Bytes returns the blob content as a byte slice.
// The content is lazily loaded and cached for subsequent calls.
// Use Read() for streaming large blobs to avoid memory pressure.
func (b *Blob) Bytes(ctx context.Context) ([]byte, error) {
	b.contentOnce.Do(func() {
		if b.contentBytes != nil {
			return // Already have content
		}

		if b.fetcher == nil {
			b.contentErr = ErrNoFetcher
			return
		}

		// Fetch content
		b.contentBytes, b.contentErr = content.FetchAll(ctx, b.fetcher, b.descriptor)
	})

	return b.contentBytes, b.contentErr
}

// Push pushes this blob to the target storage.
func (b *Blob) Push(ctx context.Context) error {
	if b.pusher == nil {
		return ErrNoPusher
	}

	var reader io.Reader
	if b.contentBytes != nil {
		reader = bytes.NewReader(b.contentBytes)
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
	if desc.Annotations == nil {
		desc.Annotations = make(map[string]string)
	}
	desc.Annotations[key] = value

	return &Blob{
		descriptor:   desc,
		fetcher:      b.fetcher,
		pusher:       b.pusher,
		contentBytes: b.contentBytes,
	}
}
