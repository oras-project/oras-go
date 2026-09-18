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

package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/errutil"
)

// headerOCIChunkMinLength is the response header a registry uses to advertise
// the minimum size, in bytes, of every chunk except the last.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-a-blob-in-chunks
const headerOCIChunkMinLength = "OCI-Chunk-Min-Length"

// errChunkedUploadNotStarted signals that a chunked upload failed before any
// byte of the content was consumed. The caller may safely fall back to a
// monolithic upload with the original reader.
var errChunkedUploadNotStarted = errors.New("chunked blob upload not started")

// chunkedUpload holds the mutable state of a single chunked upload session.
type chunkedUpload struct {
	// location is the next URL to PATCH/PUT, updated after each request.
	location *url.URL
	// chunk is the effective chunk size, raised to any registry-advertised
	// OCI-Chunk-Min-Length.
	chunk int64
	// offset is the number of bytes uploaded so far (the next Content-Range
	// start).
	offset int64
	// consumed reports whether at least one PATCH has been issued; after that
	// the reader cannot be rewound, so a monolithic fallback is impossible.
	consumed bool
	// digester computes the digest of the streamed content.
	digester digest.Digester
}

// pushChunked uploads expected in chunks per the OCI Distribution Spec: it opens
// an upload session (POST), streams content in PATCH requests of the effective
// chunk size, and closes the session with a final PUT carrying the digest.
//
// If the session cannot be opened before any content is consumed, it returns an
// error wrapping [errChunkedUploadNotStarted] so the caller can fall back to a
// monolithic upload. Once the first PATCH is issued the reader is consumed and
// no fallback is possible; later failures return a wrapped error and
// best-effort cancel the session.
//
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-a-blob-in-chunks
func (s *blobStore) pushChunked(ctx context.Context, expected ocispec.Descriptor, content io.Reader, chunkSize int64) error {
	up, err := s.openUploadSession(ctx, chunkSize)
	if err != nil {
		// The session never opened; no bytes consumed, fallback is safe.
		return fmt.Errorf("%w: %w", errChunkedUploadNotStarted, err)
	}

	if err := s.uploadChunks(ctx, up, content); err != nil {
		return err
	}

	if computed := up.digester.Digest(); computed != expected.Digest {
		s.cancelUpload(ctx, up.location)
		return fmt.Errorf("chunked blob push: content digest %s does not match expected digest %s", computed, expected.Digest)
	}
	if up.offset != expected.Size {
		s.cancelUpload(ctx, up.location)
		return fmt.Errorf("chunked blob push: uploaded %d bytes, expected %d", up.offset, expected.Size)
	}

	return s.closeUploadSession(ctx, up, expected.Digest)
}

// openUploadSession performs the POST that starts a blob upload and returns the
// initialised session state. A non-sha256 expected digest advertises its
// algorithm via the digest-algorithm query parameter. The returned session has
// its chunk size raised to any registry-advertised OCI-Chunk-Min-Length.
func (s *blobStore) openUploadSession(ctx context.Context, chunkSize int64) (*chunkedUpload, error) {
	url := buildRepositoryBlobUploadURL(s.repo.plainHTTP(), s.repo.reference())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.ContentLength = 0

	resp, err := s.repo.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, errutil.ParseErrorResponse(resp)
	}
	location, err := resolveUploadLocation(resp, req)
	if err != nil {
		return nil, err
	}
	if minLen := parseChunkMinLength(resp); minLen > chunkSize {
		chunkSize = minLen
	}
	return &chunkedUpload{
		location: location,
		chunk:    chunkSize,
		digester: digest.Canonical.Digester(),
	}, nil
}

// uploadChunks streams content in PATCH requests of up.chunk bytes, updating the
// running digest, byte offset, and push location as it goes. It sets
// up.consumed once the first PATCH is issued; after that point failures cancel
// the session.
func (s *blobStore) uploadChunks(ctx context.Context, up *chunkedUpload, content io.Reader) error {
	hasher := up.digester.Hash()
	buf := make([]byte, up.chunk)
	for {
		n, readErr := io.ReadFull(content, buf)
		if readErr == io.EOF {
			// Clean end of stream on a chunk boundary.
			return nil
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			if up.consumed {
				s.cancelUpload(ctx, up.location)
			}
			return fmt.Errorf("chunked blob push: failed to read content after %d bytes: %w", up.offset, readErr)
		}
		if n == 0 {
			return nil
		}
		_, _ = hasher.Write(buf[:n])

		req, err := http.NewRequestWithContext(ctx, http.MethodPatch, up.location.String(), bytes.NewReader(buf[:n]))
		if err != nil {
			if up.consumed {
				s.cancelUpload(ctx, up.location)
			}
			return fmt.Errorf("chunked blob push: failed to build PATCH request: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", fmt.Sprintf("%d-%d", up.offset, up.offset+int64(n)-1))
		req.ContentLength = int64(n)
		up.consumed = true

		resp, err := s.repo.do(req)
		if err != nil {
			return fmt.Errorf("chunked blob push: PATCH failed after %d bytes: %w", up.offset, err)
		}
		if resp.StatusCode != http.StatusAccepted {
			respErr := errutil.ParseErrorResponse(resp)
			resp.Body.Close()
			s.cancelUpload(ctx, up.location)
			return fmt.Errorf("chunked blob push: unexpected PATCH status %d after %d bytes: %w", resp.StatusCode, up.offset, respErr)
		}
		next, err := resolveUploadLocation(resp, req)
		resp.Body.Close()
		if err != nil {
			s.cancelUpload(ctx, up.location)
			return fmt.Errorf("chunked blob push: invalid PATCH location after %d bytes: %w", up.offset, err)
		}
		up.location = next
		up.offset += int64(n)
	}
}

// closeUploadSession issues the final PUT that commits the blob under
// finalDigest and verifies the registry-reported digest when present.
func (s *blobStore) closeUploadSession(ctx context.Context, up *chunkedUpload, finalDigest digest.Digest) error {
	q := up.location.Query()
	q.Set("digest", finalDigest.String())
	up.location.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.location.String(), http.NoBody)
	if err != nil {
		s.cancelUpload(ctx, up.location)
		return fmt.Errorf("chunked blob push: failed to build PUT request: %w", err)
	}
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := s.repo.do(req)
	if err != nil {
		return fmt.Errorf("chunked blob push: PUT failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("chunked blob push: unexpected PUT status %d: %w", resp.StatusCode, errutil.ParseErrorResponse(resp))
	}
	if returned := resp.Header.Get("Docker-Content-Digest"); returned != "" && returned != finalDigest.String() {
		return fmt.Errorf("chunked blob push: registry returned digest %q, expected %q", returned, finalDigest.String())
	}
	return nil
}

// cancelUpload issues a best-effort DELETE to release an in-progress upload
// session. Per the OCI spec, clients SHOULD ignore any failures.
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#canceling-a-blob-upload
func (s *blobStore) cancelUpload(ctx context.Context, location *url.URL) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, location.String(), http.NoBody)
	if err != nil {
		return
	}
	resp, err := s.repo.do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// parseChunkMinLength reads the registry-advertised OCI-Chunk-Min-Length header
// from a session response, returning 0 when absent, unparsable, or negative.
func parseChunkMinLength(resp *http.Response) int64 {
	v := resp.Header.Get(headerOCIChunkMinLength)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
