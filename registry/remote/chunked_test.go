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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
	uploaded      []byte // blob bytes assembled across PATCH and/or PUT
	digestQuery   string // digest query parameter seen on the closing PUT
	cancelled     bool   // whether a DELETE (cancel) was received

	// minChunkLength, when > 0, is advertised via OCI-Chunk-Min-Length on the
	// session POST response.
	minChunkLength int64
	// rejectPOSTStatus, when non-zero, is returned to the session POST to force
	// a pre-consumption fallback to monolithic upload.
	rejectPOSTStatus int
	// rejectPATCHStatus, when non-zero, is returned to the first PATCH.
	rejectPATCHStatus int
}

func (reg *chunkedUploadRegistry) handler() http.HandlerFunc {
	const sessionPath = "/v2/test/blobs/uploads/session"
	return func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.methods = append(reg.methods, r.Method)
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
			w.Header().Set("Location", sessionPath)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == sessionPath:
			body := bytes.NewBuffer(nil)
			if _, err := body.ReadFrom(r.Body); err != nil {
				reg.t.Errorf("failed to read PUT body: %v", err)
			}
			reg.mu.Lock()
			reg.uploaded = append(reg.uploaded, body.Bytes()...)
			reg.digestQuery = r.URL.Query().Get("digest")
			reg.mu.Unlock()
			w.Header().Set("Docker-Content-Digest", reg.digestQuery)
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
