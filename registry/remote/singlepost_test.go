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
	_ "crypto/sha512"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

const singlePostSessionPath = "/v2/test/blobs/uploads/session"

// singlePostRegistry is a minimal in-memory registry that implements the
// single-request blob upload (POST ?digest=) as well as the POST/PUT
// monolithic upload. It records each request so tests can assert the exact
// wire behavior. A POST carrying ?digest= is logged as "POST?digest".
type singlePostRegistry struct {
	t *testing.T

	mu         sync.Mutex
	requests   []string
	postBodies [][]byte // body of every POST carrying ?digest=, in order, including challenged ones
	uploaded   []byte   // blob bytes committed by a 201

	// singlePOSTStatus, when non-zero, is returned to a POST carrying ?digest=
	// instead of committing the blob. A 202 also sets Location to location,
	// or to the default upload session if location is empty.
	singlePOSTStatus int
	location         string
	// responseDigest, when set, is returned in Docker-Content-Digest on a 201
	// to the single POST instead of the digest of the received body.
	responseDigest string
	// username and password, when set, make the registry answer every request
	// without matching basic credentials with a 401 challenge.
	username, password string
}

func (reg *singlePostRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		reg.t.Errorf("failed to read request body: %v", err)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()

	query := r.URL.Query()
	entry := r.Method
	if r.Method == http.MethodPost && query.Has("digest") {
		entry += "?digest"
		reg.postBodies = append(reg.postBodies, body)
	}
	reg.requests = append(reg.requests, entry)

	if reg.username != "" {
		if user, pass, ok := r.BasicAuth(); !ok || user != reg.username || pass != reg.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" && query.Has("digest"):
		switch reg.singlePOSTStatus {
		case 0:
		case http.StatusAccepted:
			location := reg.location
			if location == "" {
				location = singlePostSessionPath
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusAccepted)
			return
		default:
			w.WriteHeader(reg.singlePOSTStatus)
			return
		}
		if !reg.commit(w, r, body) {
			return
		}
		if reg.responseDigest != "" {
			w.Header().Set("Docker-Content-Digest", reg.responseDigest)
		}
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
		w.Header().Set("Location", singlePostSessionPath)
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodPut && r.URL.Path == singlePostSessionPath:
		if reg.commit(w, r, body) {
			w.WriteHeader(http.StatusCreated)
		}
	default:
		reg.t.Errorf("unexpected access: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

// commit stores body as the uploaded blob if the request is well formed, and
// answers 400 otherwise. It reports whether the blob was stored.
func (reg *singlePostRegistry) commit(w http.ResponseWriter, r *http.Request, body []byte) bool {
	dgst := r.URL.Query().Get("digest")
	if r.Header.Get("Content-Type") != "application/octet-stream" ||
		r.ContentLength != int64(len(body)) ||
		digest.FromBytes(body).String() != dgst {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	reg.uploaded = body
	w.Header().Set("Docker-Content-Digest", dgst)
	return true
}

func newSinglePostTestRepo(t *testing.T, srv *httptest.Server, threshold int64) *Repository {
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
	repo.SingleRequestPushThreshold = threshold
	return repo
}

func singlePostTestBlob() ([]byte, ocispec.Descriptor) {
	blob := []byte("hello world")
	return blob, ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
}

// nonSeekableReader hides every method but Read, like a progress-tracking
// wrapper, so net/http cannot set GetBody for it.
type nonSeekableReader struct{ io.Reader }

func TestRepository_Push_SingleRequest(t *testing.T) {
	blob, desc := singlePostTestBlob()
	for _, tt := range []struct {
		name         string
		threshold    int64
		maxChunkSize int64
		want         []string
	}{
		{name: "off", threshold: 0, want: []string{"POST", "PUT"}},
		{name: "negative is off", threshold: -1, want: []string{"POST", "PUT"}},
		{name: "above threshold", threshold: desc.Size - 1, want: []string{"POST", "PUT"}},
		{name: "at threshold", threshold: desc.Size, want: []string{"POST?digest"}},
		{name: "below threshold", threshold: desc.Size * 2, want: []string{"POST?digest"}},
		{
			// a blob that fits in a chunk is never chunked, so the single
			// POST still applies
			name: "chunking configured", threshold: desc.Size, maxChunkSize: desc.Size,
			want: []string{"POST?digest"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &singlePostRegistry{t: t}
			srv := httptest.NewServer(reg)
			defer srv.Close()
			repo := newSinglePostTestRepo(t, srv, tt.threshold)
			repo.MaxChunkSize = tt.maxChunkSize

			if err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(blob)}); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if !slices.Equal(reg.requests, tt.want) {
				t.Errorf("requests = %v, want %v", reg.requests, tt.want)
			}
			if !bytes.Equal(reg.uploaded, blob) {
				t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
			}
		})
	}
}

func TestRepository_Push_SingleRequest_EmptyBlob(t *testing.T) {
	reg := &singlePostRegistry{t: t}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, 1024)

	desc := ocispec.Descriptor{MediaType: "test", Digest: digest.FromBytes(nil), Size: 0}
	if err := repo.Push(context.Background(), desc, bytes.NewReader(nil)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if want := []string{"POST", "PUT"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests = %v, want %v", reg.requests, want)
	}
}

func TestRepository_Push_SingleRequest_ChunkedTakesPrecedence(t *testing.T) {
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
	repo.SingleRequestPushThreshold = desc.Size
	if err := repo.Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if reg.patchCount() == 0 {
		t.Errorf("methods = %v, want a chunked upload", reg.methods)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_SingleRequest_DigestHeaderMismatch(t *testing.T) {
	blob, desc := singlePostTestBlob()
	reg := &singlePostRegistry{t: t, responseDigest: digest.FromString("other").String()}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)

	err := repo.Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil || !strings.Contains(err.Error(), "digest mismatch in Docker-Content-Digest") {
		t.Errorf("Push() error = %v, want a Docker-Content-Digest mismatch", err)
	}
}

func TestRepository_Push_SingleRequest_DigestHeaderDifferentAlgorithm(t *testing.T) {
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.SHA512.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("digest") != desc.Digest.String() {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if !bytes.Equal(got, blob) {
			t.Errorf("body = %q, want %q", got, blob)
		}
		w.Header().Set(headerDockerContentDigest, digest.SHA256.FromBytes(blob).String())
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)

	err := repo.Push(context.Background(), desc, bytes.NewReader(blob))
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("Push() error = %v, want errdef.ErrUnsupported", err)
	}
	if errors.Is(err, content.ErrMismatchedDigest) {
		t.Errorf("error wraps content.ErrMismatchedDigest, want only ErrUnsupported")
	}
	var mismatch *digestHeaderMismatchError
	if errors.As(err, &mismatch) {
		t.Errorf("error is generic mismatch %v, want algorithm ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "sha256") || !strings.Contains(err.Error(), "sha512") {
		t.Errorf("error = %v, want both algorithms named", err)
	}
}

func TestRepository_Push_SingleRequest_ReplaysBodyOnAuthChallenge(t *testing.T) {
	blob, desc := singlePostTestBlob()
	reg := &singlePostRegistry{t: t, username: "user", password: "pass"}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)
	uri, _ := url.Parse(srv.URL)
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{},
		CredentialFunc: credentials.StaticCredentialFunc(uri.Host, credentials.Credential{
			Username: "user",
			Password: "pass",
		}),
	}

	// a non-seekable source cannot be rewound, so the push must buffer it
	if err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(blob)}); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if want := []string{"POST?digest", "POST?digest"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests = %v, want %v", reg.requests, want)
	}
	// the challenged attempt and the replay must carry the same bytes
	if want := [][]byte{blob, blob}; !slices.EqualFunc(reg.postBodies, want, bytes.Equal) {
		t.Errorf("POST bodies = %q, want %q", reg.postBodies, want)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_SingleRequest_RetriesAfterConnectionReset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a reset surfaces as WSAECONNRESET on windows, which is not retried")
	}
	blob, desc := singlePostTestBlob()
	reg := &singlePostRegistry{t: t}
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		if !first {
			reg.ServeHTTP(w, r)
			return
		}
		// read part of the body, then reset the connection instead of
		// answering, which net/http does not retry on its own
		_, _ = io.CopyN(io.Discard, r.Body, 5)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("failed to hijack the connection: %v", err)
			return
		}
		// a zero linger makes Close send a RST instead of a FIN
		if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
			t.Errorf("failed to set linger: %v", err)
		}
		conn.Close()
	}))
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)
	repo.Registry.Client = &auth.Client{Client: &http.Client{Transport: retry.NewTransport(http.DefaultTransport)}}

	if err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(blob)}); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if want := []string{"POST?digest"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests after the reset = %v, want %v", reg.requests, want)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_SingleRequest_SessionFallback(t *testing.T) {
	blob, desc := singlePostTestBlob()
	reg := &singlePostRegistry{t: t, singlePOSTStatus: http.StatusAccepted}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)

	if err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(blob)}); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	// the registry ignored ?digest= and opened a session, so the buffered
	// blob is PUT there without opening another one
	if want := []string{"POST?digest", "PUT"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests = %v, want %v", reg.requests, want)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
}

func TestRepository_Push_SingleRequest_SessionFallbackCrossHost(t *testing.T) {
	blob, desc := singlePostTestBlob()
	var mu sync.Mutex
	attackerHit := false
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attackerHit = true
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer attacker.Close()

	reg := &singlePostRegistry{
		t:                t,
		singlePOSTStatus: http.StatusAccepted,
		location:         attacker.URL + "/v2/test/blobs/uploads/evil",
	}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, desc.Size)

	err := repo.Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil || !strings.Contains(err.Error(), "different host") {
		t.Errorf("Push() error = %v, want a cross-host Location error", err)
	}
	if attackerHit {
		t.Error("Push() sent the blob to the cross-host Location")
	}
	if want := []string{"POST?digest"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests = %v, want %v", reg.requests, want)
	}
}

func TestRepository_Push_SingleRequest_SessionFallbackSchemeDowngrade(t *testing.T) {
	blob, desc := singlePostTestBlob()
	reg := &singlePostRegistry{t: t, singlePOSTStatus: http.StatusAccepted}
	srv := httptest.NewTLSServer(reg)
	defer srv.Close()
	uri, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("invalid test https server: %v", err)
	}
	reg.location = "http://" + uri.Host + singlePostSessionPath

	repo := newSinglePostTestRepo(t, srv, desc.Size)
	repo.Registry.PlainHTTP = false
	repo.Registry.Client = &auth.Client{Client: srv.Client()}

	err = repo.Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil || !strings.Contains(err.Error(), "downgrades scheme") {
		t.Errorf("Push() error = %v, want a scheme downgrade error", err)
	}
	if want := []string{"POST?digest"}; !slices.Equal(reg.requests, want) {
		t.Errorf("requests = %v, want %v", reg.requests, want)
	}
}

func TestRepository_Push_SingleRequest_StatusFallback(t *testing.T) {
	blob, desc := singlePostTestBlob()
	for _, tt := range []struct {
		status  int
		want    []string
		wantErr bool
		// remember is whether the status says the feature is missing, so the
		// next push skips the single POST
		remember bool
	}{
		{status: http.StatusBadRequest, want: []string{"POST?digest", "POST", "PUT"}},
		{status: http.StatusNotFound, want: []string{"POST?digest", "POST", "PUT"}},
		{status: http.StatusMethodNotAllowed, want: []string{"POST?digest", "POST", "PUT"}, remember: true},
		{status: http.StatusRequestEntityTooLarge, want: []string{"POST?digest", "POST", "PUT"}},
		{status: http.StatusUnsupportedMediaType, want: []string{"POST?digest", "POST", "PUT"}, remember: true},
		{status: http.StatusNotImplemented, want: []string{"POST?digest", "POST", "PUT"}, remember: true},
		// a credential problem is not a missing feature, so it is not retried
		// with another upload
		{status: http.StatusUnauthorized, want: []string{"POST?digest"}, wantErr: true},
		{status: http.StatusForbidden, want: []string{"POST?digest"}, wantErr: true},
		// the retry transport handles rate limiting; another upload would only
		// add to it
		{status: http.StatusTooManyRequests, want: []string{"POST?digest"}, wantErr: true},
		{status: http.StatusInternalServerError, want: []string{"POST?digest"}, wantErr: true},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			reg := &singlePostRegistry{t: t, singlePOSTStatus: tt.status}
			srv := httptest.NewServer(reg)
			defer srv.Close()
			repo := newSinglePostTestRepo(t, srv, desc.Size)

			err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(blob)})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Push() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(reg.requests, tt.want) {
				t.Errorf("requests = %v, want %v", reg.requests, tt.want)
			}
			if !tt.wantErr && !bytes.Equal(reg.uploaded, blob) {
				t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
			}

			// Only a status that says the feature is missing makes the next push
			// skip the single POST. Any other status is about that one request.
			reg.singlePOSTStatus = 0
			reg.requests = nil
			if err := repo.Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
				t.Fatalf("second Push() error = %v", err)
			}
			want := []string{"POST?digest"}
			if tt.remember {
				want = []string{"POST", "PUT"}
			}
			if !slices.Equal(reg.requests, want) {
				t.Errorf("second push requests = %v, want %v", reg.requests, want)
			}
		})
	}
}

func TestRepository_Push_SingleRequest_RemembersUnsupported(t *testing.T) {
	blob, desc := singlePostTestBlob()
	other := []byte("another small blob")
	otherDesc := ocispec.Descriptor{MediaType: "test", Digest: digest.FromBytes(other), Size: int64(len(other))}
	for _, tt := range []struct {
		status    int
		wantFirst []string
	}{
		{status: http.StatusAccepted, wantFirst: []string{"POST?digest", "PUT"}},
		{status: http.StatusMethodNotAllowed, wantFirst: []string{"POST?digest", "POST", "PUT"}},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			reg := &singlePostRegistry{t: t, singlePOSTStatus: tt.status}
			srv := httptest.NewServer(reg)
			defer srv.Close()
			repo := newSinglePostTestRepo(t, srv, otherDesc.Size)

			if err := repo.Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if !slices.Equal(reg.requests, tt.wantFirst) {
				t.Errorf("first push requests = %v, want %v", reg.requests, tt.wantFirst)
			}

			reg.requests = nil
			if err := repo.Push(context.Background(), otherDesc, bytes.NewReader(other)); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if want := []string{"POST", "PUT"}; !slices.Equal(reg.requests, want) {
				t.Errorf("next push requests = %v, want %v", reg.requests, want)
			}
			if !bytes.Equal(reg.uploaded, other) {
				t.Errorf("uploaded = %q, want %q", reg.uploaded, other)
			}

			// a clone starts without what the original learned
			reg.requests = nil
			if err := repo.clone().Push(context.Background(), otherDesc, bytes.NewReader(other)); err != nil {
				t.Fatalf("clone Push() error = %v", err)
			}
			if !slices.Equal(reg.requests, tt.wantFirst) {
				t.Errorf("clone push requests = %v, want %v", reg.requests, tt.wantFirst)
			}
		})
	}
}

func TestRepository_Push_SingleRequest_ConcurrentUnsupported(t *testing.T) {
	reg := &singlePostRegistry{t: t, singlePOSTStatus: http.StatusAccepted}
	srv := httptest.NewServer(reg)
	defer srv.Close()
	repo := newSinglePostTestRepo(t, srv, 1024)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			blob := []byte{byte(i)}
			desc := ocispec.Descriptor{MediaType: "test", Digest: digest.FromBytes(blob), Size: 1}
			errs[i] = repo.Push(context.Background(), desc, bytes.NewReader(blob))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Push(%d) error = %v", i, err)
		}
	}
	if got := len(reg.postBodies); got < 1 || got > n {
		t.Errorf("single POST count = %d, want 1 to %d", got, n)
	}
}

func TestRepository_Push_SingleRequest_SizeMismatch(t *testing.T) {
	blob, desc := singlePostTestBlob()
	for _, tt := range []struct {
		name    string
		content []byte
	}{
		{name: "shorter", content: blob[:len(blob)-1]},
		{name: "longer", content: append(slices.Clone(blob), '!')},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &singlePostRegistry{t: t}
			srv := httptest.NewServer(reg)
			defer srv.Close()
			repo := newSinglePostTestRepo(t, srv, desc.Size*2)

			err := repo.Push(context.Background(), desc, nonSeekableReader{bytes.NewReader(tt.content)})
			if err == nil || !strings.Contains(err.Error(), "mismatch content length") {
				t.Errorf("Push() error = %v, want a content length mismatch", err)
			}
			if len(reg.requests) != 0 {
				t.Errorf("requests = %v, want none", reg.requests)
			}
		})
	}
}

func TestRegistry_Repository_Push_SingleRequestDefault(t *testing.T) {
	blob, desc := singlePostTestBlob()
	for _, tt := range []struct {
		name             string
		defaultThreshold int64
		override         int64
		want             []string
	}{
		{name: "registry default", defaultThreshold: desc.Size, want: []string{"POST?digest"}},
		{name: "repository override", defaultThreshold: desc.Size, override: desc.Size - 1, want: []string{"POST", "PUT"}},
		{name: "off by default", want: []string{"POST", "PUT"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &singlePostRegistry{t: t}
			srv := httptest.NewServer(reg)
			defer srv.Close()

			uri, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatalf("invalid test server URL: %v", err)
			}
			registry, err := NewRegistry(uri.Host)
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}
			registry.PlainHTTP = true
			registry.Client = &auth.Client{Client: &http.Client{}}
			registry.SingleRequestPushThreshold = tt.defaultThreshold
			target, err := registry.Repository(context.Background(), "test")
			if err != nil {
				t.Fatalf("Registry.Repository() error = %v", err)
			}
			repo, ok := target.(*Repository)
			if !ok {
				t.Fatalf("repository type = %T, want *Repository", target)
			}
			repo.SingleRequestPushThreshold = tt.override

			if err := repo.Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if !slices.Equal(reg.requests, tt.want) {
				t.Errorf("requests = %v, want %v", reg.requests, tt.want)
			}
		})
	}
}

func TestRepository_SingleRequestPushThreshold(t *testing.T) {
	for _, tt := range []struct {
		name   string
		child  int64
		parent int64
		want   int64
	}{
		{name: "child overrides parent", child: 4, parent: 8, want: 4},
		{name: "falls back to parent", child: 0, parent: 8, want: 8},
		{name: "zero when both are zero", child: 0, parent: 0, want: 0},
		{name: "negative child falls back to parent", child: -1, parent: 8, want: 8},
		{name: "negative child and zero parent", child: -1, parent: 0, want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{
				SingleRequestPushThreshold: tt.child,
				Registry: &Registry{
					SingleRequestPushThreshold: tt.parent,
				},
			}
			if got := repo.singleRequestPushThreshold(); got != tt.want {
				t.Errorf("singleRequestPushThreshold() = %d, want %d", got, tt.want)
			}
		})
	}
}
