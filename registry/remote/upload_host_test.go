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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
)

const (
	uploadHostSessionPath  = "/v2/test/blobs/uploads/session"
	uploadHostRegistryCred = "Bearer registry-token"
)

// uploadHost is an upload host that is not the registry and has its own token
// service, as when a registry behind a proxy hands out upload locations on
// another host. Its tokens list the scopes they were issued for, and every
// request without a token for the scope it needs is challenged.
type uploadHost struct {
	t *testing.T
	// repository is the repository its challenges name.
	repository string
	// getActions are the actions it challenges an upload status GET for.
	getActions string
	// getStatus, when non-zero, answers an authorized upload status GET.
	getStatus int
	// rejectStatus, when non-zero, answers a token without the needed scope
	// instead of a challenge.
	rejectStatus int

	server *httptest.Server
	tokens *httptest.Server

	mu       sync.Mutex
	requests []string // "<method> <authorization>" of each request
	uploaded []byte
}

func newUploadHost(t *testing.T, repository, getActions string, getStatus int) *uploadHost {
	u := &uploadHost{t: t, repository: repository, getActions: getActions, getStatus: getStatus}
	u.tokens = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.Join(r.URL.Query()["scope"], "|")
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, token); err != nil {
			t.Errorf("failed to write token response: %v", err)
		}
	}))
	u.server = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(func() {
		u.server.Close()
		u.tokens.Close()
	})
	return u
}

// granted reports whether the bearer token in authorization grants action on
// the upload host's repository.
func (u *uploadHost) granted(authorization, action string) bool {
	token, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok {
		return false
	}
	for scope := range strings.SplitSeq(token, "|") {
		name, actions, ok := strings.Cut(strings.TrimPrefix(scope, "repository:"), ":")
		if ok && name == u.repository && slices.Contains(strings.Split(actions, ","), action) {
			return true
		}
	}
	return false
}

func (u *uploadHost) serve(w http.ResponseWriter, r *http.Request) {
	authorization := r.Header.Get("Authorization")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		u.t.Errorf("failed to read %s body: %v", r.Method, err)
	}
	u.mu.Lock()
	u.requests = append(u.requests, r.Method+" "+authorization)
	u.mu.Unlock()
	if authorization == uploadHostRegistryCred {
		u.t.Errorf("%s %s carried the registry's credential", r.Method, r.URL)
	}
	// the auth client's own header, which must be sent once
	if got := r.Header.Values("X-Test"); len(got) != 1 {
		u.t.Errorf("%s %s X-Test = %q, want one", r.Method, r.URL, got)
	}
	if r.URL.Path != uploadHostSessionPath {
		u.t.Errorf("unexpected access: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	challenge := func(actions string) {
		if u.rejectStatus != 0 && authorization != "" {
			w.WriteHeader(u.rejectStatus)
			return
		}
		w.Header().Set("Www-Authenticate", fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", u.tokens.URL, "uploads", "repository:"+u.repository+":"+actions))
		w.WriteHeader(http.StatusUnauthorized)
	}
	switch r.Method {
	case http.MethodGet:
		if !u.granted(authorization, "pull") {
			challenge(u.getActions)
			return
		}
		if u.getStatus != 0 {
			w.WriteHeader(u.getStatus)
			return
		}
		w.Header().Set("Location", uploadHostSessionPath)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch, http.MethodPut:
		if !u.granted(authorization, "push") {
			challenge("pull,push")
			return
		}
		u.mu.Lock()
		u.uploaded = append(u.uploaded, body...)
		u.mu.Unlock()
		if r.Method == http.MethodPatch {
			w.Header().Set("Location", uploadHostSessionPath)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Docker-Content-Digest", r.URL.Query().Get("digest"))
		w.WriteHeader(http.StatusCreated)
	default:
		u.t.Errorf("unexpected access: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// count returns how many requests with method the upload host received.
func (u *uploadHost) count(method string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, request := range u.requests {
		if strings.HasPrefix(request, method+" ") {
			n++
		}
	}
	return n
}

// newUploadHostRepository returns a repository on a registry that opens every
// upload session at location. The registry's own credential is set on the POST.
func newUploadHostRepository(t *testing.T, location string) *Repository {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					req.Header.Set("Authorization", uploadHostRegistryCred)
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
		Cache:  auth.NewCache(),
		Header: http.Header{"X-Test": {"1"}},
	}
	return repo
}

// streamed hides every interface of r but io.Reader, so the push cannot replay it.
type streamed struct{ io.Reader }

func Test_BlobStore_Push_UploadHostToken(t *testing.T) {
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	tests := []struct {
		name         string
		repository   string
		getActions   string
		getStatus    int
		rejectStatus int
		content      io.Reader
		wantPUTs     int
	}{
		{
			// The status GET is challenged for pull only, like distribution does;
			// the push scope comes from the hint.
			name:       "streamed body",
			repository: "test",
			getActions: "pull",
			content:    streamed{bytes.NewReader(blob)},
			wantPUTs:   1,
		},
		{
			// The challenge names the repository differently from the upload
			// location, so the PUT would miss a token cached under the hint.
			name:       "challenge names another repository",
			repository: "proxy/test",
			getActions: "pull,push",
			content:    streamed{bytes.NewReader(blob)},
			wantPUTs:   1,
		},
		{
			name:       "status GET not found",
			repository: "test",
			getActions: "pull",
			getStatus:  http.StatusNotFound,
			content:    bytes.NewReader(blob),
			wantPUTs:   2,
		},
		{
			name:       "status GET not allowed",
			repository: "test",
			getActions: "pull",
			getStatus:  http.StatusMethodNotAllowed,
			content:    bytes.NewReader(blob),
			wantPUTs:   2,
		},
		{
			// The token from the status GET cannot push, so the PUT falls back to
			// answering its own challenge.
			name:       "status GET token rejected",
			repository: "proxy/test",
			getActions: "pull",
			content:    bytes.NewReader(blob),
			wantPUTs:   3,
		},
		{
			name:         "status GET token forbidden",
			repository:   "proxy/test",
			getActions:   "pull",
			rejectStatus: http.StatusForbidden,
			content:      bytes.NewReader(blob),
			wantPUTs:     3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := newUploadHost(t, tt.repository, tt.getActions, tt.getStatus)
			host.rejectStatus = tt.rejectStatus
			repo := newUploadHostRepository(t, host.server.URL+uploadHostSessionPath)

			if err := repo.Blobs().Push(context.Background(), desc, tt.content); err != nil {
				t.Fatalf("Blobs.Push() error = %v", err)
			}
			if !bytes.Equal(host.uploaded, blob) {
				t.Errorf("uploaded = %q, want %q", host.uploaded, blob)
			}
			if got := host.count(http.MethodPut); got != tt.wantPUTs {
				t.Errorf("PUT count = %d, want %d (requests %q)", got, tt.wantPUTs, host.requests)
			}
		})
	}
}

func TestRepository_Push_ChunkedUploadHostToken(t *testing.T) {
	blob := []byte("hello") // 3 chunks
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	tests := []struct {
		name         string
		repository   string
		getStatus    int
		rejectStatus int
		wantPATCHes  int
	}{
		{name: "token from status GET", repository: "test", wantPATCHes: 3},
		// Only the first PATCH is challenged; its token is reused after it.
		{name: "status GET not found", repository: "test", getStatus: http.StatusNotFound, wantPATCHes: 4},
		// The first PATCH is refused with the status GET's token, sent again
		// without it and challenged, and its new token is reused after it.
		{name: "status GET token forbidden", repository: "proxy/test", rejectStatus: http.StatusForbidden, wantPATCHes: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := newUploadHost(t, tt.repository, "pull", tt.getStatus)
			host.rejectStatus = tt.rejectStatus
			repo := newUploadHostRepository(t, host.server.URL+uploadHostSessionPath)
			repo.MaxChunkSize = 2

			if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if !bytes.Equal(host.uploaded, blob) {
				t.Errorf("uploaded = %q, want %q", host.uploaded, blob)
			}
			if got := host.count(http.MethodPatch); got != tt.wantPATCHes {
				t.Errorf("PATCH count = %d, want %d (requests %q)", got, tt.wantPATCHes, host.requests)
			}
			if got := host.count(http.MethodPut); got != 1 {
				t.Errorf("PUT count = %d, want 1 (requests %q)", got, host.requests)
			}
		})
	}
}

// TestRepository_Push_ChunkedUploadHostTokenNotForwarded checks that the upload
// host's token stays on the upload host when the session moves to a third host.
func TestRepository_Push_ChunkedUploadHostTokenNotForwarded(t *testing.T) {
	third := &chunkedUploadRegistry{t: t}
	thirdSrv := httptest.NewServer(third.handler())
	defer thirdSrv.Close()
	uploads := &chunkedUploadRegistry{t: t, patchLocationOverride: thirdSrv.URL + uploadHostSessionPath}
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		uploads.handler()(w, r)
	}))
	defer uploadSrv.Close()
	uploadURL, err := url.Parse(uploadSrv.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo := newUploadHostRepository(t, uploadSrv.URL+uploadHostSessionPath)
	repo.MaxChunkSize = 2
	// Stand in for the upload host's token service.
	repo.Registry.Client.(*auth.Client).Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost:
			req.Header.Set("Authorization", uploadHostRegistryCred)
		case req.URL.Host == uploadURL.Host && req.Header.Get("Authorization") == "":
			req.Header.Set("Authorization", "Bearer upload-token")
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := append(uploads.uploaded, third.uploaded...); !bytes.Equal(got, blob) {
		t.Errorf("uploaded = %q, want %q", got, blob)
	}
	if len(third.methodSequence()) == 0 {
		t.Fatal("third host received no requests")
	}
	thirdSeq := third.methodSequence()
	for i, authorization := range third.authHeaders() {
		if authorization != "" {
			t.Errorf("third host request %d (%s) authorization = %q, want none", i, thirdSeq[i], authorization)
		}
	}
}

// uploadHostClient answers every request with status, as if its final request
// went to final carrying authorization.
type uploadHostClient struct {
	status        int
	final         string
	authorization string
	// noRequest leaves the response's Request unset, as some clients do.
	noRequest bool
}

func (c uploadHostClient) Do(req *http.Request) (*http.Response, error) {
	final := req.Clone(req.Context())
	if c.final != "" {
		u, err := url.Parse(c.final)
		if err != nil {
			return nil, err
		}
		final.URL = u
	}
	final.Header.Set("Authorization", c.authorization)
	if c.noRequest {
		final = nil
	}
	return &http.Response{StatusCode: c.status, Body: http.NoBody, Request: final}, nil
}

func Test_blobStore_uploadHostAuthorization(t *testing.T) {
	const location = "http://uploads.example" + uploadHostSessionPath
	tests := []struct {
		name   string
		client uploadHostClient
		want   string
	}{
		{name: "authorized", client: uploadHostClient{status: http.StatusNoContent, authorization: "Bearer upload-token"}, want: "Bearer upload-token"},
		{name: "not authorized", client: uploadHostClient{status: http.StatusUnauthorized, authorization: "Bearer upload-token"}},
		// A token obtained at the HTTPS endpoint must not go out over HTTP.
		{name: "upgraded to https", client: uploadHostClient{status: http.StatusNoContent, final: "https://uploads.example" + uploadHostSessionPath, authorization: "Bearer upload-token"}},
		{name: "no request on the response", client: uploadHostClient{status: http.StatusNoContent, authorization: "Bearer upload-token", noRequest: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := NewRepository("registry.example/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.Client = tt.client
			u, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			s := &blobStore{repo: repo}
			if got := s.uploadHostAuthorization(context.Background(), u); got != tt.want {
				t.Errorf("uploadHostAuthorization() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRepository_Push_ChunkedRegistryForbiddenNotResent checks that a 403 to
// the registry's cached header fails the upload rather than sending the chunk
// again: the auth client would only send the same token.
func TestRepository_Push_ChunkedRegistryForbiddenNotResent(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, rejectPATCHStatus: http.StatusForbidden}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	repo := newChunkedTestRepo(t, srv, 2)
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					req.Header.Set("Authorization", uploadHostRegistryCred)
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	if err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob)); err == nil {
		t.Fatal("Push() error = nil, want the 403")
	}
	if got := reg.patchCount(); got != 1 {
		t.Errorf("PATCH count = %d, want 1", got)
	}
}

// Test_blobStore_sendChunkRequest_upgradedAuthorization checks that a token
// the auth client got after an upgrade to HTTPS is not kept for the HTTP host.
func Test_blobStore_sendChunkRequest_upgradedAuthorization(t *testing.T) {
	repo, err := NewRepository("registry.example/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.Client = uploadHostClient{
		status:        http.StatusAccepted,
		final:         "https://uploads.example" + uploadHostSessionPath,
		authorization: "Bearer https-token",
	}
	s := &blobStore{repo: repo}
	origin, err := url.Parse("https://registry.example/v2/test/blobs/uploads/")
	if err != nil {
		t.Fatal(err)
	}
	up := &chunkedUpload{origin: origin}
	newReq := func() (*http.Request, error) {
		return http.NewRequest(http.MethodPatch, "http://uploads.example"+uploadHostSessionPath, http.NoBody)
	}
	req, err := newReq()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.sendChunkRequest(up, req, newReq)
	if err != nil {
		t.Fatalf("sendChunkRequest() error = %v", err)
	}
	resp.Body.Close()
	if up.uploadAuth != "" || up.auth != "" {
		t.Errorf("cached authorization = %q (upload host), %q (registry), want none", up.uploadAuth, up.auth)
	}
}
