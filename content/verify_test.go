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
	"bytes"
	"context"
	"crypto/sha1"
	_ "crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// tracker wraps a reader and records whether Close() was called.
type tracker struct {
	io.Reader
	closed int
}

func (c *tracker) Close() error {
	c.closed++
	return nil
}

// errReader returns the given error after serving the given prefix.
type errReader struct {
	prefix []byte
	err    error
}

func (r *errReader) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	return 0, r.err
}

// fetcherOf returns a Fetcher that serves r regardless of the descriptor
// requested, and records the tracker so tests can assert on Close().
func fetcherOf(r io.Reader) (Fetcher, *tracker) {
	tracker := &tracker{Reader: r}
	return FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		return tracker, nil
	}), tracker
}

func TestVerify_CorrectContent(t *testing.T) {
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	if err := Verify(context.Background(), fetcher, desc); err != nil {
		t.Fatal("Verify() error = ", err)
	}
}

func TestVerify_EmptyContent(t *testing.T) {
	content := []byte("")
	desc := NewDescriptorFromBytes("test", content)
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	if err := Verify(context.Background(), fetcher, desc); err != nil {
		t.Fatal("Verify() error = ", err)
	}
}

func TestVerify_LargeContent(t *testing.T) {
	// Content larger than maxInitialBufferSize must still be verified in
	// full. Verify streams to io.Discard, so it must not depend on the
	// content fitting in memory or in ReadAll's initial buffer.
	size := maxInitialBufferSize + 1024*1024 // 33 MiB
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}
	desc := NewDescriptorFromBytes("test", content)
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	if err := Verify(context.Background(), fetcher, desc); err != nil {
		t.Fatal("Verify() error = ", err)
	}
}

func TestVerify_PassesContextAndDescriptorToFetcher(t *testing.T) {
	type ctxKey struct{}
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	ctx := context.WithValue(context.Background(), ctxKey{}, "value")

	var gotCtx context.Context
	var gotDesc ocispec.Descriptor
	fetcher := FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		gotCtx = ctx
		gotDesc = target
		return io.NopCloser(bytes.NewReader(content)), nil
	})

	if err := Verify(ctx, fetcher, desc); err != nil {
		t.Fatal("Verify() error = ", err)
	}
	if gotCtx != ctx {
		t.Errorf("Fetch() ctx = %v, want %v", gotCtx, ctx)
	}
	if !Equal(gotDesc, desc) {
		t.Errorf("Fetch() desc = %v, want %v", gotDesc, desc)
	}
}

func TestVerify_FetchError(t *testing.T) {
	desc := NewDescriptorFromBytes("test", []byte("example content"))
	wantErr := errors.New("fetch error")
	fetcher := FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		return nil, wantErr
	})

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, wantErr) {
		t.Errorf("Verify() error = %v, want %v", err, wantErr)
	}
}

func TestVerify_ContextCanceled(t *testing.T) {
	desc := NewDescriptorFromBytes("test", []byte("example content"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetcher := FetcherFunc(func(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
		return nil, ctx.Err()
	})

	err := Verify(ctx, fetcher, desc)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Verify() error = %v, want %v", err, context.Canceled)
	}
}

func TestVerify_MismatchedDigest(t *testing.T) {
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", []byte("another content"))
	// same size as the descriptor so only the digest can differ
	content = content[:len("another content")]
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, ErrMismatchedDigest) {
		t.Errorf("Verify() error = %v, want %v", err, ErrMismatchedDigest)
	}
}

func TestVerify_ContentShorterThanDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Verify() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestVerify_ContentLongerThanDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) - 1),
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, ErrTrailingData) {
		t.Errorf("Verify() error = %v, want %v", err, ErrTrailingData)
	}
}

func TestVerify_OversizedDescriptor(t *testing.T) {
	// A crafted descriptor can declare an enormous Size (here 2^62). Verify
	// must neither panic nor allocate based on it: it streams the actual
	// content and fails because the content is shorter than declared.
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      4611686018427387904, // 2^62
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Verify() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestVerify_InvalidDigest(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    "invalid-digest",
		Size:      int64(len(content)),
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, digest.ErrDigestInvalidFormat) {
		t.Errorf("Verify() error = %v, want %v", err, digest.ErrDigestInvalidFormat)
	}
}

func TestVerify_UnsupportedAlgorithm_SHA1(t *testing.T) {
	content := []byte("example content")
	h := sha1.New()
	h.Write(content)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		Size:      int64(len(content)),
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, digest.ErrDigestUnsupported) {
		t.Errorf("Verify() error = %v, want %v", err, digest.ErrDigestUnsupported)
	}
}

func TestVerify_NegativeDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      -1,
	}
	fetcher, _ := fetcherOf(bytes.NewReader(content))

	if err := Verify(context.Background(), fetcher, desc); !errors.Is(err, ErrTrailingData) {
		t.Errorf("Verify() error = %v, want %v", err, ErrTrailingData)
	}
}

func TestVerify_ReadError(t *testing.T) {
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	wantErr := errors.New("read error")
	// the read fails partway through the blob
	fetcher, _ := fetcherOf(&errReader{prefix: content[:5], err: wantErr})

	err := Verify(context.Background(), fetcher, desc)
	if !errors.Is(err, wantErr) {
		t.Errorf("Verify() error = %v, want %v", err, wantErr)
	}
}

func TestVerify_ClosesReader(t *testing.T) {
	content := []byte("example content")

	tests := []struct {
		name string
		desc ocispec.Descriptor
		r    io.Reader
	}{
		{
			name: "on success",
			desc: NewDescriptorFromBytes("test", content),
			r:    bytes.NewReader(content),
		},
		{
			name: "on mismatched digest",
			desc: NewDescriptorFromBytes("test", []byte("another content")),
			r:    bytes.NewReader(content[:len("another content")]),
		},
		{
			name: "on read error",
			desc: NewDescriptorFromBytes("test", content),
			r:    &errReader{prefix: content[:5], err: errors.New("read error")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher, tracker := fetcherOf(tt.r)
			_ = Verify(context.Background(), fetcher, tt.desc)
			if tracker.closed != 1 {
				t.Errorf("Close() called %d time(s), want 1", tracker.closed)
			}
		})
	}
}

func TestVerify_DoesNotReturnContent(t *testing.T) {
	// Verify only reports integrity; it must drain the reader fully so the
	// whole blob was actually checked, not just a prefix.
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader(content)
	fetcher, _ := fetcherOf(r)

	if err := Verify(context.Background(), fetcher, desc); err != nil {
		t.Fatal("Verify() error = ", err)
	}
	if r.Len() != 0 {
		t.Errorf("unread bytes after Verify() = %d, want 0", r.Len())
	}
}
