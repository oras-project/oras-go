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
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
)

// chunkedUploadRegistry is a minimal in-memory registry that implements the OCI
// chunked blob upload protocol (POST session, PATCH chunks, PUT close) as well
// as the monolithic POST/PUT path. It records the method sequence and the
// Content-Range of every PATCH so tests can assert the exact wire behavior.
type chunkedUploadRegistry struct {
	t *testing.T

	mu            sync.Mutex
	methods       []string
	contentRanges []string
	auths         []string // Authorization header seen on each request
	uploaded      []byte   // blob bytes assembled across PATCH and/or PUT
	digestQuery   string   // digest query parameter seen on the closing PUT
	cancelled     bool     // whether a DELETE (cancel) was received

	// minChunkLength, when > 0, is advertised via OCI-Chunk-Min-Length on the
	// session POST response.
	minChunkLength int64
	// rejectPOSTStatus, when non-zero, is returned to the session POST to force
	// a pre-consumption fallback to monolithic upload.
	rejectPOSTStatus int
	// rejectPATCHStatus, when non-zero, is returned to the first PATCH.
	rejectPATCHStatus int
	// rejectPUTStatus, when non-zero, is returned to the closing PUT.
	rejectPUTStatus int
	// patchLocationOverride, when non-empty, overrides the Location header on PATCH responses.
	patchLocationOverride string
	// putDigestOverride, when non-empty, is returned as the PUT response
	// Docker-Content-Digest header instead of the real digest.
	putDigestOverride string
}

func (reg *chunkedUploadRegistry) handler() http.HandlerFunc {
	const sessionPath = "/v2/test/blobs/uploads/session"
	return func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.methods = append(reg.methods, r.Method)
		reg.auths = append(reg.auths, r.Header.Get("Authorization"))
		reg.mu.Unlock()

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			if reg.rejectPOSTStatus != 0 {
				w.WriteHeader(reg.rejectPOSTStatus)
				return
			}
			if reg.minChunkLength > 0 {
				w.Header().Set("OCI-Chunk-Min-Length", fmt.Sprintf("%d", reg.minChunkLength))
			}
			w.Header().Set("Location", sessionPath)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPatch && r.URL.Path == sessionPath:
			if reg.rejectPATCHStatus != 0 {
				w.WriteHeader(reg.rejectPATCHStatus)
				return
			}
			body := bytes.NewBuffer(nil)
			if _, err := body.ReadFrom(r.Body); err != nil {
				reg.t.Errorf("failed to read PATCH body: %v", err)
			}
			reg.mu.Lock()
			reg.contentRanges = append(reg.contentRanges, r.Header.Get("Content-Range"))
			reg.uploaded = append(reg.uploaded, body.Bytes()...)
			reg.mu.Unlock()
			loc := sessionPath
			if reg.patchLocationOverride != "" {
				loc = reg.patchLocationOverride
			}
			w.Header().Set("Location", loc)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == sessionPath:
			if reg.rejectPUTStatus != 0 {
				w.WriteHeader(reg.rejectPUTStatus)
				return
			}
			body := bytes.NewBuffer(nil)
			if _, err := body.ReadFrom(r.Body); err != nil {
				reg.t.Errorf("failed to read PUT body: %v", err)
			}
			reg.mu.Lock()
			reg.uploaded = append(reg.uploaded, body.Bytes()...)
			reg.digestQuery = r.URL.Query().Get("digest")
			returnedDigest := reg.digestQuery
			if reg.putDigestOverride != "" {
				returnedDigest = reg.putDigestOverride
			}
			reg.mu.Unlock()
			w.Header().Set("Docker-Content-Digest", returnedDigest)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v2/test/blobs/uploads/"):
			// monolithic PUT (fallback path uses a different session location)
			body := bytes.NewBuffer(nil)
			if _, err := body.ReadFrom(r.Body); err != nil {
				reg.t.Errorf("failed to read monolithic PUT body: %v", err)
			}
			reg.mu.Lock()
			reg.uploaded = append(reg.uploaded, body.Bytes()...)
			reg.digestQuery = r.URL.Query().Get("digest")
			reg.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == sessionPath:
			reg.mu.Lock()
			reg.cancelled = true
			reg.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusForbidden)
			reg.t.Errorf("unexpected access: %s %s", r.Method, r.URL)
		}
	}
}

func (reg *chunkedUploadRegistry) methodSequence() []string {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]string, len(reg.methods))
	copy(out, reg.methods)
	return out
}

func (reg *chunkedUploadRegistry) authHeaders() []string {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]string, len(reg.auths))
	copy(out, reg.auths)
	return out
}

func (reg *chunkedUploadRegistry) patchCount() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	n := 0
	for _, m := range reg.methods {
		if m == http.MethodPatch {
			n++
		}
	}
	return n
}

// newChunkedTestRepo builds a Repository targeting srv with chunking configured.
func newChunkedTestRepo(t *testing.T, srv *httptest.Server, maxChunkSize int64) *Repository {
	t.Helper()
	uri, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	// Use a non-retrying client so error responses fail fast instead of
	// exercising the default retry backoff.
	repo.Registry.Client = &auth.Client{Client: &http.Client{}}
	repo.MaxChunkSize = maxChunkSize
	return repo
}

func TestRepository_Push_Chunked(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello") // 5 bytes
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2 /* MaxChunkSize */)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// Exact protocol: POST, three PATCH (2+2+1), PUT.
	wantMethods := []string{
		http.MethodPost,
		http.MethodPatch, http.MethodPatch, http.MethodPatch,
		http.MethodPut,
	}
	if got := reg.methodSequence(); !equalStrings(got, wantMethods) {
		t.Fatalf("method sequence = %v, want %v", got, wantMethods)
	}
	wantRanges := []string{"0-1", "2-3", "4-4"}
	if got := reg.contentRanges; !equalStrings(got, wantRanges) {
		t.Errorf("content ranges = %v, want %v", got, wantRanges)
	}
	if reg.digestQuery != desc.Digest.String() {
		t.Errorf("PUT digest = %q, want %q", reg.digestQuery, desc.Digest.String())
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_BelowSingleChunkIsMonolithic(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hi") // 2 bytes, <= chunk size
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 8 /* MaxChunkSize > blob size */)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if n := reg.patchCount(); n != 0 {
		t.Errorf("expected monolithic push (0 PATCH), got %d PATCH", n)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_DefaultIsMonolithic(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world, this is a larger blob")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 0 /* chunking disabled */)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if n := reg.patchCount(); n != 0 {
		t.Errorf("expected monolithic push (0 PATCH), got %d PATCH", n)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_ChunkFallbackWhenSessionRejected(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, rejectPOSTStatus: http.StatusNotFound}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world, chunk me")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	// The session POST returns 404. Push must fall back to monolithic. Since the
	// same uploads endpoint is used, model the fallback: first POST 404s, then
	// the monolithic POST (a second POST) succeeds. Reconfigure the registry so
	// the first POST 404s and subsequent POSTs succeed.
	var postCount int
	var mu sync.Mutex
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" {
			mu.Lock()
			postCount++
			n := postCount
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusNotFound) // reject chunked session
				return
			}
			w.Header().Set("Location", "/v2/test/blobs/uploads/mono")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/mono" {
			body := bytes.NewBuffer(nil)
			_, _ = body.ReadFrom(r.Body)
			mu.Lock()
			reg.uploaded = append(reg.uploaded[:0], body.Bytes()...)
			reg.digestQuery = r.URL.Query().Get("digest")
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			return
		}
		if r.Method == http.MethodPatch {
			t.Errorf("fallback must not issue PATCH")
		}
		w.WriteHeader(http.StatusForbidden)
	})

	repo := newChunkedTestRepo(t, srv, 4 /* chunking enabled but session 404s */)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
	if reg.digestQuery != desc.Digest.String() {
		t.Errorf("PUT digest = %q, want %q", reg.digestQuery, desc.Digest.String())
	}
}

func TestRepository_Push_ChunkMinLengthRaisesChunkSize(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, minChunkLength: 4}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("abcdefgh") // 8 bytes
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	// MaxChunkSize=2 but the registry advertises a 4-byte minimum, so chunks are
	// 4 bytes: ranges 0-3 and 4-7.
	repo := newChunkedTestRepo(t, srv, 2)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	wantRanges := []string{"0-3", "4-7"}
	if got := reg.contentRanges; !equalStrings(got, wantRanges) {
		t.Errorf("content ranges = %v, want %v", got, wantRanges)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_ChunkDigestMismatch(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	// Deliberately wrong digest.
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes([]byte("not the blob")),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match expected digest") {
		t.Errorf("error = %v, want digest mismatch", err)
	}
	if !reg.cancelled {
		t.Error("expected upload session to be cancelled on digest mismatch")
	}
}

func TestRepository_Push_ChunkPATCHRejectedAfterConsuming(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, rejectPATCHStatus: http.StatusInternalServerError}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected PATCH error, got nil")
	}
	// No monolithic fallback once bytes are consumed.
	if !strings.Contains(err.Error(), "PATCH") {
		t.Errorf("error = %v, want PATCH failure", err)
	}
	if !reg.cancelled {
		t.Error("expected upload session to be cancelled after PATCH rejection")
	}
}

func TestRepository_Push_ChunkedExactBoundary(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("abcd") // 4 bytes, an exact multiple of the 2-byte chunk
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	// Two full chunks, no trailing empty PATCH: ranges 0-1 and 2-3.
	wantRanges := []string{"0-1", "2-3"}
	if got := reg.contentRanges; !equalStrings(got, wantRanges) {
		t.Errorf("content ranges = %v, want %v", got, wantRanges)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_ChunkedRegistryDigestMismatch(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, putDigestOverride: digest.FromBytes([]byte("wrong")).String()}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected error when registry returns a mismatching digest, got nil")
	}
	if !strings.Contains(err.Error(), "registry returned digest") {
		t.Errorf("error = %v, want registry digest mismatch", err)
	}
}

func TestRepository_Push_ChunkedViaRepositoryPush(t *testing.T) {
	// Exercise the top-level Repository.Push entrypoint (not repo.Blobs()) that
	// real callers use, ensuring MaxChunkSize flows through to the blob store.
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	if err := repo.Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if reg.patchCount() == 0 {
		t.Error("expected chunked push via Repository.Push, got no PATCH")
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type chunkedRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chunkedRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRepository_Push_ChunkedNonSHA256(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world, sha512 chunked push test")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.SHA512.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 4)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if reg.patchCount() == 0 {
		t.Errorf("expected chunked upload (multiple PATCHes), got 0")
	}
	if reg.digestQuery != desc.Digest.String() {
		t.Errorf("PUT digest = %q, want %q", reg.digestQuery, desc.Digest.String())
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_ChunkedUnsupportedDigestAlgorithm(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.Digest("unsupported:abcdef1234567890"),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected error with unsupported digest algorithm, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Errorf("error = %v, want unsupported digest algorithm", err)
	}
	if !reg.cancelled {
		t.Error("expected session to be cancelled on unsupported algorithm")
	}
}

func TestRepository_Push_ChunkedMinLengthHuge(t *testing.T) {
	// A registry response with OCI-Chunk-Min-Length larger than the blob itself
	// (or even 1<<62) must not panic with 'makeslice: len out of range'.
	// It should cap chunk size to expected.Size.
	reg := &chunkedUploadRegistry{t: t, minChunkLength: 1 << 62}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello chunked")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_ChunkedOverlongReader(t *testing.T) {
	// A reader that produces more bytes than declared in expected.Size must be
	// bounded at expected.Size and not upload extra trailing bytes.
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello") // 5 bytes
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	overlongData := append(blob, []byte("extra data that should not be uploaded")...)
	repo := newChunkedTestRepo(t, srv, 2)
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(overlongData)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
	if len(reg.uploaded) != len(blob) {
		t.Errorf("uploaded len = %d, want %d", len(reg.uploaded), len(blob))
	}
}

func TestRepository_Push_ChunkedCarriesAuthorization(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	// Transport only injects Authorization on POST; PATCH and PUT must carry
	// it forward from the session POST response.
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: chunkedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					req.Header.Set("Authorization", "Bearer session-token")
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	seq := reg.methodSequence()
	for i, authHeader := range reg.authHeaders() {
		if authHeader != "Bearer session-token" {
			t.Errorf("request %d (%s) authorization = %q, want %q", i, seq[i], authHeader, "Bearer session-token")
		}
	}
}

func TestRepository_Push_ChunkedPATCHTransportErrorCancelsSession(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	// Transport that succeeds on POST and DELETE, but fails on PATCH
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: chunkedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPatch {
					return nil, errors.New("simulated network failure")
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected error on PATCH transport failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated network failure") {
		t.Errorf("error = %v, want simulated network failure", err)
	}
	if !reg.cancelled {
		t.Error("expected session to be cancelled on PATCH transport error")
	}
}

func TestBlobStore_cancelUpload_ContextCancelled(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	repo := newChunkedTestRepo(t, srv, 2)
	sessionURL, err := url.Parse(srv.URL + "/v2/test/blobs/uploads/session")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // deliberately cancel context before calling cancelUpload

	repo.Blobs().(*blobStore).cancelUpload(ctx, sessionURL)
	if !reg.cancelled {
		t.Error("expected cancelUpload to issue DELETE even with cancelled context")
	}
}

type errReader struct {
	err error
}

func (r *errReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

func TestRepository_Push_ChunkedReaderError(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes([]byte("hello world")),
		Size:      11,
	}

	repo := newChunkedTestRepo(t, srv, 2)
	r := io.MultiReader(bytes.NewReader([]byte("he")), &errReader{err: errors.New("simulated read error")})
	err := repo.Blobs().Push(context.Background(), desc, r)
	if err == nil {
		t.Fatal("expected reader error, got nil")
	}
	if !strings.Contains(err.Error(), "simulated read error") {
		t.Errorf("error = %v, want simulated read error", err)
	}
	if !reg.cancelled {
		t.Error("expected session to be cancelled on read error after consumption")
	}
}

func TestRepository_Push_ChunkedSizeMismatchUnderflow(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("short")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      100, // claims 100 bytes, only provides 5
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected size mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "uploaded 5 bytes, expected 100") {
		t.Errorf("error = %v, want uploaded 5 bytes, expected 100", err)
	}
	if !reg.cancelled {
		t.Error("expected session to be cancelled on size mismatch")
	}
}

func TestRepository_Push_ChunkPostConsumptionFailureDoesNotFallback(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, rejectPATCHStatus: http.StatusInternalServerError}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected PATCH failure via repo.Push, got nil")
	}
	if !strings.Contains(err.Error(), "PATCH") {
		t.Errorf("error = %v, want PATCH failure", err)
	}
}

func TestParseChunkMinLength(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   int64
	}{
		{"absent", "", 0},
		{"valid", "1024", 1024},
		{"negative", "-50", 0},
		{"invalid", "not-a-number", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: make(http.Header),
			}
			if tt.header != "" {
				resp.Header.Set(headerOCIChunkMinLength, tt.header)
			}
			if got := parseChunkMinLength(resp); got != tt.want {
				t.Errorf("parseChunkMinLength() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveUploadLocation(t *testing.T) {
	reqURL, err := url.Parse("https://registry.example.com/v2/test/blobs/uploads/")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, reqURL.String(), nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	t.Run("missing location", func(t *testing.T) {
		resp := &http.Response{Header: make(http.Header), Request: req}
		if _, err := resolveUploadLocation(resp, req); err == nil {
			t.Error("expected error for missing Location header")
		}
	})

	t.Run("port 443 preserved", func(t *testing.T) {
		req443URL, err := url.Parse("https://registry.example.com:443/v2/test/blobs/uploads/")
		if err != nil {
			t.Fatalf("url.Parse() error = %v", err)
		}
		req443, err := http.NewRequest(http.MethodPost, req443URL.String(), nil)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		resp := &http.Response{
			Header:  http.Header{"Location": []string{"https://registry.example.com/v2/test/blobs/uploads/session"}},
			Request: req443,
		}
		loc, err := resolveUploadLocation(resp, req443)
		if err != nil {
			t.Fatalf("resolveUploadLocation() error = %v", err)
		}
		if loc.Host != "registry.example.com:443" {
			t.Errorf("loc.Host = %q, want %q", loc.Host, "registry.example.com:443")
		}
	})

	t.Run("cross host rejected", func(t *testing.T) {
		resp := &http.Response{
			Header:  http.Header{"Location": []string{"https://attacker.com/v2/test/blobs/uploads/session"}},
			Request: req,
		}
		if _, err := resolveUploadLocation(resp, req); err == nil || !strings.Contains(err.Error(), "different host") {
			t.Errorf("expected different host error, got %v", err)
		}
	})

	t.Run("scheme downgrade rejected", func(t *testing.T) {
		reqCustomPortURL, err := url.Parse("https://registry.example.com:5000/v2/test/blobs/uploads/")
		if err != nil {
			t.Fatalf("url.Parse() error = %v", err)
		}
		reqCustom, err := http.NewRequest(http.MethodPost, reqCustomPortURL.String(), nil)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		resp := &http.Response{
			Header:  http.Header{"Location": []string{"http://registry.example.com:5000/v2/test/blobs/uploads/session"}},
			Request: reqCustom,
		}
		if _, err := resolveUploadLocation(resp, reqCustom); err == nil || !strings.Contains(err.Error(), "downgrades scheme") {
			t.Errorf("expected scheme downgrade error, got %v", err)
		}
	})

	t.Run("valid relative location", func(t *testing.T) {
		resp := &http.Response{
			Header:  http.Header{"Location": []string{"/v2/test/blobs/uploads/session"}},
			Request: req,
		}
		loc, err := resolveUploadLocation(resp, req)
		if err != nil {
			t.Fatalf("resolveUploadLocation() error = %v", err)
		}
		if loc.String() != "https://registry.example.com/v2/test/blobs/uploads/session" {
			t.Errorf("loc = %q, want %q", loc.String(), "https://registry.example.com/v2/test/blobs/uploads/session")
		}
	})
}

func TestRepository_Push_ChunkedPUTStatusError(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, rejectPUTStatus: http.StatusInternalServerError}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected PUT status error, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected PUT status 500") {
		t.Errorf("error = %v, want unexpected PUT status 500", err)
	}
}

func TestRepository_Push_ChunkedPUTTransportError(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: chunkedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut {
					return nil, errors.New("simulated PUT failure")
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected PUT transport error, got nil")
	}
	if !strings.Contains(err.Error(), "simulated PUT failure") {
		t.Errorf("error = %v, want simulated PUT failure", err)
	}
}

func TestRepository_Push_ChunkedInvalidPATCHLocation(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, patchLocationOverride: "http://other-host.example.com/session"}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected invalid PATCH location error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PATCH location") {
		t.Errorf("error = %v, want invalid PATCH location error", err)
	}
}

func TestRepository_Push_ChunkedSessionPOSTTransportErrorFallsBack(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	var postCount int
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: chunkedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					postCount++
					if postCount == 1 {
						return nil, errors.New("simulated session POST network failure")
					}
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	// First POST (chunked) fails with transport error, falls back to monolithic POST/PUT
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
	if reg.patchCount() != 0 {
		t.Errorf("expected monolithic push (0 PATCH), got %d", reg.patchCount())
	}
}
