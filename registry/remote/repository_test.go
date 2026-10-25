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
	"crypto/sha1"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
	"golang.org/x/sync/errgroup"
)

type testIOStruct struct {
	isTag                   bool
	clientSuppliedReference string
	serverCalculatedDigest  digest.Digest // for non-HEAD (body-containing) requests only
	errExpectedOnHEAD       bool
	errExpectedOnGET        bool
}

const theAmazingBanClan = "Ban Gu, Ban Chao, Ban Zhao"
const theAmazingBanDigest = "b526a4f2be963a2f9b0990c001255669eab8a254ab1a6e3f84f1820212ac7078"

type badReader struct{}

func (r *badReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func (r *badReader) Close() error {
	return nil
}

// The following truth table aims to cover the expected GET/HEAD request outcome
// for all possible permutations of the client/server "containing a digest", for
// both Manifests and Blobs.  Where the results between the two differ, the index
// of the first column has an exclamation mark.
//
// The client is said to "contain a digest" if the user-supplied reference string
// is of the form that contains a digest rather than a tag.  The server, on the
// other hand, is said to "contain a digest" if the server responded with the
// special header `Docker-Content-Digest`.
//
// In this table, anything denoted with an asterisk indicates that the true
// response should actually be the opposite of what's expected; for example,
// `*PASS` means we will get a `PASS`, even though the true answer would be its
// diametric opposite--a `FAIL`. This may seem odd, and deserves an explanation.
// This function has blind-spots, and while it can expend power to gain sight,
// i.e., perform the expensive validation, we chose not to.  The reason is two-
// fold: a) we "know" that even if we say "!PASS", it will eventually fail later
// when checks are performed, and with that assumption, we have the luxury for
// the second point, which is b) performance.
//
//	 _______________________________________________________________________________________________________________
//	| ID | CLIENT          | SERVER           | Manifest.GET          | Blob.GET  | Manifest.HEAD       | Blob.HEAD |
//	|----+-----------------+------------------+-----------------------+-----------+---------------------+-----------+
//	| 1  | tag             | missing          | CALCULATE,PASS        | n/a       | FAIL                | n/a       |
//	| 2  | tag             | presentCorrect   | TRUST,PASS            | n/a       | TRUST,PASS          | n/a       |
//	| 3  | tag             | presentIncorrect | TRUST,*PASS           | n/a       | TRUST,*PASS         | n/a       |
//	| 4  | correctDigest   | missing          | TRUST,PASS            | PASS      | TRUST,PASS          | PASS      |
//	| 5  | correctDigest   | presentCorrect   | TRUST,COMPARE,PASS    | PASS      | TRUST,COMPARE,PASS  | PASS      |
//	| 6  | correctDigest   | presentIncorrect | TRUST,COMPARE,FAIL    | FAIL      | TRUST,COMPARE,FAIL  | FAIL      |
//	 ---------------------------------------------------------------------------------------------------------------
func getTestIOStructMapForGetDescriptorClass() map[string]testIOStruct {
	correctDigest := fmt.Sprintf("sha256:%v", theAmazingBanDigest)
	incorrectDigest := fmt.Sprintf("sha256:%v", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	return map[string]testIOStruct{
		"1. Client:Tag & Server:DigestMissing": {
			isTag:             true,
			errExpectedOnHEAD: true,
		},
		"2. Client:Tag & Server:DigestValid": {
			isTag:                  true,
			serverCalculatedDigest: digest.Digest(correctDigest),
		},
		"3. Client:Tag & Server:DigestWrongButSyntacticallyValid": {
			isTag:                  true,
			serverCalculatedDigest: digest.Digest(incorrectDigest),
		},
		"4. Client:DigestValid & Server:DigestMissing": {
			clientSuppliedReference: correctDigest,
		},
		"5. Client:DigestValid & Server:DigestValid": {
			clientSuppliedReference: correctDigest,
			serverCalculatedDigest:  digest.Digest(correctDigest),
		},
		"6. Client:DigestValid & Server:DigestWrongButSyntacticallyValid": {
			clientSuppliedReference: correctDigest,
			serverCalculatedDigest:  digest.Digest(incorrectDigest),
			errExpectedOnHEAD:       true,
			errExpectedOnGET:        true,
		},
	}
}

func TestNewRepository(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		wantErr   error
	}{
		{
			name:      "valid reference",
			reference: "localhost:5000/hello-world",
			wantErr:   nil,
		},
		{
			name:      "invalid reference",
			reference: "invalid reference",
			wantErr:   errdef.ErrInvalidReference,
		},
		{
			name:      "reference with tag",
			reference: "localhost:5000/hello-world:v1",
			wantErr:   errdef.ErrInvalidReference,
		},
		{
			name:      "reference with digest",
			reference: "localhost:5000/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			wantErr:   errdef.ErrInvalidReference,
		},
		{
			name:      "empty reference",
			reference: "",
			wantErr:   errdef.ErrInvalidReference,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRepository(tt.reference)
			if err != nil {
				if errors.Is(err, tt.wantErr) {
					return
				}
				t.Fatalf("NewRepository() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.Reference().String() != tt.reference {
				t.Errorf("NewRepository() got = %v, want %v", got.Reference().String(), tt.reference)
			}
		})
	}
}

func TestRepository_Fetch(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case "/v2/test/manifests/" + indexDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	rc, err := repo.Fetch(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Repository.Fetch() error = %v", err)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Repository.Fetch() = %v, want %v", got, blob)
	}

	rc, err = repo.Fetch(ctx, indexDesc)
	if err != nil {
		t.Fatalf("Repository.Fetch() error = %v", err)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, index) {
		t.Errorf("Repository.Fetch() = %v, want %v", got, index)
	}
}

func TestRepository_Push(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var gotBlob []byte
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotBlob = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Repository.Push() error = %v", err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Errorf("Repository.Push() = %v, want %v", gotBlob, blob)
	}

	err = repo.Push(ctx, indexDesc, bytes.NewReader(index))
	if err != nil {
		t.Fatalf("Repository.Push() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.Push() = %v, want %v", gotIndex, index)
	}
}

func TestRepository_Mount(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	gotMount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, "POST"; got != want {
			t.Errorf("unexpected HTTP method; got %q want %q", got, want)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("invalid form in HTTP request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/v2/test2/blobs/uploads/":
			if got, want := r.Form.Get("mount"), blobDesc.Digest; digest.Digest(got) != want {
				t.Errorf("unexpected value for 'mount' parameter; got %q want %q", got, want)
			}
			if got, want := r.Form.Get("from"), "test"; got != want {
				t.Errorf("unexpected value for 'from' parameter; got %q want %q", got, want)
			}
			gotMount++
			w.Header().Set(headerDockerContentDigest, blobDesc.Digest.String())
			w.WriteHeader(201)
			return
		default:
			t.Errorf("unexpected URL for mount request %q", r.URL)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Mount(ctx, blobDesc, "test", nil)
	if err != nil {
		t.Fatalf("Repository.Push() error = %v", err)
	}
	if gotMount != 1 {
		t.Errorf("did not get expected mount request")
	}
}

// scopeRecordingClient records the scope hints that apply to each request it
// sends, as the auth client would resolve them.
type scopeRecordingClient struct {
	client Client
	scopes map[string][]auth.Scope
}

func (c *scopeRecordingClient) Do(req *http.Request) (*http.Response, error) {
	resource := properties.Resource{
		Registry: req.URL.Host,
		Path:     strings.SplitN(strings.TrimPrefix(req.URL.Path, "/v2/"), "/blobs/", 2)[0],
	}
	if c.scopes == nil {
		c.scopes = make(map[string][]auth.Scope)
	}
	c.scopes[resource.Path] = auth.GetScopesForResource(req.Context(), resource)
	return c.client.Do(req)
}

// TestRepository_Mount_ScopeHints checks that a cross-repository mount still
// asks for pull on the source repository. The hints are keyed by resource now,
// so the source scope has to be registered against the target repository, which
// is the repository the mount request is made to.
func TestRepository_Mount_ScopeHints(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/test2/blobs/uploads/" {
			t.Errorf("unexpected URL for mount request %q", r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set(headerDockerContentDigest, blobDesc.Digest.String())
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	recorder := &scopeRecordingClient{client: repo.client()}
	repo.Registry.Client = recorder

	if err := repo.Mount(context.Background(), blobDesc, "test", nil); err != nil {
		t.Fatalf("Repository.Mount() error = %v", err)
	}

	want := []auth.Scope{
		auth.ScopeRepository("test", auth.ActionPull),
		auth.ScopeRepository("test2", auth.ActionPull, auth.ActionPush),
	}
	if got := recorder.scopes["test2"]; !reflect.DeepEqual(got, want) {
		t.Errorf("scope hints on the mount request = %v, want %v", got, want)
	}
}

func TestRepository_Mount_Fallback(t *testing.T) {
	// This test checks the case where the server does not know
	// about the mount query parameters, so the call falls back to
	// the regular push flow. This test is thus very similar to TestPush,
	// except that it doesn't push a manifest because mounts aren't
	// documented to be supported for manifests.

	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var sequence string
	var gotBlob []byte
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test2/blobs/uploads/":
			w.Header().Set("Location", "/v2/test2/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			sequence += "post "
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/blobs/"+blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
			sequence += "get "
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test2/blobs/uploads/"+uuid:
			if got, want := r.Header.Get("Content-Type"), "application/octet-stream"; got != want {
				t.Errorf("unexpected content type; got %q want %q", got, want)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got, want := r.URL.Query().Get("digest"), blobDesc.Digest.String(); got != want {
				t.Errorf("unexpected content digest; got %q want %q", got, want)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("error reading body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			gotBlob = data
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			sequence += "put "
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	t.Run("getContent is nil", func(t *testing.T) {
		sequence = ""

		err = repo.Mount(ctx, blobDesc, "test", nil)
		if err != nil {
			t.Fatalf("Repository.Push() error = %v", err)
		}
		if !bytes.Equal(gotBlob, blob) {
			t.Errorf("Repository.Mount() = %v, want %v", gotBlob, blob)
		}
		if got, want := sequence, "post get put "; got != want {
			t.Errorf("unexpected request sequence; got %q want %q", got, want)
		}
	})

	t.Run("getContent is non nil", func(t *testing.T) {
		sequence = ""

		err = repo.Mount(ctx, blobDesc, "test", func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(blob)), nil
		})
		if err != nil {
			t.Fatalf("Repository.Push() error = %v", err)
		}
		if !bytes.Equal(gotBlob, blob) {
			t.Errorf("Repository.Mount() = %v, want %v", gotBlob, blob)
		}
		if got, want := sequence, "post put "; got != want {
			t.Errorf("unexpected request sequence; got %q want %q", got, want)
		}
	})
}

func TestRepository_Mount_Fallback_DigestHeaderMismatch(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test2/blobs/uploads/":
			w.Header().Set("Location", "/v2/test2/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test2/blobs/uploads/"+uuid:
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Docker-Content-Digest", digest.FromString("other").String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.Registry.Client = &auth.Client{Client: &http.Client{}}

	err = repo.Mount(context.Background(), blobDesc, "test", func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(blob)), nil
	})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch in Docker-Content-Digest") {
		t.Errorf("Mount() error = %v, want a Docker-Content-Digest mismatch", err)
	}
	if errors.Is(err, content.ErrMismatchedDigest) {
		t.Errorf("errors.Is(%v, content.ErrMismatchedDigest) = true, want false on push", err)
	}
}

func TestRepository_Mount_Error(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, "POST"; got != want {
			t.Errorf("unexpected HTTP method; got %q want %q", got, want)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("invalid form in HTTP request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/uploads/":
			w.WriteHeader(400)
			w.Write([]byte(`{ "errors": [ { "code": "NAME_UNKNOWN", "message": "some error" } ] }`))
		default:
			t.Errorf("unexpected URL for mount request %q", r.URL)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	err = repo.Mount(context.Background(), blobDesc, "foo", nil)
	if err == nil {
		t.Fatalf("expected error but got success instead")
	}
	var errResp *errcode.ErrorResponse
	if !errors.As(err, &errResp) {
		t.Fatalf("unexpected error type %#v", err)
	}
	if !reflect.DeepEqual(errResp.Errors, errcode.Errors{{
		Code:    "NAME_UNKNOWN",
		Message: "some error",
	}}) {
		t.Errorf("unexpected errors %#v", errResp.Errors)
	}
}

func TestRepository_Mount_Fallback_GetContent(t *testing.T) {
	// This test checks the case where the server does not know
	// about the mount query parameters, so the call falls back to
	// the regular push flow, but using the getContent function
	// parameter to get the content to push.

	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var sequence string
	var gotBlob []byte
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test2/blobs/uploads/":
			w.Header().Set("Location", "/v2/test2/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			sequence += "post "
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test2/blobs/uploads/"+uuid:
			if got, want := r.Header.Get("Content-Type"), "application/octet-stream"; got != want {
				t.Errorf("unexpected content type; got %q want %q", got, want)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got, want := r.URL.Query().Get("digest"), blobDesc.Digest.String(); got != want {
				t.Errorf("unexpected content digest; got %q want %q", got, want)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("error reading body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			gotBlob = data
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			sequence += "put "
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Mount(ctx, blobDesc, "test", func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(blob)), nil
	})
	if err != nil {
		t.Fatalf("Repository.Push() error = %v", err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Errorf("Repository.Mount() = %v, want %v", gotBlob, blob)
	}
	if got, want := sequence, "post put "; got != want {
		t.Errorf("unexpected request sequence; got %q want %q", got, want)
	}
}

func TestRepository_Mount_Fallback_GetContentError(t *testing.T) {
	// This test checks the case where the server does not know
	// about the mount query parameters, so the call falls back to
	// the regular push flow, but it's possible the caller wants to
	// avoid the pull/push pattern so returns an error from getContent
	// and checks it to find out that's happened.

	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var sequence string
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test2/blobs/uploads/":
			w.Header().Set("Location", "/v2/test2/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			sequence += "post "
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test2")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	testErr := errors.New("test error")
	err = repo.Mount(ctx, blobDesc, "test", func() (io.ReadCloser, error) {
		return nil, testErr
	})
	if err == nil {
		t.Fatalf("expected error but found no error")
	}
	if !errors.Is(err, testErr) {
		t.Fatalf("expected getContent error to be wrapped")
	}
	if got, want := sequence, "post "; got != want {
		t.Errorf("unexpected request sequence; got %q want %q", got, want)
	}
}

func TestRepository_Exists(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(blobDesc.Size)))
		case "/v2/test/manifests/" + indexDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(indexDesc.Size)))
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	exists, err := repo.Exists(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Repository.Exists() error = %v", err)
	}
	if !exists {
		t.Errorf("Repository.Exists() = %v, want %v", exists, true)
	}

	exists, err = repo.Exists(ctx, indexDesc)
	if err != nil {
		t.Fatalf("Repository.Exists() error = %v", err)
	}
	if !exists {
		t.Errorf("Repository.Exists() = %v, want %v", exists, true)
	}
}

func TestRepository_Delete(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}

	var blobDeleted bool
	var indexDeleted bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/blobs/"+blobDesc.Digest.String():
			blobDeleted = true
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Delete(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Repository.Delete() error = %v", err)
	}
	if !blobDeleted {
		t.Errorf("Repository.Delete() = %v, want %v", blobDeleted, true)
	}

	err = repo.Delete(ctx, indexDesc)
	if err != nil {
		t.Fatalf("Repository.Delete() error = %v", err)
	}
	if !indexDeleted {
		t.Errorf("Repository.Delete() = %v, want %v", indexDeleted, true)
	}
}

func TestRepository_Resolve(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + blobDesc.Digest.String():
			w.WriteHeader(http.StatusNotFound)
		case "/v2/test/manifests/" + indexDesc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(indexDesc.Size)))
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	_, err = repo.Resolve(ctx, blobDesc.Digest.String())
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Repository.Resolve() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	got, err := repo.Resolve(ctx, indexDesc.Digest.String())
	if err != nil {
		t.Fatalf("Repository.Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(got, indexDesc) {
		t.Errorf("Repository.Resolve() = %v, want %v", got, indexDesc)
	}

	got, err = repo.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Repository.Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(got, indexDesc) {
		t.Errorf("Repository.Resolve() = %v, want %v", got, indexDesc)
	}

	tagDigestRef := "whatever" + "@" + indexDesc.Digest.String()
	got, err = repo.Resolve(ctx, tagDigestRef)
	if err != nil {
		t.Fatalf("Repository.Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(got, indexDesc) {
		t.Errorf("Repository.Resolve() = %v, want %v", got, indexDesc)
	}

	fqdnRef := repoName + ":" + tagDigestRef
	got, err = repo.Resolve(ctx, fqdnRef)
	if err != nil {
		t.Fatalf("Repository.Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(got, indexDesc) {
		t.Errorf("Repository.Resolve() = %v, want %v", got, indexDesc)
	}
}

func TestRepository_Tag(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+blobDesc.Digest.String():
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodPut &&
			r.URL.Path == "/v2/test/manifests/"+ref || r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Tag(ctx, blobDesc, ref)
	if err == nil {
		t.Errorf("Repository.Tag() error = %v, wantErr %v", err, true)
	}

	err = repo.Tag(ctx, indexDesc, ref)
	if err != nil {
		t.Fatalf("Repository.Tag() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.Tag() = %v, want %v", gotIndex, index)
	}

	gotIndex = nil
	err = repo.Tag(ctx, indexDesc, indexDesc.Digest.String())
	if err != nil {
		t.Fatalf("Repository.Tag() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.Tag() = %v, want %v", gotIndex, index)
	}
}

func TestRepository_Untag(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ref := "foobar"
	refNotFound := "ghost"
	refError := "boom"

	var untagged bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+ref:
			untagged = true
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refNotFound:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refError:
			// registries that do not support tag deletion answer 405
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = repo.Untag(ctx, "")
	if !errors.Is(err, errdef.ErrInvalidReference) {
		t.Errorf("Repository.Untag() error = %v, wantErr %v", err, errdef.ErrInvalidReference)
	}

	err = repo.Untag(ctx, indexDesc.Digest.String())
	if !errors.Is(err, errdef.ErrInvalidReference) {
		t.Errorf("Repository.Untag() error = %v, wantErr %v", err, errdef.ErrInvalidReference)
	}

	err = repo.Untag(ctx, refNotFound)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Repository.Untag() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	err = repo.Untag(ctx, refError)
	if err == nil {
		t.Errorf("Repository.Untag() error = %v, wantErr %v", err, true)
	}

	err = repo.Untag(ctx, ref)
	if err != nil {
		t.Fatalf("Repository.Untag() error = %v", err)
	}
	if !untagged {
		t.Errorf("Repository.Untag() did not send DELETE request")
	}

	// a canceled context makes the underlying request fail
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = repo.Untag(canceledCtx, ref)
	if err == nil {
		t.Errorf("Repository.Untag() error = %v, wantErr %v", err, true)
	}
}

func TestRepository_PushReference(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+ref:
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()
	err = repo.PushReference(ctx, indexDesc, bytes.NewReader(index), ref)
	if err != nil {
		t.Fatalf("Repository.PushReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.PushReference() = %v, want %v", gotIndex, index)
	}
}

func TestRepository_FetchReference(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + blobDesc.Digest.String():
			w.WriteHeader(http.StatusNotFound)
		case "/v2/test/manifests/" + indexDesc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	// test with blob digest
	_, _, err = repo.FetchReference(ctx, blobDesc.Digest.String())
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Repository.FetchReference() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test with manifest digest
	gotDesc, rc, err := repo.FetchReference(ctx, indexDesc.Digest.String())
	if err != nil {
		t.Fatalf("Repository.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("Repository.FetchReference() = %v, want %v", gotDesc, indexDesc)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, index) {
		t.Errorf("Repository.FetchReference() = %v, want %v", got, index)
	}

	// test with manifest tag
	gotDesc, rc, err = repo.FetchReference(ctx, ref)
	if err != nil {
		t.Fatalf("Repository.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("Repository.FetchReference() = %v, want %v", gotDesc, indexDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, index) {
		t.Errorf("Repository.FetchReference() = %v, want %v", got, index)
	}

	// test with manifest tag@digest
	tagDigestRef := "whatever" + "@" + indexDesc.Digest.String()
	gotDesc, rc, err = repo.FetchReference(ctx, tagDigestRef)
	if err != nil {
		t.Fatalf("Repository.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("Repository.FetchReference() = %v, want %v", gotDesc, indexDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, index) {
		t.Errorf("Repository.FetchReference() = %v, want %v", got, index)
	}

	// test with manifest FQDN
	fqdnRef := repoName + ":" + tagDigestRef
	gotDesc, rc, err = repo.FetchReference(ctx, fqdnRef)
	if err != nil {
		t.Fatalf("Repository.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("Repository.FetchReference() = %v, want %v", gotDesc, indexDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, index) {
		t.Errorf("Repository.FetchReference() = %v, want %v", got, index)
	}
}

func TestRepository_ListPaginationSettings(t *testing.T) {
	registry := &Registry{
		TagListPageSize:      100,
		ReferrerListPageSize: 200,
		TagListMaxPages:      10,
		ReferrerListMaxPages: 20,
	}

	tests := []struct {
		name string
		repo *Repository
		want int
		got  func(*Repository) int
	}{
		{
			name: "tag list page size uses repository override",
			repo: &Repository{Registry: registry, TagListPageSize: 5},
			want: 5,
			got:  (*Repository).tagListPageSize,
		},
		{
			name: "tag list page size inherits registry default",
			repo: &Repository{Registry: registry},
			want: 100,
			got:  (*Repository).tagListPageSize,
		},
		{
			name: "referrer list page size uses repository override",
			repo: &Repository{Registry: registry, ReferrerListPageSize: 6},
			want: 6,
			got:  (*Repository).referrerListPageSize,
		},
		{
			name: "referrer list page size inherits registry default",
			repo: &Repository{Registry: registry},
			want: 200,
			got:  (*Repository).referrerListPageSize,
		},
		{
			name: "tag list max pages uses repository override",
			repo: &Repository{Registry: registry, TagListMaxPages: 3},
			want: 3,
			got:  (*Repository).tagListMaxPages,
		},
		{
			name: "tag list max pages inherits registry default",
			repo: &Repository{Registry: registry},
			want: 10,
			got:  (*Repository).tagListMaxPages,
		},
		{
			name: "referrer list max pages uses repository override",
			repo: &Repository{Registry: registry, ReferrerListMaxPages: 4},
			want: 4,
			got:  (*Repository).referrerListMaxPages,
		},
		{
			name: "referrer list max pages inherits registry default",
			repo: &Repository{Registry: registry},
			want: 20,
			got:  (*Repository).referrerListMaxPages,
		},
		{
			name: "negative tag list page size inherits registry default",
			repo: &Repository{Registry: registry, TagListPageSize: -5},
			want: 100,
			got:  (*Repository).tagListPageSize,
		},
		{
			name: "negative referrer list page size inherits registry default",
			repo: &Repository{Registry: registry, ReferrerListPageSize: -6},
			want: 200,
			got:  (*Repository).referrerListPageSize,
		},
		{
			name: "negative tag list max pages inherits registry default",
			repo: &Repository{Registry: registry, TagListMaxPages: -3},
			want: 10,
			got:  (*Repository).tagListMaxPages,
		},
		{
			name: "negative referrer list max pages inherits registry default",
			repo: &Repository{Registry: registry, ReferrerListMaxPages: -4},
			want: 20,
			got:  (*Repository).referrerListMaxPages,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(tt.repo); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRepository_ManifestMediaTypesInheritsRegistry(t *testing.T) {
	registry := &Registry{
		ManifestMediaTypes: []string{"application/vnd.oci.image.manifest.v1+json"},
	}
	repo := &Repository{Registry: registry}

	got := repo.manifestMediaTypes()
	if !slices.Equal(got, registry.ManifestMediaTypes) {
		t.Errorf("manifestMediaTypes() = %v, want %v",
			got, registry.ManifestMediaTypes)
	}
}

func TestRepository_SkipReferrersGC(t *testing.T) {
	tests := []struct {
		name     string
		repo     bool
		registry bool
		want     bool
	}{
		{
			name:     "repository overrides registry",
			repo:     true,
			registry: false,
			want:     true,
		},
		{
			name:     "repository inherits registry",
			repo:     false,
			registry: true,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{
				Registry: &Registry{
					SkipReferrersGC: tt.registry,
				},
				SkipReferrersGC: tt.repo,
			}

			if got := repo.skipReferrersGC(); got != tt.want {
				t.Errorf("skipReferrersGC() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestRepository_Tags(t *testing.T) {
	tagSet := [][]string{
		{"the", "quick", "brown", "fox"},
		{"jumps", "over", "the", "lazy"},
		{"dog"},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/test/tags/list" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 4 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var tags []string
		switch q.Get("test") {
		case "foo":
			tags = tagSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/test/tags/list?n=4&test=bar>; rel="next"`, ts.URL))
		case "bar":
			tags = tagSet[2]
		default:
			tags = tagSet[0]
			w.Header().Set("Link", `</v2/test/tags/list?n=4&test=foo>; rel="next"`)
		}
		result := struct {
			Tags []string `json:"tags"`
		}{
			Tags: tags,
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.TagListPageSize = 4

	ctx := context.Background()
	index := 0
	if err := repo.Tags(ctx, "", func(got []string) error {
		if index > 2 {
			t.Fatalf("out of index bound: %d", index)
		}
		tags := tagSet[index]
		index++
		if !reflect.DeepEqual(got, tags) {
			t.Errorf("Repository.Tags() = %v, want %v", got, tags)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Tags() error = %v", err)
	}
}

func TestRepository_Tags_MaxPages(t *testing.T) {
	tagSet := [][]string{
		{"the", "quick", "brown", "fox"},
		{"jumps", "over", "the", "lazy"},
		{"dog"},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/test/tags/list" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		var tags []string
		switch q.Get("test") {
		case "foo":
			tags = tagSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/test/tags/list?test=bar>; rel="next"`, ts.URL))
		case "bar":
			tags = tagSet[2]
		default:
			tags = tagSet[0]
			w.Header().Set("Link", `</v2/test/tags/list?test=foo>; rel="next"`)
		}
		result := struct {
			Tags []string `json:"tags"`
		}{Tags: tags}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.TagListMaxPages = 2

	ctx := context.Background()
	err = repo.Tags(ctx, "", func(got []string) error { return nil })
	if !errors.Is(err, errdef.ErrTooManyPages) {
		t.Errorf("Repository.Tags() error = %v, want %v", err, errdef.ErrTooManyPages)
	}
}

func TestRepository_Predecessors(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]ocispec.Descriptor{
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	got, err := repo.Predecessors(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Repository.Predecessors() error = %v", err)
	}
	var want []ocispec.Descriptor
	for _, referrers := range referrerSet {
		want = append(want, referrers...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repository.Predecessors() = %v, want %v", got, want)
	}
}

func TestRepository_Referrers(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]ocispec.Descriptor{
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		if r.Method != http.MethodGet || r.URL.Path != path {
			referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
			if r.URL.Path != "/v2/test/manifests/"+referrersTag {
				t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()

	// test auto detect
	// remote server supports Referrers, should be no error
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if index >= len(referrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := referrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test force attempt Referrers
	// remote server supports Referrers, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	index = 0
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if index >= len(referrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := referrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test force attempt tag schema
	// request tag schema but got 404, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_MaxPages(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]ocispec.Descriptor{
		{{MediaType: spec.MediaTypeArtifactManifest, Size: 1, Digest: digest.FromString("1"), ArtifactType: "application/vnd.test"}},
		{{MediaType: spec.MediaTypeArtifactManifest, Size: 2, Digest: digest.FromString("2"), ArtifactType: "application/vnd.test"}},
		{{MediaType: spec.MediaTypeArtifactManifest, Size: 3, Digest: digest.FromString("3"), ArtifactType: "application/vnd.test"}},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		if r.Method != http.MethodGet || r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListMaxPages = 2
	repo.SetReferrersCapability(true)

	ctx := context.Background()
	err = repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error { return nil })
	if !errors.Is(err, errdef.ErrTooManyPages) {
		t.Errorf("Repository.Referrers() error = %v, want %v", err, errdef.ErrTooManyPages)
	}
}

func TestRepository_Referrers_TagSchemaFallback(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	referrers := []ocispec.Descriptor{
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         2,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("5"),
			ArtifactType: "application/vnd.test",
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		path := "/v2/test/manifests/" + referrersTag
		if r.Method != http.MethodGet || r.URL.Path != path {
			if r.URL.Path != "/v2/test/referrers/"+manifestDesc.Digest.String() {
				t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test auto detect
	// remote server does not support Referrers, should fallback to tag schema
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test force attempt Referrers
	// remote server does not support Referrers, should return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test force attempt tag schema
	// should request tag schema
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_TagSchemaFallback_NotFound(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		if r.Method == http.MethodGet ||
			r.URL.Path == referrersUrl ||
			r.URL.Path == tagSchemaUrl {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test auto detect
	// tag schema referrers not found, should be no error
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test force attempt tag schema
	// tag schema referrers not found, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_TagSchemaFallback_NameUnknown(t *testing.T) {
	// Some registries answer the Referrers API with 404 NAME_UNKNOWN for an
	// existing repository. Referrers stored with the tag schema must still be
	// found, and the Referrers API capability must not be cached.
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrers := []ocispec.Descriptor{
		{
			MediaType:    ocispec.MediaTypeImageManifest,
			Digest:       "sha256:baf18f0d9f3f6d8bdfe2a9a9a2bd5ee2a1a8bd43ec5e2a4e1e5e83b6a8ab9c7d",
			Size:         3,
			ArtifactType: "application/vnd.test",
		},
	}
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: referrers,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal index: %v", err)
	}
	var referrersAPICalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		switch {
		case r.Method == http.MethodGet && r.URL.Path == referrersUrl:
			referrersAPICalls++
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{ "errors": [ { "code": "NAME_UNKNOWN", "message": "repository name not known to registry" } ] }`))
		case r.Method == http.MethodGet && r.URL.Path == tagSchemaUrl:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(indexJSON).String())
			w.Write(indexJSON)
		default:
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	for i := range 2 {
		var got []ocispec.Descriptor
		if err := repo.Referrers(ctx, manifestDesc, "", func(page []ocispec.Descriptor) error {
			got = append(got, page...)
			return nil
		}); err != nil {
			t.Fatalf("Repository.Referrers() call %d error = %v", i, err)
		}
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() call %d = %v, want %v", i, got, referrers)
		}
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
		}
	}
	if referrersAPICalls != 2 {
		t.Errorf("Referrers API calls = %d, want 2", referrersAPICalls)
	}
}

func TestRepository_Referrers_TagSchemaFallback_ContentType(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		if r.URL.Path == referrersUrl {
			w.Header().Set("Content-Type", "application/json") // not an OCI image index
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet ||
			r.URL.Path == tagSchemaUrl {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test auto detect
	// tag schema referrers not found, should be no error
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_ContentType(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrers := []ocispec.Descriptor{{
		MediaType:    ocispec.MediaTypeImageManifest,
		Size:         1,
		Digest:       digest.FromString("1"),
		ArtifactType: "application/vnd.test",
	}}
	tests := []struct {
		name        string
		contentType string
		isIndex     bool
	}{
		{"parameter", ocispec.MediaTypeImageIndex + "; charset=utf-8", true},
		{"upper case", "Application/Vnd.Oci.Image.Index.V1+Json", true},
		{"malformed parameter", ocispec.MediaTypeImageIndex + "; charset", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
				switch r.URL.Path {
				case "/v2/test/referrers/" + manifestDesc.Digest.String():
					result := ocispec.Index{
						Versioned: specs.Versioned{
							SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
						},
						MediaType: ocispec.MediaTypeImageIndex,
						Manifests: referrers,
					}
					w.Header().Set("Content-Type", tt.contentType)
					if err := json.NewEncoder(w).Encode(result); err != nil {
						t.Errorf("failed to write response: %v", err)
					}
				case "/v2/test/manifests/" + referrersTag:
					w.WriteHeader(http.StatusNotFound)
				default:
					t.Errorf("unexpected access: %s %q", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			// a response that is not an image index falls back to the tag schema,
			// which is not found, should be no error
			repo, err := NewRepository(uri.Host + "/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			var got []ocispec.Descriptor
			if err := repo.Referrers(context.Background(), manifestDesc, "", func(page []ocispec.Descriptor) error {
				got = append(got, page...)
				return nil
			}); err != nil {
				t.Errorf("Repository.Referrers() error = %v", err)
			}
			wantReferrers, wantState := referrers, properties.ReferrersAPISupported
			if !tt.isIndex {
				wantReferrers, wantState = nil, properties.ReferrersAPIUnsupported
			}
			if !reflect.DeepEqual(got, wantReferrers) {
				t.Errorf("Repository.Referrers() = %v, want %v", got, wantReferrers)
			}
			if state := repo.loadReferrersState(); state != wantState {
				t.Errorf("Repository.loadReferrersState() = %v, want %v", state, wantState)
			}
		})
	}
}

func TestRepository_Referrers_TagSchemaFallback_ValidateIndex(t *testing.T) {
	referrers := []ocispec.Descriptor{{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("child"),
		Size:      5,
	}}
	for _, tt := range []struct {
		name, mediaType string
		isSubject       bool
		wantReferrers   bool
	}{
		{"valid OCI index", ocispec.MediaTypeImageIndex, false, true},
		{"Docker manifest list", "application/vnd.docker.distribution.manifest.list.v2+json", false, false},
		{"Docker subject", "application/vnd.docker.distribution.manifest.list.v2+json", true, false},
		{"OCI subject", ocispec.MediaTypeImageIndex, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			index := ocispec.Index{
				Versioned: specs.Versioned{SchemaVersion: 2},
				MediaType: tt.mediaType,
				Manifests: referrers,
			}
			body, err := json.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
			indexDigest := digest.FromBytes(body)
			subject := ocispec.Descriptor{Digest: digest.FromString("subject")}
			if tt.isSubject {
				subject.Digest = indexDigest
			}
			referrersTag := strings.Replace(subject.Digest.String(), ":", "-", 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+subject.Digest.String():
					w.WriteHeader(http.StatusNotFound)
				case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
					w.Header().Set("Content-Type", tt.mediaType)
					w.Header().Set("Docker-Content-Digest", indexDigest.String())
					w.Write(body)
				default:
					t.Errorf("unexpected access: %s %q", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			repo, err := NewRepository(strings.TrimPrefix(ts.URL, "http://") + "/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.PlainHTTP = true
			var got []ocispec.Descriptor
			if err := repo.Referrers(context.Background(), subject, "", func(page []ocispec.Descriptor) error {
				got = append(got, page...)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var want []ocispec.Descriptor
			if tt.wantReferrers {
				want = referrers
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Repository.Referrers() = %v, want %v", got, want)
			}
		})
	}
}

func TestRepository_PushReference_TagSchemaFallback_ValidateIndex(t *testing.T) {
	child := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString("child"), Size: 5}
	for _, tt := range []struct {
		name, mediaType  string
		isSubject, valid bool
	}{
		{"valid OCI index", ocispec.MediaTypeImageIndex, false, true},
		{"Docker manifest list", "application/vnd.docker.distribution.manifest.list.v2+json", false, false},
		{"Docker subject", "application/vnd.docker.distribution.manifest.list.v2+json", true, false},
		{"OCI subject", ocispec.MediaTypeImageIndex, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: tt.mediaType, Manifests: []ocispec.Descriptor{child}})
			if err != nil {
				t.Fatal(err)
			}
			oldIndex := content.NewDescriptorFromBytes(tt.mediaType, body)
			subject := ocispec.Descriptor{Digest: digest.FromString("subject")}
			if tt.isSubject {
				subject = oldIndex
			}
			referrersTag := strings.Replace(subject.Digest.String(), ":", "-", 1)
			attachment, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Subject: &subject, Manifests: []ocispec.Descriptor{}})
			if err != nil {
				t.Fatal(err)
			}
			attachmentDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, attachment)
			var updates, deletes atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/attachment":
					w.Header().Set("Docker-Content-Digest", attachmentDesc.Digest.String())
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
					w.Header().Set("Content-Type", tt.mediaType)
					w.Header().Set("Docker-Content-Digest", oldIndex.Digest.String())
					w.Write(body)
				case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
					updated, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					var index ocispec.Index
					if err := json.Unmarshal(updated, &index); err != nil {
						t.Error(err)
					}
					want := []ocispec.Descriptor{attachmentDesc}
					if tt.valid {
						want = append([]ocispec.Descriptor{child}, want...)
					}
					if !reflect.DeepEqual(index.Manifests, want) {
						t.Errorf("published referrers = %v, want %v", index.Manifests, want)
					}
					updates.Add(1)
					w.Header().Set("Docker-Content-Digest", digest.FromBytes(updated).String())
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodDelete:
					deletes.Add(1)
					if !tt.valid || r.URL.Path != "/v2/test/manifests/"+oldIndex.Digest.String() {
						t.Errorf("unexpected deletion: %s", r.URL.Path)
					}
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Errorf("unexpected access: %s %q", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			repo, err := NewRepository(strings.TrimPrefix(ts.URL, "http://") + "/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.PlainHTTP = true
			repo.SetReferrersCapability(false)
			if err := repo.PushReference(context.Background(), attachmentDesc, bytes.NewReader(attachment), "attachment"); err != nil {
				t.Fatal(err)
			}
			if updates.Load() != 1 {
				t.Errorf("index updates = %d, want 1", updates.Load())
			}
			wantDeletes := int32(0)
			if tt.valid {
				wantDeletes = 1
			}
			if deletes.Load() != wantDeletes {
				t.Errorf("deletions = %d, want %d", deletes.Load(), wantDeletes)
			}
		})
	}
}

func TestRepository_Referrers_TagSchemaFallback_BadDigest(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "invalid-digest",
		Size:      int64(len(manifest)),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		if r.Method == http.MethodGet ||
			r.URL.Path == referrersUrl ||
			r.URL.Path == tagSchemaUrl {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_BadRequest(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		if r.Method == http.MethodGet ||
			r.URL.Path == referrersUrl ||
			r.URL.Path == tagSchemaUrl {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test auto detect
	// Referrers returns error
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}

	// test force attempt Referrers
	// Referrers returns error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test force attempt tag schema
	// Referrers returns error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_RepositoryNotFound(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersUrl := "/v2/test/referrers/" + manifestDesc.Digest.String()
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		tagSchemaUrl := "/v2/test/manifests/" + referrersTag
		if r.Method == http.MethodGet &&
			(r.URL.Path == referrersUrl || r.URL.Path == tagSchemaUrl) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{ "errors": [ { "code": "NAME_UNKNOWN", "message": "repository name not known to registry" } ] }`))
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test auto detect
	// repository not found, should fall back to the tag schema without error
	// and without caching the Referrers API capability
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		t.Errorf("Repository.Referrers() fn called with %v, want no referrers", got)
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, nil)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}

	// test force attempt Referrers
	// repository not found, should return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test force attempt tag schema
	// repository not found, but should not return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, nil)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_Referrers_ServerFiltering(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]ocispec.Descriptor{
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}

	// Test with filter annotations only
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		queryParams, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatal("failed to parse url query")
		}
		if r.Method != http.MethodGet ||
			r.URL.Path != path ||
			reflect.DeepEqual(queryParams["artifactType"], []string{"application%2Fvnd.test"}) {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
			// set filter annotations
			Annotations: map[string]string{
				spec.AnnotationReferrersFiltersApplied: "artifactType",
			},
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []ocispec.Descriptor) error {
		if index >= len(referrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := referrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if index != len(referrerSet) {
		t.Errorf("fn invoked %d time(s), want %d", index, len(referrerSet))
	}

	// Test with filter header only
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		queryParams, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatal("failed to parse url query")
		}
		if r.Method != http.MethodGet ||
			r.URL.Path != path ||
			reflect.DeepEqual(queryParams["artifactType"], []string{"application%2Fvnd.test"}) {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		// set filter header
		w.Header().Set("OCI-Filters-Applied", "artifactType")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2

	ctx = context.Background()
	index = 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []ocispec.Descriptor) error {
		if index >= len(referrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := referrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if index != len(referrerSet) {
		t.Errorf("fn invoked %d time(s), want %d", index, len(referrerSet))
	}

	// Test with both filter annotation and filter header
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		queryParams, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatal("failed to parse url query")
		}
		if r.Method != http.MethodGet ||
			r.URL.Path != path ||
			reflect.DeepEqual(queryParams["artifactType"], []string{"application%2Fvnd.test"}) {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
			// set filter annotation
			Annotations: map[string]string{
				spec.AnnotationReferrersFiltersApplied: "artifactType",
			},
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		// set filter header
		w.Header().Set("OCI-Filters-Applied", "artifactType")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2

	ctx = context.Background()
	index = 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []ocispec.Descriptor) error {
		if index >= len(referrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := referrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if index != len(referrerSet) {
		t.Errorf("fn invoked %d time(s), want %d", index, len(referrerSet))
	}
}

func TestRepository_Referrers_ClientFiltering(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]ocispec.Descriptor{
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.foo",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.bar",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.baz",
			},
		},
	}
	filteredReferrerSet := [][]ocispec.Descriptor{
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    spec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/referrers/" + manifestDesc.Digest.String()
		queryParams, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatal("failed to parse url query")
		}
		if r.Method != http.MethodGet ||
			r.URL.Path != path ||
			reflect.DeepEqual(queryParams["artifactType"], []string{"application%2Fvnd.test"}) {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 2 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var referrers []ocispec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []ocispec.Descriptor) error {
		if index >= len(filteredReferrerSet) {
			t.Fatalf("out of index bound: %d", index)
		}
		referrers := filteredReferrerSet[index]
		index++
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if index != len(filteredReferrerSet) {
		t.Errorf("fn invoked %d time(s), want %d", index, len(referrerSet))
	}
}

func TestRepository_Referrers_TagSchemaFallback_ClientFiltering(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	referrers := []ocispec.Descriptor{
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         2,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.foo",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.bar",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("5"),
			ArtifactType: "application/vnd.baz",
		},
	}
	filteredReferrers := []ocispec.Descriptor{
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.test",
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referrersTag := strings.Replace(manifestDesc.Digest.String(), ":", "-", 1)
		path := "/v2/test/manifests/" + referrersTag
		if r.Method != http.MethodGet || r.URL.Path != path {
			if r.URL.Path != "/v2/test/referrers/"+manifestDesc.Digest.String() {
				t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		result := ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: referrers,
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	ctx := context.Background()
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []ocispec.Descriptor) error {
		if !reflect.DeepEqual(got, filteredReferrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, filteredReferrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
}

func TestRepository_BadDigest(t *testing.T) {
	data := []byte("hello world")
	invalidDesc := ocispec.Descriptor{
		MediaType: "application/test",
		Digest:    "invalid-digest",
		Size:      int64(len(data)),
	}

	h := sha1.New()
	h.Write(data)
	unsupportedDesc := ocispec.Descriptor{
		MediaType: "application/test",
		Size:      int64(len(data)),
		Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
	}
	descs := []ocispec.Descriptor{invalidDesc, unsupportedDesc}
	for _, desc := range descs {
		t.Run("Test push", func(t *testing.T) {
			uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
					w.Header().Set("Location", "/v2/test/blobs/uploads/"+uuid)
					w.WriteHeader(http.StatusAccepted)
					return
				case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
					if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
						w.WriteHeader(http.StatusBadRequest)
						break
					}
					if contentDigest := r.URL.Query().Get("digest"); contentDigest != desc.Digest.String() {
						w.WriteHeader(http.StatusBadRequest)
						break
					}
					buf := bytes.NewBuffer(nil)
					if _, err := buf.ReadFrom(r.Body); err != nil {
						t.Errorf("fail to read: %v", err)
					}
					// Omit Docker-Content-Digest: the client digest is
					// intentionally invalid or an unavailable algorithm, so
					// echoing it would fail verifyContentDigest even though
					// this case is about accepting a bad client digest.
					w.WriteHeader(http.StatusCreated)
					return
				default:
					w.WriteHeader(http.StatusForbidden)
				}
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			repo, err := NewRepository(uri.Host + "/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			ctx := context.Background()
			err = repo.Push(ctx, desc, bytes.NewReader(data))
			if desc.Digest == unsupportedDesc.Digest {
				// An algorithm oras-go does not support is rejected before
				// any request; a malformed digest is left to the registry.
				if !errors.Is(err, errdef.ErrUnsupported) {
					t.Errorf("Repository.Push() error = %v, want errdef.ErrUnsupported", err)
				}
				return
			}
			if err != nil {
				t.Errorf("Repository.Push() error = %v", err)
			}
		})

		t.Run("Test exists", func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("unexpected access: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/v2/test/blobs/" + desc.Digest.String():
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Docker-Content-Digest", desc.Digest.String())
					w.Header().Set("Content-Length", strconv.Itoa(int(desc.Size)))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			repo, err := NewRepository(uri.Host + "/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			ctx := context.Background()

			if _, err = repo.Exists(ctx, desc); err == nil {
				t.Errorf("Repository.Exists() error = nil, wantErr %v", true)
			}
		})

		t.Run("Test fetch", func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected access: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/v2/test/blobs/" + desc.Digest.String():
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Docker-Content-Digest", desc.Digest.String())
					if _, err := w.Write(data); err != nil {
						t.Errorf("failed to write %q: %v", r.URL, err)
					}
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			repo, err := NewRepository(uri.Host + "/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			store := repo.Blobs()
			ctx := context.Background()

			if _, err = store.Fetch(ctx, desc); err == nil {
				t.Errorf("Repository.Fetch() error = nil, wantErr %v", true)
			}
		})

		t.Run("Test resolve", func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("unexpected access: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/v2/test/blobs/" + desc.Digest.String():
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Docker-Content-Digest", desc.Digest.String())
					w.Header().Set("Content-Length", strconv.Itoa(int(desc.Size)))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			repoName := uri.Host + "/test"
			repo, err := NewRepository(repoName)
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			ctx := context.Background()

			if _, err = repo.Blobs().Resolve(ctx, desc.Digest.String()); err == nil {
				t.Errorf("Repository.Resolve() error = nil, wantErr %v", true)
			}
		})
	}
}

func Test_BlobStore_Fetch(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	rc, err := store.Fetch(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Fetch() error = %v", err)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.Fetch() = %v, want %v", got, blob)
	}

	content := []byte("foobar")
	contentDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	_, err = store.Fetch(ctx, contentDesc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Blobs.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func Test_BlobStore_Fetch_Seek(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	seekable := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if seekable {
				w.Header().Set("Accept-Ranges", "bytes")
			}
			rangeHeader := r.Header.Get("Range")
			if !seekable || rangeHeader == "" {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write(blob); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
				return
			}
			var start, end int
			_, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
			if err != nil {
				t.Errorf("invalid range header: %s", rangeHeader)
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if start < 0 || start > end || start >= int(blobDesc.Size) {
				t.Errorf("invalid range: %s", rangeHeader)
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			end++
			if end > int(blobDesc.Size) {
				end = int(blobDesc.Size)
			}
			w.WriteHeader(http.StatusPartialContent)
			if _, err := w.Write(blob[start:end]); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	rc, err := store.Fetch(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Fetch() error = %v", err)
	}
	if _, ok := rc.(io.Seeker); ok {
		t.Errorf("Blobs.Fetch() returns io.Seeker on non-seekable content")
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.Fetch() = %v, want %v", got, blob)
	}

	seekable = true
	rc, err = store.Fetch(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Fetch() error = %v", err)
	}
	s, ok := rc.(io.Seeker)
	if !ok {
		t.Fatalf("Blobs.Fetch() = %v, want io.Seeker", rc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.Fetch() = %v, want %v", got, blob)
	}

	_, err = s.Seek(3, io.SeekStart)
	if err != nil {
		t.Errorf("fail to seek: %v", err)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob[3:]) {
		t.Errorf("Blobs.Fetch() = %v, want %v", got, blob[3:])
	}

	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
}

func Test_BlobStore_Fetch_ZeroSizedBlob(t *testing.T) {
	blob := []byte("")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
				t.Errorf("unexpected range header")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	rc, err := store.Fetch(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Fetch() error = %v", err)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.Fetch() = %v, want %v", got, blob)
	}
}

func Test_BlobStore_Fetch_BadResponse(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	t.Run("Docker-Content-Digest header mismatches", func(t *testing.T) {
		randomDgst := digest.FromBytes([]byte("random"))

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/blobs/" + blobDesc.Digest.String():
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Docker-Content-Digest", randomDgst.String())
				if _, err := w.Write(blob); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Blobs()
		ctx := context.Background()

		if _, err = store.Fetch(ctx, blobDesc); err == nil {
			t.Error("Blobs.Fetch() error = nil, want error")
		} else if !errors.Is(err, content.ErrMismatchedDigest) {
			t.Errorf("Blobs.Fetch() error = %v, want %v", err, content.ErrMismatchedDigest)
		}
	})

	t.Run("Content-Length header mismatches", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/blobs/" + blobDesc.Digest.String():
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
				w.Header().Set("Content-Length", strconv.FormatInt(blobDesc.Size+1, 10))
				if _, err := w.Write(blob); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Blobs()
		ctx := context.Background()

		if _, err = store.Fetch(ctx, blobDesc); err == nil {
			t.Error("Blobs.Fetch() error = nil, want error")
		}
	})
}

func Test_BlobStore_Push(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var gotBlob []byte
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotBlob = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Errorf("Blobs.Push() = %v, want %v", gotBlob, blob)
	}
}

func Test_BlobStore_Push_UploadLocationAuthorization(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	const token = "Bearer registry-token"
	const sessionPath = "/v2/test/blobs/uploads/session"

	tests := []struct {
		name      string
		crossHost bool
		wantAuth  string
	}{
		{name: "same host", crossHost: false, wantAuth: token},
		{name: "cross host", crossHost: true, wantAuth: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBlob []byte
			var gotAuth string
			var putCount int
			upload := func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != sessionPath {
					w.WriteHeader(http.StatusForbidden)
					t.Errorf("unexpected access: %s %s", r.Method, r.URL)
					return
				}
				if got := r.URL.Query().Get("digest"); got != blobDesc.Digest.String() {
					t.Errorf("digest = %q, want %q", got, blobDesc.Digest)
				}
				buf := bytes.NewBuffer(nil)
				if _, err := buf.ReadFrom(r.Body); err != nil {
					t.Errorf("failed to read PUT body: %v", err)
				}
				putCount++
				gotBlob = buf.Bytes()
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
				w.WriteHeader(http.StatusCreated)
			}
			uploadServer := httptest.NewServer(http.HandlerFunc(upload))
			defer uploadServer.Close()

			location := sessionPath
			if tt.crossHost {
				location = uploadServer.URL + sessionPath
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" {
					w.Header().Set("Location", location)
					w.WriteHeader(http.StatusAccepted)
					return
				}
				upload(w, r)
			}))
			defer ts.Close()

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
							req.Header.Set("Authorization", token)
						}
						return http.DefaultTransport.RoundTrip(req)
					}),
				},
			}

			if err := repo.Blobs().Push(context.Background(), blobDesc, bytes.NewReader(blob)); err != nil {
				t.Fatalf("Blobs.Push() error = %v", err)
			}
			if putCount != 1 {
				t.Fatalf("PUT count = %d, want 1", putCount)
			}
			if !bytes.Equal(gotBlob, blob) {
				t.Errorf("uploaded blob = %q, want %q", gotBlob, blob)
			}
			if gotAuth != tt.wantAuth {
				t.Errorf("PUT authorization = %q, want %q", gotAuth, tt.wantAuth)
			}
		})
	}
}

func Test_BlobStore_Push_CrossHostUploadChallenge(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	const sessionPath = "/v2/test/blobs/uploads/session"

	var mu sync.Mutex
	var uploadAuth []string
	uploads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uploadAuth = append(uploadAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="uploads"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer uploads.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if username, password, ok := r.BasicAuth(); !ok || username != "user" || password != "pass" {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" {
			w.Header().Set("Location", uploads.URL+sessionPath)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()

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
		Client: &http.Client{},
		CredentialFunc: credentials.StaticCredentialFunc(uri.Host, credentials.Credential{
			Username: "user",
			Password: "pass",
		}),
	}

	if err := repo.Blobs().Push(context.Background(), blobDesc, bytes.NewReader(blob)); err == nil {
		t.Fatal("Blobs.Push() error = nil, want an error from the upload host challenge")
	}
	if len(uploadAuth) == 0 {
		t.Fatal("upload host received no requests")
	}
	for i, got := range uploadAuth {
		if got != "" {
			t.Errorf("upload host request %d authorization = %q, want none", i, got)
		}
	}
}

func Test_BlobStore_Exists(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(blobDesc.Size)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	exists, err := store.Exists(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Exists() error = %v", err)
	}
	if !exists {
		t.Errorf("Blobs.Exists() = %v, want %v", exists, true)
	}

	content := []byte("foobar")
	contentDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	exists, err = store.Exists(ctx, contentDesc)
	if err != nil {
		t.Fatalf("Blobs.Exists() error = %v", err)
	}
	if exists {
		t.Errorf("Blobs.Exists() = %v, want %v", exists, false)
	}
}

func Test_BlobStore_Delete(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	blobDeleted := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			blobDeleted = true
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	err = store.Delete(ctx, blobDesc)
	if err != nil {
		t.Fatalf("Blobs.Delete() error = %v", err)
	}
	if !blobDeleted {
		t.Errorf("Blobs.Delete() = %v, want %v", blobDeleted, true)
	}

	content := []byte("foobar")
	contentDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	err = store.Delete(ctx, contentDesc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Blobs.Delete() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func Test_BlobStore_Resolve(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	t.Run("Successfully resolve", func(t *testing.T) {
		ref := "foobar"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/blobs/" + blobDesc.Digest.String():
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
				w.Header().Set("Content-Length", strconv.Itoa(int(blobDesc.Size)))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repoName := uri.Host + "/test"
		repo, err := NewRepository(repoName)
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Blobs()
		ctx := context.Background()

		got, err := store.Resolve(ctx, blobDesc.Digest.String())
		if err != nil {
			t.Fatalf("Blobs.Resolve() error = %v", err)
		}
		if got.Digest != blobDesc.Digest || got.Size != blobDesc.Size {
			t.Errorf("Blobs.Resolve() = %v, want %v", got, blobDesc)
		}

		_, err = store.Resolve(ctx, ref)
		if !errors.Is(err, digest.ErrDigestInvalidFormat) {
			t.Errorf("Blobs.Resolve() error = %v, wantErr %v", err, digest.ErrDigestInvalidFormat)
		}

		fqdnRef := repoName + "@" + blobDesc.Digest.String()
		got, err = store.Resolve(ctx, fqdnRef)
		if err != nil {
			t.Fatalf("Blobs.Resolve() error = %v", err)
		}
		if got.Digest != blobDesc.Digest || got.Size != blobDesc.Size {
			t.Errorf("Blobs.Resolve() = %v, want %v", got, blobDesc)
		}

		content := []byte("foobar")
		contentDesc := ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
		_, err = store.Resolve(ctx, contentDesc.Digest.String())
		if !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Blobs.Resolve() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}
	})

	t.Run("Resolve failed with server error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/blobs/" + blobDesc.Digest.String():
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repoName := uri.Host + "/test"
		repo, err := NewRepository(repoName)
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Blobs()
		ctx := context.Background()

		if _, err := store.Resolve(ctx, blobDesc.Digest.String()); err == nil {
			t.Error("Blobs.Resolve() error = nil, wantErr = true")
		}
	})

	t.Run("Resolve failed with bad reference", func(t *testing.T) {
		ref := "sha256:bad"
		repo, err := NewRepository("localhost:5000/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		store := repo.Blobs()
		ctx := context.Background()
		if _, err := store.Resolve(ctx, ref); err == nil {
			t.Error("Blobs.Resolve() error = nil, wantErr = true")
		}
	})

	t.Run("Resolve failed with connection error", func(t *testing.T) {
		ref := blobDesc.Digest.String()
		repo, err := NewRepository("localhost:9876/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		store := repo.Blobs()
		ctx := context.Background()
		if _, err := store.Resolve(ctx, ref); err == nil {
			t.Error("Blobs.Resolve() error = nil, wantErr = true")
		}
	})
}

func Test_BlobStore_FetchReference(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	// test with digest
	gotDesc, rc, err := store.FetchReference(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatalf("Blobs.FetchReference() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("Blobs.FetchReference() = %v, want %v", gotDesc, blobDesc)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.FetchReference() = %v, want %v", got, blob)
	}

	// test with non-digest reference
	_, _, err = store.FetchReference(ctx, ref)
	if !errors.Is(err, digest.ErrDigestInvalidFormat) {
		t.Errorf("Blobs.FetchReference() error = %v, wantErr %v", err, digest.ErrDigestInvalidFormat)
	}

	// test with FQDN reference
	fqdnRef := repoName + "@" + blobDesc.Digest.String()
	gotDesc, rc, err = store.FetchReference(ctx, fqdnRef)
	if err != nil {
		t.Fatalf("Blobs.FetchReference() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("Blobs.FetchReference() = %v, want %v", gotDesc, blobDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.FetchReference() = %v, want %v", got, blob)
	}

	content := []byte("foobar")
	contentDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// test with other digest
	_, _, err = store.FetchReference(ctx, contentDesc.Digest.String())
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Blobs.FetchReference() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func Test_BlobStore_FetchReference_Seek(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	seekable := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if seekable {
				w.Header().Set("Accept-Ranges", "bytes")
			}
			rangeHeader := r.Header.Get("Range")
			if !seekable || rangeHeader == "" {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write(blob); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
				return
			}
			var start int
			_, err := fmt.Sscanf(rangeHeader, "bytes=%d-", &start)
			if err != nil {
				t.Errorf("invalid range header: %s", rangeHeader)
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if start < 0 || start >= int(blobDesc.Size) {
				t.Errorf("invalid range: %s", rangeHeader)
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}

			w.WriteHeader(http.StatusPartialContent)
			if _, err := w.Write(blob[start:]); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	ctx := context.Background()

	// test non-seekable content
	gotDesc, rc, err := store.FetchReference(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatalf("Blobs.FetchReference() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("Blobs.FetchReference() = %v, want %v", gotDesc, blobDesc)
	}
	if _, ok := rc.(io.Seeker); ok {
		t.Errorf("Blobs.FetchReference() returns io.Seeker on non-seekable content")
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.FetchReference() = %v, want %v", got, blob)
	}

	// test seekable content
	seekable = true
	gotDesc, rc, err = store.FetchReference(ctx, blobDesc.Digest.String())
	if err != nil {
		t.Fatalf("Blobs.FetchReference() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("Blobs.FetchReference() = %v, want %v", gotDesc, blobDesc)
	}
	s, ok := rc.(io.Seeker)
	if !ok {
		t.Fatalf("Blobs.FetchReference() = %v, want io.Seeker", rc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob) {
		t.Errorf("Blobs.FetchReference() = %v, want %v", got, blob)
	}

	_, err = s.Seek(3, io.SeekStart)
	if err != nil {
		t.Errorf("fail to seek: %v", err)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, blob[3:]) {
		t.Errorf("Blobs.FetchReference() = %v, want %v", got, blob[3:])
	}

	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
}

func Test_generateBlobDescriptorWithVariousDockerContentDigestHeaders(t *testing.T) {
	reference := registry.Reference{
		Registry:   "eastern.haan.com",
		Reference:  "<calculate>",
		Repository: "from25to220ce",
	}
	tests := getTestIOStructMapForGetDescriptorClass()
	for testName, dcdIOStruct := range tests {
		if dcdIOStruct.isTag {
			continue
		}

		for i, method := range []string{http.MethodGet, http.MethodHead} {
			reference.Reference = dcdIOStruct.clientSuppliedReference

			resp := http.Response{
				Header: http.Header{
					"Content-Type":            []string{"application/vnd.docker.distribution.manifest.v2+json"},
					headerDockerContentDigest: []string{dcdIOStruct.serverCalculatedDigest.String()},
				},
			}
			if method == http.MethodGet {
				resp.Body = io.NopCloser(bytes.NewBufferString(theAmazingBanClan))
			}
			resp.Request = &http.Request{
				Method: method,
			}

			var err error
			var d digest.Digest
			if d, err = reference.GetDigest(); err != nil {
				t.Errorf(
					"[Blob.%v] %v; got digest from a tag reference unexpectedly",
					method, testName,
				)
			}

			errExpected := []bool{dcdIOStruct.errExpectedOnGET, dcdIOStruct.errExpectedOnHEAD}[i]
			if len(d) == 0 {
				// To avoid an otherwise impossible scenario in the tested code
				// path, we set d so that verifyContentDigest does not break.
				d = dcdIOStruct.serverCalculatedDigest
			}
			_, err = generateBlobDescriptor(&resp, d)
			if !errExpected && err != nil {
				t.Errorf(
					"[Blob.%v] %v; expected no error for request, but got err: %v",
					method, testName, err,
				)
			} else if errExpected && err == nil {
				t.Errorf(
					"[Blob.%v] %v; expected an error for request, but got none",
					method, testName,
				)
			}
		}
	}
}

func TestManifestStoreInterface(t *testing.T) {
	var ms any = &manifestStore{}
	if _, ok := ms.(registry.ReferenceParser); !ok {
		t.Error("&manifestStore{} does not conform registry.ReferenceParser")
	}
	if _, ok := ms.(content.Untagger); !ok {
		t.Error("&manifestStore{} does not conform content.Untagger")
	}
}

func TestRepositoryMounterInterface(t *testing.T) {
	var r any = &Repository{}
	if _, ok := r.(registry.Mounter); !ok {
		t.Error("&Repository{} does not conform to registry.Mounter")
	}
}

func Test_ManifestStore_Fetch(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	t.Run("successfull fetch", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String():
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", manifestDesc.MediaType)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				if _, err := w.Write(manifest); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		rc, err := store.Fetch(ctx, manifestDesc)
		if err != nil {
			t.Fatalf("Manifests.Fetch() error = %v", err)
		}
		buf := bytes.NewBuffer(nil)
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Errorf("fail to read: %v", err)
		}
		if err := rc.Close(); err != nil {
			t.Errorf("fail to close: %v", err)
		}
		if got := buf.Bytes(); !bytes.Equal(got, manifest) {
			t.Errorf("Manifests.Fetch() = %v, want %v", got, manifest)
		}

		content := []byte(`{"manifests":[]}`)
		contentDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageIndex,
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
		_, err = store.Fetch(ctx, contentDesc)
		if !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Manifests.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}
	})

	t.Run("fail with invalid Content-Type", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String():
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "invalid content type")
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				if _, err := w.Write(manifest); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
		}
	})

	t.Run("fail with mismatching Content-Type", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String():
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.other")
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				if _, err := w.Write(manifest); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
		}
	})

	// Content-Length mismatches are tolerated for manifests because some
	// registries (e.g. Harbor) return different values between HEAD (Resolve)
	// and GET responses. Integrity is guaranteed by the digest check instead.
	t.Run("succeed with mismatching Content-Length", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String():
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", manifestDesc.MediaType)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				if _, err := w.Write(manifest); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		// Use a descriptor with a wrong Size to simulate a HEAD/GET Content-Length
		// divergence as seen with Harbor when copying to a differently-named repo.
		wrongSizeDesc := manifestDesc
		wrongSizeDesc.Size = manifestDesc.Size + 100
		rc, err := store.Fetch(ctx, wrongSizeDesc)
		if err != nil {
			t.Fatalf("Manifests.Fetch() error = %v, want nil (Content-Length mismatch should be tolerated)", err)
		}
		rc.Close()
	})

	t.Run("fail with mismatching digest", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String():
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", manifestDesc.MediaType)
				w.Header().Set("Docker-Content-Digest", digest.FromBytes([]byte("random")).String())
				if _, err := w.Write(manifest); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
		} else if !errors.Is(err, content.ErrMismatchedDigest) {
			t.Errorf("Manifests.Fetch() error = %v, want %v", err, content.ErrMismatchedDigest)
		}
	})
}

func Test_ManifestStore_Push(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	var gotManifest []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	err = store.Push(ctx, manifestDesc, bytes.NewReader(manifest))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifest) {
		t.Errorf("Manifests.Push() = %v, want %v", gotManifest, manifest)
	}
}

func Test_ManifestStore_Push_ReferrersAPIAvailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	artifact := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subjectDesc,
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDesc,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)
	index := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Subject:   &subjectDesc,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc := content.NewDescriptorFromBytes(manifest.MediaType, indexJSON)

	var gotManifest []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test pushing artifact with subject
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, artifactDesc, bytes.NewReader(artifactJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test pushing image manifest with subject
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test pushing image index with subject
	err = repo.Push(ctx, indexDesc, bytes.NewReader(indexJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
}

func Test_ManifestStore_Push_ReferrersAPIUnavailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)
	artifact := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		Subject:      &subjectDesc,
		ArtifactType: "application/vnd.test",
		Annotations:  map[string]string{"foo": "bar"},
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)
	artifactDesc.ArtifactType = artifact.ArtifactType
	artifactDesc.Annotations = artifact.Annotations

	// test pushing artifact with subject, a referrer list should be created
	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
		},
	}
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_1 := content.NewDescriptorFromBytes(index_1.MediaType, indexJSON_1)
	var gotManifest []byte
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_1.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, artifactDesc, bytes.NewReader(artifactJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing artifact with subject when an old empty referrer list exists,
	// the referrer list should be updated
	emptyIndex := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
	}
	emptyIndexJSON, err := json.Marshal(emptyIndex)
	if err != nil {
		t.Error("failed to marshal index", err)
	}
	emptyIndexDesc := content.NewDescriptorFromBytes(emptyIndex.MediaType, emptyIndexJSON)
	var indexDeleted bool
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(emptyIndexJSON)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_1.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+emptyIndexDesc.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, artifactDesc, bytes.NewReader(artifactJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
	if !indexDeleted {
		t.Errorf("indexDeleted = %v, want %v", indexDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing image manifest with subject, referrer list should be updated
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "testconfig",
		},
		Subject:     &subjectDesc,
		Annotations: map[string]string{"foo": "bar"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)
	manifestDesc.ArtifactType = manifest.Config.MediaType
	manifestDesc.Annotations = manifest.Annotations
	index_2 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
			manifestDesc,
		},
	}
	indexJSON_2, err := json.Marshal(index_2)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_2 := content.NewDescriptorFromBytes(index_2.MediaType, indexJSON_2)
	indexDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_1)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_2.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_1.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if !indexDeleted {
		t.Errorf("indexDeleted = %v, want %v", indexDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing image manifest with subject again, referrers list should not be changed
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	// referrers list should not be changed
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// push image index with subject, referrer list should be updated
	indexManifest := ocispec.Index{
		MediaType:    ocispec.MediaTypeImageIndex,
		Subject:      &subjectDesc,
		ArtifactType: "test/index",
		Annotations:  map[string]string{"foo": "bar"},
	}
	indexManifestJSON, err := json.Marshal(indexManifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexManifestDesc := content.NewDescriptorFromBytes(indexManifest.MediaType, indexManifestJSON)
	indexManifestDesc.ArtifactType = indexManifest.ArtifactType
	indexManifestDesc.Annotations = indexManifest.Annotations
	index_3 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_3, err := json.Marshal(index_3)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_3 := content.NewDescriptorFromBytes(index_3.MediaType, indexJSON_3)
	indexDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexManifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexManifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_3.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_2.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, indexManifestDesc, bytes.NewReader(indexManifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexManifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexManifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_3) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_3))
	}
	if !indexDeleted {
		t.Errorf("indexDeleted = %v, want %v", indexDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_Push_ReferrersAPIUnavailable_SkipReferrersGC(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "testconfig",
		},
		Subject:     &subjectDesc,
		Annotations: map[string]string{"foo": "bar"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)
	manifestDesc.ArtifactType = manifest.Config.MediaType
	manifestDesc.Annotations = manifest.Annotations
	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc,
		},
	}

	// test pushing image manifest with subject, a referrers list should be created
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_1 := content.NewDescriptorFromBytes(index_1.MediaType, indexJSON_1)
	var gotManifest []byte
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_1.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing image manifest with subject when an old empty referrer list exists,
	// the referrer list should be updated
	emptyIndex := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
	}
	emptyIndexJSON, err := json.Marshal(emptyIndex)
	if err != nil {
		t.Error("failed to marshal index", err)
	}
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(emptyIndexJSON)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_1.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// push image index with subject, referrer list should be updated, the old
	// one should not be deleted
	indexManifest := ocispec.Index{
		MediaType:    ocispec.MediaTypeImageIndex,
		Subject:      &subjectDesc,
		ArtifactType: "test/index",
		Annotations:  map[string]string{"foo": "bar"},
	}
	indexManifestJSON, err := json.Marshal(indexManifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexManifestDesc := content.NewDescriptorFromBytes(indexManifest.MediaType, indexManifestJSON)
	indexManifestDesc.ArtifactType = indexManifest.ArtifactType
	indexManifestDesc.Annotations = indexManifest.Annotations
	index_2 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_2, err := json.Marshal(index_2)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_2 := content.NewDescriptorFromBytes(index_2.MediaType, indexJSON_2)
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexManifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexManifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_1)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_2.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.Push(ctx, indexManifestDesc, bytes.NewReader(indexManifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexManifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexManifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_Exists(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + manifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(manifestDesc.Size)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	exists, err := store.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Exists() error = %v", err)
	}
	if !exists {
		t.Errorf("Manifests.Exists() = %v, want %v", exists, true)
	}

	content := []byte(`{"manifests":[]}`)
	contentDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	exists, err = store.Exists(ctx, contentDesc)
	if err != nil {
		t.Fatalf("Manifests.Exists() error = %v", err)
	}
	if exists {
		t.Errorf("Manifests.Exists() = %v, want %v", exists, false)
	}
}

func Test_ManifestStore_Delete(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	manifestDeleted := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete && r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifest); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	// test deleting manifest without subject
	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}

	// test deleting content that does not exist
	content := []byte(`{"manifests":[]}`)
	contentDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	err = store.Delete(ctx, contentDesc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Manifests.Delete() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func Test_ManifestStore_Delete_ReferrersAPIAvailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	artifact := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subjectDesc,
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDesc,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)

	index := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Subject:   &subjectDesc,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc := content.NewDescriptorFromBytes(index.MediaType, indexJSON)

	var manifestDeleted bool
	var artifactDeleted bool
	var indexDeleted bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete && r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			artifactDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, artifactDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", artifactDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			if _, err := w.Write(artifactJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: []ocispec.Descriptor{},
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()
	// test deleting artifact with subject
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !artifactDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", artifactDeleted, true)
	}

	// test deleting manifest with subject
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}

	// test deleting index with subject
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	err = store.Delete(ctx, indexDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !indexDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", indexDeleted, true)
	}

	// test deleting content that does not exist
	content := []byte("whatever")
	contentDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ctx = context.Background()
	err = store.Delete(ctx, contentDesc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Manifests.Delete() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func Test_ManifestStore_Delete_ReferrersAPIUnavailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)

	artifact := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subjectDesc,
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDesc,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)

	indexManifest := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Subject:   &subjectDesc,
	}
	indexManifestJSON, err := json.Marshal(indexManifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexManifestDesc := content.NewDescriptorFromBytes(indexManifest.MediaType, indexManifestJSON)

	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_1 := content.NewDescriptorFromBytes(index_1.MediaType, indexJSON_1)
	index_2 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_2, err := json.Marshal(index_2)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_2 := content.NewDescriptorFromBytes(index_2.MediaType, indexJSON_2)
	index_3 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			indexManifestDesc,
		},
	}
	indexJSON_3, err := json.Marshal(index_3)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_3 := content.NewDescriptorFromBytes(index_3.MediaType, indexJSON_3)

	// test deleting artifact with subject, referrers list should be updated
	manifestDeleted := false
	indexDeleted := false
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, artifactDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", artifactDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			if _, err := w.Write(artifactJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_1)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_2.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_1.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if !indexDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test deleting manifest with subject, referrers list should be updated
	manifestDeleted = false
	indexDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifestJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_3.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_2.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if !indexDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test deleting index with a subject, referrers list should be updated
	manifestDeleted = false
	indexDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexManifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexManifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexManifestDesc.Digest.String())
			if _, err := w.Write(indexManifestJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_3)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_3.Digest.String():
			indexDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, indexManifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if !indexDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_Delete_ReferrersAPIUnavailable_SkipReferrersGC(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDesc,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)

	indexManifest := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Subject:   &subjectDesc,
	}
	indexManifestJSON, err := json.Marshal(indexManifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexManifestDesc := content.NewDescriptorFromBytes(indexManifest.MediaType, indexManifestJSON)

	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	index_2 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			indexManifestDesc,
		},
	}
	indexJSON_2, err := json.Marshal(index_2)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_2 := content.NewDescriptorFromBytes(index_2.MediaType, indexJSON_2)
	index_3 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{},
	}
	indexJSON_3, err := json.Marshal(index_3)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_3 := content.NewDescriptorFromBytes(index_3.MediaType, indexJSON_3)

	// test deleting image manifest with subject, referrers list should be updated,
	// the old one should not be deleted
	manifestDeleted := false
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifestJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_1)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_2.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SkipReferrersGC = true
	store := repo.Manifests()
	ctx := context.Background()

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test deleting index with a subject, referrers list should be updated,
	// the old one should not be deleted, an empty one should be pushed
	manifestDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+indexManifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexManifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexManifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexManifestDesc.Digest.String())
			if _, err := w.Write(indexManifestJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_3.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SkipReferrersGC = true
	store = repo.Manifests()
	ctx = context.Background()

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, indexManifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_3) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_3))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_Delete_ReferrersAPIUnavailable_InconsistentIndex(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)
	artifact := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subjectDesc,
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)

	// test inconsistent state: index not found
	manifestDeleted := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, artifactDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", artifactDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			if _, err := w.Write(artifactJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test inconsistent state: empty referrers list
	manifestDeleted = true
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, artifactDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", artifactDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			if _, err := w.Write(artifactJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: []ocispec.Descriptor{},
			}
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test inconsistent state: current referrer is not in referrers list
	manifestDeleted = true
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, artifactDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", artifactDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			if _, err := w.Write(artifactJSON); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: []ocispec.Descriptor{
					content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, []byte("whaterver")),
				},
			}
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_Resolve(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ref := "foobar"

	t.Run("Successfully resolve", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String(),
				"/v2/test/manifests/" + ref:
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", manifestDesc.MediaType)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Header().Set("Content-Length", strconv.Itoa(int(manifestDesc.Size)))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repoName := uri.Host + "/test"
		repo, err := NewRepository(repoName)
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		got, err := store.Resolve(ctx, manifestDesc.Digest.String())
		if err != nil {
			t.Fatalf("Manifests.Resolve() error = %v", err)
		}
		if !reflect.DeepEqual(got, manifestDesc) {
			t.Errorf("Manifests.Resolve() = %v, want %v", got, manifestDesc)
		}

		got, err = store.Resolve(ctx, ref)
		if err != nil {
			t.Fatalf("Manifests.Resolve() error = %v", err)
		}
		if !reflect.DeepEqual(got, manifestDesc) {
			t.Errorf("Manifests.Resolve() = %v, want %v", got, manifestDesc)
		}

		tagDigestRef := "whatever" + "@" + manifestDesc.Digest.String()
		got, err = repo.Resolve(ctx, tagDigestRef)
		if err != nil {
			t.Fatalf("Manifests.Resolve() error = %v", err)
		}
		if !reflect.DeepEqual(got, manifestDesc) {
			t.Errorf("Manifests.Resolve() = %v, want %v", got, manifestDesc)
		}

		fqdnRef := repoName + ":" + tagDigestRef
		got, err = repo.Resolve(ctx, fqdnRef)
		if err != nil {
			t.Fatalf("Manifests.Resolve() error = %v", err)
		}
		if !reflect.DeepEqual(got, manifestDesc) {
			t.Errorf("Manifests.Resolve() = %v, want %v", got, manifestDesc)
		}

		content := []byte(`{"manifests":[]}`)
		contentDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageIndex,
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
		_, err = store.Resolve(ctx, contentDesc.Digest.String())
		if !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Manifests.Resolve() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}
	})

	t.Run("Resolve failed with server error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/v2/test/manifests/" + manifestDesc.Digest.String(),
				"/v2/test/manifests/" + ref:
				if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
					t.Errorf("manifest not convertable: %s", accept)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repoName := uri.Host + "/test"
		repo, err := NewRepository(repoName)
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		if _, err := store.Resolve(ctx, ref); err == nil {
			t.Error("Manifests.Resolve() error = nil, wantErr = true")
		}
	})

	t.Run("Resolve failed with bad reference", func(t *testing.T) {
		ref := "sha256:bad"
		repo, err := NewRepository("localhost:5000/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		store := repo.Manifests()
		ctx := context.Background()
		if _, err := store.Resolve(ctx, ref); err == nil {
			t.Error("Manifests.Resolve() error = nil, wantErr = true")
		}
	})

	t.Run("Resolve failed with connection error", func(t *testing.T) {
		repo, err := NewRepository("localhost:9876/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		store := repo.Manifests()
		ctx := context.Background()
		if _, err := store.Resolve(ctx, ref); err == nil {
			t.Error("Manifests.Resolve() error = nil, wantErr = true")
		}
	})
}

func Test_ManifestStore_FetchReference(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + manifestDesc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifest); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	// test with tag
	gotDesc, rc, err := store.FetchReference(ctx, ref)
	if err != nil {
		t.Fatalf("Manifests.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", gotDesc, manifestDesc)
	}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, manifest) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", got, manifest)
	}

	// test with other tag
	randomRef := "whatever"
	_, _, err = store.FetchReference(ctx, randomRef)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Manifests.FetchReference() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test with digest
	gotDesc, rc, err = store.FetchReference(ctx, manifestDesc.Digest.String())
	if err != nil {
		t.Fatalf("Manifests.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", gotDesc, manifestDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, manifest) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", got, manifest)
	}

	// test with other digest
	randomContent := []byte("whatever")
	randomContentDigest := digest.FromBytes(randomContent)
	_, _, err = store.FetchReference(ctx, randomContentDigest.String())
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Manifests.FetchReference() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test with tag@digest
	tagDigestRef := randomRef + "@" + manifestDesc.Digest.String()
	gotDesc, rc, err = store.FetchReference(ctx, tagDigestRef)
	if err != nil {
		t.Fatalf("Manifests.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", gotDesc, manifestDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, manifest) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", got, manifest)
	}

	// test with FQDN
	fqdnRef := repoName + ":" + tagDigestRef
	gotDesc, rc, err = store.FetchReference(ctx, fqdnRef)
	if err != nil {
		t.Fatalf("Manifests.FetchReference() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", gotDesc, manifestDesc)
	}
	buf.Reset()
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, manifest) {
		t.Errorf("Manifests.FetchReference() = %v, want %v", got, manifest)
	}
}

func Test_ManifestStore_Tag(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+blobDesc.Digest.String():
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodPut &&
			r.URL.Path == "/v2/test/manifests/"+ref || r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	store := repo.Manifests()
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	err = store.Tag(ctx, blobDesc, ref)
	if err == nil {
		t.Errorf("Repository.Tag() error = %v, wantErr %v", err, true)
	}

	err = store.Tag(ctx, indexDesc, ref)
	if err != nil {
		t.Fatalf("Repository.Tag() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.Tag() = %v, want %v", gotIndex, index)
	}

	gotIndex = nil
	err = store.Tag(ctx, indexDesc, indexDesc.Digest.String())
	if err != nil {
		t.Fatalf("Repository.Tag() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.Tag() = %v, want %v", gotIndex, index)
	}
}

func Test_ManifestStore_Untag(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	// refOK, refAccepted and refNoContent exercise the three success status
	// codes; refNotFound and refError exercise the error branches.
	refOK := "ok"
	refAccepted := "accepted"
	refNoContent := "nocontent"
	refNotFound := "ghost"
	refError := "boom"

	var untagged bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refOK:
			untagged = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refAccepted:
			untagged = true
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refNoContent:
			untagged = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refNotFound:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+refError:
			// registries that do not support tag deletion answer 405
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	store := repo.Manifests()
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	// deleting by digest is rejected: only tags are accepted
	err = store.Untag(ctx, indexDesc.Digest.String())
	if !errors.Is(err, errdef.ErrInvalidReference) {
		t.Errorf("manifestStore.Untag() error = %v, wantErr %v", err, errdef.ErrInvalidReference)
	}

	// an unparsable reference is rejected before any request is sent
	err = store.Untag(ctx, "")
	if !errors.Is(err, errdef.ErrInvalidReference) {
		t.Errorf("manifestStore.Untag() error = %v, wantErr %v", err, errdef.ErrInvalidReference)
	}

	// 200, 202 and 204 are all treated as success
	for _, ref := range []string{refOK, refAccepted, refNoContent} {
		untagged = false
		err = store.Untag(ctx, ref)
		if err != nil {
			t.Fatalf("manifestStore.Untag() error = %v", err)
		}
		if !untagged {
			t.Errorf("manifestStore.Untag() did not send DELETE request for %q", ref)
		}
	}

	err = store.Untag(ctx, refNotFound)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("manifestStore.Untag() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	err = store.Untag(ctx, refError)
	if err == nil {
		t.Errorf("manifestStore.Untag() error = %v, wantErr %v", err, true)
	}

	// a canceled context makes the underlying request fail
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = store.Untag(canceledCtx, refAccepted)
	if err == nil {
		t.Errorf("manifestStore.Untag() error = %v, wantErr %v", err, true)
	}
}

func Test_ManifestStore_PushReference(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+ref:
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	store := repo.Manifests()
	repo.Registry.PlainHTTP = true
	ctx := context.Background()
	err = store.PushReference(ctx, indexDesc, bytes.NewReader(index), ref)
	if err != nil {
		t.Fatalf("Repository.PushReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.PushReference() = %v, want %v", gotIndex, index)
	}
}

func Test_ManifestStore_PushReference_ReferrersAPIAvailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	artifact := spec.Artifact{
		MediaType: spec.MediaTypeArtifactManifest,
		Subject:   &subjectDesc,
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)
	artifactRef := "foo"

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDesc,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)
	manifestRef := "bar"

	index := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Subject:   &subjectDesc,
	}
	indexJSON, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc := content.NewDescriptorFromBytes(index.MediaType, indexJSON)
	indexRef := "baz"

	var gotManifest []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactRef:
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestRef:
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexRef:
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("OCI-Subject", subjectDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: []ocispec.Descriptor{},
			}
			if err := json.NewEncoder(w).Encode(result); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test pushing artifact with subject
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, artifactDesc, bytes.NewReader(artifactJSON), artifactRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test pushing image manifest with subject
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestJSON), manifestRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test pushing image index with subject
	err = repo.PushReference(ctx, indexDesc, bytes.NewReader(indexJSON), indexRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexJSON))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
}

func Test_ManifestStore_PushReference_ReferrersAPIUnavailable(t *testing.T) {
	// generate test content
	subject := []byte(`{"layers":[]}`)
	subjectDesc := content.NewDescriptorFromBytes(spec.MediaTypeArtifactManifest, subject)
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)
	artifact := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		Subject:      &subjectDesc,
		ArtifactType: "application/vnd.test",
		Annotations:  map[string]string{"foo": "bar"},
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)
	artifactDesc.ArtifactType = artifact.ArtifactType
	artifactDesc.Annotations = artifact.Annotations
	artifactRef := "foo"

	// test pushing artifact with subject
	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
		},
	}
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_1 := content.NewDescriptorFromBytes(index_1.MediaType, indexJSON_1)
	var gotManifest []byte
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactRef:
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_1.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, artifactDesc, bytes.NewReader(artifactJSON), artifactRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing image manifest with subject, referrers list should be updated
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "testconfig",
		},
		Subject:     &subjectDesc,
		Annotations: map[string]string{"foo": "bar"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	manifestDesc := content.NewDescriptorFromBytes(manifest.MediaType, manifestJSON)
	manifestDesc.ArtifactType = manifest.Config.MediaType
	manifestDesc.Annotations = manifest.Annotations
	manifestRef := "bar"

	index_2 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
			manifestDesc,
		},
	}
	indexJSON_2, err := json.Marshal(index_2)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_2 := content.NewDescriptorFromBytes(index_2.MediaType, indexJSON_2)
	var manifestDeleted bool
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestRef:
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_1)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_2.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_1.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestJSON), manifestRef)
	if err != nil {
		t.Fatalf("Manifests.PushReference() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.PushReference() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if !manifestDeleted {
		t.Errorf("manifestDeleted = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// test pushing image manifest with subject again, referrers list should not be changed
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+manifestRef:
			if contentType := r.Header.Get("Content-Type"); contentType != manifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestJSON), manifestRef)
	if err != nil {
		t.Fatalf("Manifests.PushReference() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.PushReference() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	// referrers list should not be changed
	if !bytes.Equal(gotReferrerIndex, indexJSON_2) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_2))
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}

	// push image index with subject, referrer list should be updated
	indexManifest := ocispec.Index{
		MediaType:    ocispec.MediaTypeImageIndex,
		Subject:      &subjectDesc,
		ArtifactType: "test/index",
		Annotations:  map[string]string{"foo": "bar"},
	}
	indexManifestJSON, err := json.Marshal(indexManifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexManifestDesc := content.NewDescriptorFromBytes(indexManifest.MediaType, indexManifestJSON)
	indexManifestDesc.ArtifactType = indexManifest.ArtifactType
	indexManifestDesc.Annotations = indexManifest.Annotations
	indexManifestRef := "baz"
	index_3 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
			manifestDesc,
			indexManifestDesc,
		},
	}
	indexJSON_3, err := json.Marshal(index_3)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	indexDesc_3 := content.NewDescriptorFromBytes(index_3.MediaType, indexJSON_3)
	manifestDeleted = false
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexManifestRef:
			if contentType := r.Header.Get("Content-Type"); contentType != indexManifestDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotManifest = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexManifestDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Write(indexJSON_2)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc_3.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/manifests/"+indexDesc_2.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx = context.Background()
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	err = repo.PushReference(ctx, indexManifestDesc, bytes.NewReader(indexManifestJSON), indexManifestRef)
	if err != nil {
		t.Fatalf("Manifests.PushReference() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexManifestJSON) {
		t.Errorf("Manifests.PushReference() = %v, want %v", string(gotManifest), string(indexManifestJSON))
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_3) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_3))
	}
	if !manifestDeleted {
		t.Errorf("manifestDeleted = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func Test_ManifestStore_generateDescriptorWithVariousDockerContentDigestHeaders(t *testing.T) {
	reference := properties.Reference{
		Registry:   "eastern.haan.com",
		Repository: "from25to220ce",
	}

	tests := getTestIOStructMapForGetDescriptorClass()
	for testName, dcdIOStruct := range tests {
		repo, err := NewRepository(fmt.Sprintf("%s/%s", reference.Repository, reference.Repository))
		if err != nil {
			t.Fatalf("failed to initialize repository")
		}

		s := manifestStore{repo: repo}

		for i, method := range []string{http.MethodGet, http.MethodHead} {
			reference.Tag = ""
			reference.Digest = ""
			if dcdIOStruct.isTag {
				reference.Tag = dcdIOStruct.clientSuppliedReference
			} else {
				reference.Digest = dcdIOStruct.clientSuppliedReference
			}

			resp := http.Response{
				Header: http.Header{
					"Content-Type":            []string{"application/vnd.docker.distribution.manifest.v2+json"},
					headerDockerContentDigest: []string{dcdIOStruct.serverCalculatedDigest.String()},
				},
			}
			if method == http.MethodGet {
				resp.Body = io.NopCloser(bytes.NewBufferString(theAmazingBanClan))
			}
			resp.Request = &http.Request{
				Method: method,
			}

			errExpected := []bool{dcdIOStruct.errExpectedOnGET, dcdIOStruct.errExpectedOnHEAD}[i]
			_, err = s.generateDescriptor(&resp, reference, method)
			if !errExpected && err != nil {
				t.Errorf(
					"[Manifest.%v] %v; expected no error for request, but got err: %v",
					method, testName, err,
				)
			} else if errExpected && err == nil {
				t.Errorf(
					"[Manifest.%v] %v; expected an error for request, but got none",
					method, testName,
				)
			}
		}
	}
}

func Test_ManifestStore_updateReferrersIndex_BadDigest(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	repo, err := NewRepository("localhost:5000/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	store := manifestStore{repo: repo}

	t.Run("invalid digest", func(t *testing.T) {
		subject := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    "invalid-digest",
			Size:      int64(len(manifest)),
		}
		ctx := context.Background()
		if err := store.updateReferrersIndex(ctx, subject, referrerChange{}); !errors.Is(err, digest.ErrDigestInvalidFormat) {
			t.Errorf("updateReferrersIndex() error = %v, wantErr %v", err, digest.ErrDigestInvalidFormat)
		}
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		h := sha1.New()
		h.Write(manifest)
		subject := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Size:      int64(len(manifest)),
			Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		}
		ctx := context.Background()
		if err := store.updateReferrersIndex(ctx, subject, referrerChange{}); !errors.Is(err, digest.ErrDigestUnsupported) {
			t.Errorf("updateReferrersIndex() error = %v, wantErr %v", err, digest.ErrDigestUnsupported)
		}
	})
}

type testTransport struct {
	proxyHost           string
	underlyingTransport http.RoundTripper
	mockHost            string
}

func (t *testTransport) RoundTrip(originalReq *http.Request) (*http.Response, error) {
	req := originalReq.Clone(originalReq.Context())
	mockHostName, mockPort, err := net.SplitHostPort(t.mockHost)
	// when t.mockHost is as form host:port
	if err == nil && (req.URL.Hostname() != mockHostName || req.URL.Port() != mockPort) {
		return nil, errors.New("bad request")
	}
	// when t.mockHost does not have specified port, in this case,
	// err is not nil
	if err != nil && req.URL.Hostname() != t.mockHost {
		return nil, errors.New("bad request")
	}
	req.Host = t.proxyHost
	req.URL.Host = t.proxyHost
	resp, err := t.underlyingTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Request.Host = t.mockHost
	resp.Request.URL.Host = t.mockHost
	return resp, nil
}

// Helper function to create a registry.BlobStore for
// Test_BlobStore_Push_Port443
func blobStore_Push_Port443_create_store(uri *url.URL, testRegistry string) (registry.BlobStore, error) {
	repo, err := NewRepository(testRegistry + "/test")
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: &testTransport{
				proxyHost:           uri.Host,
				underlyingTransport: http.DefaultTransport,
				mockHost:            testRegistry,
			},
		},
		Cache: auth.NewCache(),
	}
	repo.Registry.PlainHTTP = true
	store := repo.Blobs()
	return store, err
}

func Test_BlobStore_Push_Port443(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "http://registry.wabbit-networks.io/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	// Test case with Host: "registry.wabbit-networks.io:443",
	// Location: "registry.wabbit-networks.io"
	testRegistry := "registry.wabbit-networks.io:443"
	store, err := blobStore_Push_Port443_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_create_store() error = %v", err)
	}
	ctx := context.Background()

	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}

	// Test case with Host: "registry.wabbit-networks.io",
	// Location: "registry.wabbit-networks.io"
	testRegistry = "registry.wabbit-networks.io"
	store, err = blobStore_Push_Port443_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_create_store() error = %v", err)
	}

	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}
}

// Helper function to create a registry.BlobStore for
// Test_BlobStore_Push_Port443_HTTPS
func blobStore_Push_Port443_HTTPS_create_store(uri *url.URL, testRegistry string) (registry.BlobStore, error) {
	repo, err := NewRepository(testRegistry + "/test")
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{
			Transport: &testTransport{
				proxyHost:           uri.Host,
				underlyingTransport: transport,
				mockHost:            testRegistry,
			},
		},
		Cache: auth.NewCache(),
	}
	repo.Registry.PlainHTTP = false
	store := repo.Blobs()
	return store, err
}

func Test_BlobStore_Push_Port443_HTTPS(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "https://registry.wabbit-networks.io/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test https server: %v", err)
	}

	ctx := context.Background()
	// Test case with Host: "registry.wabbit-networks.io:443",
	// Location: "registry.wabbit-networks.io"
	testRegistry := "registry.wabbit-networks.io:443"
	store, err := blobStore_Push_Port443_HTTPS_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_HTTPS_create_store() error = %v", err)
	}
	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}

	// Test case with Host: "registry.wabbit-networks.io",
	// Location: "registry.wabbit-networks.io"
	testRegistry = "registry.wabbit-networks.io"
	store, err = blobStore_Push_Port443_HTTPS_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_HTTPS_create_store() error = %v", err)
	}
	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}

	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "https://registry.wabbit-networks.io:443/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err = url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test https server: %v", err)
	}

	// Test case with Host: "registry.wabbit-networks.io:443",
	// Location: "registry.wabbit-networks.io:443"
	testRegistry = "registry.wabbit-networks.io:443"
	store, err = blobStore_Push_Port443_HTTPS_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_HTTPS_create_store() error = %v", err)
	}
	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}

	// Test case with Host: "registry.wabbit-networks.io",
	// Location: "registry.wabbit-networks.io:443"
	testRegistry = "registry.wabbit-networks.io"
	store, err = blobStore_Push_Port443_HTTPS_create_store(uri, testRegistry)
	if err != nil {
		t.Fatalf("blobStore_Push_Port443_HTTPS_create_store() error = %v", err)
	}
	err = store.Push(ctx, blobDesc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}
}

// Testing `last` parameter for Tags list
func TestRepository_Tags_WithLastParam(t *testing.T) {
	tagSet := strings.Split("abcdefghijklmnopqrstuvwxyz", "")
	var offset int
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/test/tags/list" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n != 4 {
			t.Errorf("bad page size: %s", q.Get("n"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		last := q.Get("last")
		if last != "" {
			offset = indexOf(last, tagSet) + 1
		}
		var tags []string
		switch q.Get("test") {
		case "foo":
			tags = tagSet[offset : offset+n]
			offset += n
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/test/tags/list?n=4&last=v&test=bar>; rel="next"`, ts.URL))
		case "bar":
			tags = tagSet[offset : offset+n]
		default:
			tags = tagSet[offset : offset+n]
			offset += n
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/test/tags/list?n=4&last=r&test=foo>; rel="next"`, ts.URL))
		}
		result := struct {
			Tags []string `json:"tags"`
		}{
			Tags: tags,
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.TagListPageSize = 4
	last := "n"
	startInd := indexOf(last, tagSet) + 1

	ctx := context.Background()
	if err := repo.Tags(ctx, last, func(got []string) error {
		want := tagSet[startInd : startInd+repo.TagListPageSize]
		startInd += repo.TagListPageSize
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Registry.Repositories() = %v, want %v", got, want)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Tags() error = %v", err)
	}
}

func TestRepository_policyImageReference_LowerCasesHost(t *testing.T) {
	repo, err := NewRepository("Quay.IO/secure/app")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	got := repo.policyImageReference("")
	if want := "quay.io/secure/app"; got.Scope != want {
		t.Errorf("policyImageReference().Scope = %q, want %q", got.Scope, want)
	}
}

func TestRepository_ParseReference(t *testing.T) {
	type args struct {
		reference string
	}
	tests := []struct {
		name    string
		repoRef properties.Reference
		args    args
		want    properties.Reference
		wantErr error
	}{
		{
			name: "parse tag",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "foobar",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Tag:        "foobar",
			},
			wantErr: nil,
		},
		{
			name: "parse digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse tag@digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN tag",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world:foobar",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Tag:        "foobar",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN tag@digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Tag:        "foobar",
				Digest:     "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "empty reference",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing repository",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "myregistry.example.com:hello-world",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "registry mismatch",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "myregistry.example.com/hello-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "repository mismatch",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/goodbye-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "FQDN invalid repository with digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "myregistry.example.com/Hello-World@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "FQDN invalid tag with digest",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world:bad tag@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "digest posing as a tag",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com:5000/hello-world:sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference after the at sign",
			repoRef: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world@",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference after the colon",
			repoRef: properties.Reference{
				Registry: "localhost",
			},
			args: args{
				reference: "localhost:5000/hello:",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "zero-size tag, zero-size digest",
			repoRef: properties.Reference{},
			args: args{
				reference: "localhost:5000/hello:@",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "zero-size tag with valid digest",
			repoRef: properties.Reference{},
			args: args{
				reference: "localhost:5000/hello:@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "valid tag with zero-size digest",
			repoRef: properties.Reference{},
			args: args{
				reference: "localhost:5000/hello:foobar@",
			},
			want:    properties.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &Registry{
				Reference: properties.Reference{Registry: tt.repoRef.Registry},
			}
			r := &Repository{
				Registry:       reg,
				RepositoryName: tt.repoRef.Repository,
			}
			got, err := r.ParseReference(tt.args.reference)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Repository.ParseReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Repository.ParseReference() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRepository_SetReferrersCapability(t *testing.T) {
	repo, err := NewRepository("registry.example.com/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	// initial state
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}

	// first set
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// conflicting second set is silently ignored; state should be unchanged
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v (conflicting set should be ignored)", state, properties.ReferrersAPISupported)
	}
}

func Test_generateIndex(t *testing.T) {
	referrer_1 := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		ArtifactType: "foo",
	}
	referrerJSON_1, err := json.Marshal(referrer_1)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	referrer_2 := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		ArtifactType: "bar",
	}
	referrerJSON_2, err := json.Marshal(referrer_2)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	referrers := []ocispec.Descriptor{
		content.NewDescriptorFromBytes(referrer_1.MediaType, referrerJSON_1),
		content.NewDescriptorFromBytes(referrer_2.MediaType, referrerJSON_2),
	}

	wantIndex := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: referrers,
	}
	wantIndexJSON, err := json.Marshal(wantIndex)
	if err != nil {
		t.Fatal("failed to marshal index:", err)
	}
	wantIndexDesc, err := content.NewDescriptorFromBytesWithAlgorithm(wantIndex.MediaType, wantIndexJSON, digest.Canonical)
	if err != nil {
		t.Fatal("failed to generate descriptor:", err)
	}

	wantEmptyIndex := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{},
	}
	wantEmptyIndexJSON, err := json.Marshal(wantEmptyIndex)
	if err != nil {
		t.Fatal("failed to marshal index:", err)
	}
	wantEmptyIndexDesc, err := content.NewDescriptorFromBytesWithAlgorithm(wantEmptyIndex.MediaType, wantEmptyIndexJSON, digest.Canonical)
	if err != nil {
		t.Fatal("failed to generate descriptor:", err)
	}

	tests := []struct {
		name      string
		manifests []ocispec.Descriptor
		alg       digest.Algorithm
		wantDesc  ocispec.Descriptor
		wantBytes []byte
		wantErr   bool
	}{
		{
			name:      "non-empty referrers list",
			manifests: referrers,
			alg:       digest.Canonical,
			wantDesc:  wantIndexDesc,
			wantBytes: wantIndexJSON,
			wantErr:   false,
		},
		{
			name:      "nil referrers list",
			manifests: nil,
			alg:       digest.Canonical,
			wantDesc:  wantEmptyIndexDesc,
			wantBytes: wantEmptyIndexJSON,
			wantErr:   false,
		},
		{
			name:      "empty referrers list",
			manifests: nil,
			alg:       digest.Canonical,
			wantDesc:  wantEmptyIndexDesc,
			wantBytes: wantEmptyIndexJSON,
			wantErr:   false,
		},
		{
			name:      "SHA-384 algorithm",
			manifests: referrers,
			alg:       digest.SHA384,
			wantDesc: func() ocispec.Descriptor {
				desc, err := content.NewDescriptorFromBytesWithAlgorithm(wantIndex.MediaType, wantIndexJSON, digest.SHA384)
				if err != nil {
					t.Fatal("failed to generate descriptor:", err)
				}
				return desc
			}(),
			wantBytes: wantIndexJSON,
			wantErr:   false,
		},
		{
			name:      "SHA-512 algorithm",
			manifests: referrers,
			alg:       digest.SHA512,
			wantDesc: func() ocispec.Descriptor {
				desc, err := content.NewDescriptorFromBytesWithAlgorithm(wantIndex.MediaType, wantIndexJSON, digest.SHA512)
				if err != nil {
					t.Fatal("failed to generate descriptor:", err)
				}
				return desc
			}(),
			wantBytes: wantIndexJSON,
			wantErr:   false,
		},
		{
			name:      "empty algorithm uses SHA-256",
			manifests: referrers,
			alg:       "",
			wantDesc:  wantIndexDesc,
			wantBytes: wantIndexJSON,
			wantErr:   false,
		},
		{
			name:      "unsupported algorithm errors",
			manifests: referrers,
			alg:       digest.Algorithm("sha1"),
			wantDesc:  ocispec.Descriptor{},
			wantBytes: nil,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := generateIndex(tt.manifests, tt.alg)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateReferrersIndex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.wantDesc) {
				t.Errorf("generateReferrersIndex() got = %v, want %v", got, tt.wantDesc)
			}
			if !reflect.DeepEqual(got1, tt.wantBytes) {
				t.Errorf("generateReferrersIndex() got1 = %v, want %v", got1, tt.wantBytes)
			}
		})
	}
}

func Test_ManifestStore_Push_ReferrersAPIUnavailable_SHA384Subject(t *testing.T) {
	// generate test content: the subject is digested with SHA-384, so the
	// referrers tag is sha384-*. That the generated referrers index is
	// digested with the subject's algorithm is covered by Test_generateIndex;
	// this end-to-end test covers pushing the index by tag to a registry
	// that canonicalizes tag pushes to SHA-256, which must not fail the
	// push.
	// Note: a SHA-512 subject is not usable here because its referrers tag
	// exceeds the reference length limit, hence SHA-384 is the only
	// non-SHA-256 algorithm covered by this end-to-end test.
	subject := []byte(`{"layers":[]}`)
	subjectDesc, err := content.NewDescriptorFromBytesWithAlgorithm(spec.MediaTypeArtifactManifest, subject, digest.SHA384)
	if err != nil {
		t.Fatalf("failed to generate subject descriptor: %v", err)
	}
	referrersTag := strings.Replace(subjectDesc.Digest.String(), ":", "-", 1)
	artifact := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
		Subject:      &subjectDesc,
		ArtifactType: "application/vnd.test",
		Annotations:  map[string]string{"foo": "bar"},
	}
	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	artifactDesc := content.NewDescriptorFromBytes(artifact.MediaType, artifactJSON)
	artifactDesc.ArtifactType = artifact.ArtifactType
	artifactDesc.Annotations = artifact.Annotations

	// the referrers index the client should generate and push for the
	// artifact's subject
	index_1 := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			artifactDesc,
		},
	}
	indexJSON_1, err := json.Marshal(index_1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	var gotReferrerIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+artifactDesc.Digest.String():
			if contentType := r.Header.Get("Content-Type"); contentType != artifactDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			w.Header().Set("Docker-Content-Digest", artifactDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+referrersTag:
			if contentType := r.Header.Get("Content-Type"); contentType != ocispec.MediaTypeImageIndex {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotReferrerIndex = buf.Bytes()
			// like a registry that canonicalizes tag pushes, report the
			// digest of the received content under SHA-256
			w.Header().Set("Docker-Content-Digest", digest.Canonical.FromBytes(buf.Bytes()).String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if err := repo.Push(ctx, artifactDesc, bytes.NewReader(artifactJSON)); err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotReferrerIndex, indexJSON_1) {
		t.Errorf("got referrers index = %v, want %v", string(gotReferrerIndex), string(indexJSON_1))
	}
}

func TestRepository_pingReferrers(t *testing.T) {
	t.Run("referrers available", func(t *testing.T) {
		count := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
				count++
				w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}

		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		ctx := context.Background()
		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true

		// 1st call
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
		}
		got, err := repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != true {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
		}
		if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}

		// 2nd call
		if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
		}
		got, err = repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != true {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
		}
		if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}
	})

	t.Run("referrers unavailable", func(t *testing.T) {
		count := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
				count++
				w.WriteHeader(http.StatusNotFound)
			default:
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}

		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		ctx := context.Background()
		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true

		// 1st call
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
		}
		got, err := repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != false {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
		}
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}

		// 2nd call
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
		}
		got, err = repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != false {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
		}
		if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}
	})

	t.Run("referrers unavailable incorrect content type", func(t *testing.T) {
		count := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
				count++
				w.Header().Set("Content-Type", "text/html") // can be anything except an OCI image index
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}

		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		ctx := context.Background()
		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true

		got, err := repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != false {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}
	})

	t.Run("referrers available content type with a parameter", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
				w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex+"; charset=utf-8")
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		uri, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("invalid test http server: %v", err)
		}

		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatalf("NewRepository() error = %v", err)
		}
		repo.Registry.PlainHTTP = true

		got, err := repo.pingReferrers(context.Background())
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != true {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
		}
	})
}

func TestRepository_pingReferrers_RepositoryNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{ "errors": [ { "code": "NAME_UNKNOWN", "message": "repository name not known to registry" } ] }`))
			return
		}
		t.Errorf("unexpected access: %s %q", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	ctx := context.Background()

	// test referrers state unknown
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	if _, err = repo.pingReferrers(ctx); err == nil {
		t.Fatalf("Repository.pingReferrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}

	// test referrers state supported
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
	got, err := repo.pingReferrers(ctx)
	if err != nil {
		t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
	}
	if got != true {
		t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}

	// test referrers state unsupported
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
	got, err = repo.pingReferrers(ctx)
	if err != nil {
		t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
	}
	if got != false {
		t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnsupported)
	}
}

func TestRepository_pingReferrers_Concurrent(t *testing.T) {
	// referrers available
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/test/referrers/"+zeroDigest:
			atomic.AddInt32(&count, 1)
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}

	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	ctx := context.Background()
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)

	if state := repo.loadReferrersState(); state != properties.ReferrersAPIUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPIUnknown)
	}
	for range concurrency {
		eg.Go(func() func() error {
			return func() error {
				got, err := repo.pingReferrers(egCtx)
				if err != nil {
					t.Fatalf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
				}
				if got != true {
					t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
				}
				return nil
			}
		}())
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
	}
	if state := repo.loadReferrersState(); state != properties.ReferrersAPISupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, properties.ReferrersAPISupported)
	}
}

func TestRepository_do(t *testing.T) {
	data := []byte(`hello world!`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/test" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Add("Warning", `299 - "Test 1: Good warning."`)
		w.Header().Add("Warning", `199 - "Test 2: Warning with a non-299 code."`)
		w.Header().Add("Warning", `299 - "Test 3: Good warning."`)
		w.Header().Add("Warning", `299 myregistry.example.com "Test 4: Warning with a non-unknown agent"`)
		w.Header().Add("Warning", `299 - "Test 5: Warning with a date." "Sat, 25 Aug 2012 23:34:45 GMT"`)
		w.Header().Add("wArnIng", `299 - "Test 6: Good warning."`)
		w.Write(data)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	testURL := ts.URL + "/test"

	// test do() without HandleWarning
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatal("NewRepository() error =", err)
	}
	req, err := http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		t.Fatal("failed to create test request:", err)
	}
	resp, err := repo.do(req)
	if err != nil {
		t.Fatal("Repository.do() error =", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Repository.do() status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if got := len(resp.Header["Warning"]); got != 6 {
		t.Errorf("Repository.do() warning header len = %v, want %v", got, 6)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal("io.ReadAll() error =", err)
	}
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("Repository.do() = %v, want %v", got, data)
	}

	// test do() with HandleWarning
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatal("NewRepository() error =", err)
	}
	var gotWarnings []Warning
	repo.Registry.HandleWarning = func(warning Warning) {
		gotWarnings = append(gotWarnings, warning)
	}

	req, err = http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		t.Fatal("failed to create test request:", err)
	}
	resp, err = repo.do(req)
	if err != nil {
		t.Fatal("Repository.do() error =", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Repository.do() status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if got := len(resp.Header["Warning"]); got != 6 {
		t.Errorf("Repository.do() warning header len = %v, want %v", got, 6)
	}
	got, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("Repository.do() = %v, want %v", got, data)
	}
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("Repository.do() = %v, want %v", got, data)
	}

	wantWarnings := []Warning{
		{
			WarningValue: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  "Test 1: Good warning.",
			},
		},
		{
			WarningValue: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  "Test 3: Good warning.",
			},
		},
		{
			WarningValue: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  "Test 6: Good warning.",
			},
		},
	}
	if !reflect.DeepEqual(gotWarnings, wantWarnings) {
		t.Errorf("Repository.do() = %v, want %v", gotWarnings, wantWarnings)
	}
}

func TestRegistry_newRepository(t *testing.T) {
	t.Run("valid reference and registry", func(t *testing.T) {
		reg, err := NewRegistry("registry.example.com")
		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}
		reg.PlainHTTP = true

		repo, err := reg.newRepository("test")
		if err != nil {
			t.Fatalf("Registry.newRepository() error = %v", err)
		}
		if repo.Registry.PlainHTTP != reg.PlainHTTP {
			t.Errorf("Repository.Registry.PlainHTTP = %v, want %v", repo.Registry.PlainHTTP, reg.PlainHTTP)
		}
		if repo.RepositoryName != "test" {
			t.Errorf("Repository.RepositoryName = %v, want %v", repo.RepositoryName, "test")
		}
	})

	t.Run("invalid repository name", func(t *testing.T) {
		reg, err := NewRegistry("registry.example.com")
		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}
		if _, err := reg.newRepository(""); err == nil {
			t.Error("Registry.newRepository() error = nil, wantErr")
		}
	})
}

// TestRepository_clone_copiesEveryField fails when a field is added to
// Repository but not to clone(), which would otherwise be silently dropped on
// cloned repositories. Exported fields and mirrors must be copied; the
// referrers state, ping lock and merge pool must be fresh in the clone.
func TestRepository_clone_copiesEveryField(t *testing.T) {
	orig := &Repository{
		Registry:                   &Registry{},
		RepositoryName:             "repo/image",
		sourceReference:            properties.Reference{Registry: "source.example.test", Repository: "repo/image"},
		ManifestMediaTypes:         []string{ocispec.MediaTypeImageManifest},
		TagListPageSize:            1,
		ReferrerListPageSize:       2,
		TagListMaxPages:            3,
		ReferrerListMaxPages:       4,
		SkipReferrersGC:            true,
		MaxChunkSize:               5,
		SingleRequestPushThreshold: 6,
		MaxUnsafePushBytes:         7,
		mirrors:                    []mirrorRepository{{Repository: &Repository{}, pullFromMirror: "all"}},
	}
	atomic.StoreInt32(&orig.referrersState, int32(properties.ReferrersAPISupported))
	orig.singleRequestUnsupported.Store(true)
	orig.referrersPingLock.Lock()
	defer orig.referrersPingLock.Unlock()

	clone := orig.clone()

	origValue := reflect.ValueOf(orig).Elem()
	cloneValue := reflect.ValueOf(clone).Elem()
	repoType := origValue.Type()
	wantUnexported := map[string]bool{
		"mirrors":            true,
		"sourceReference":    true,
		"referrersState":     true,
		"referrersPingLock":  true,
		"referrersMergePool": true,
		// what one repository learned about the registry is not carried
		// over, like referrersState
		"singleRequestUnsupported": true,
	}
	for i := range repoType.NumField() {
		field := repoType.Field(i)
		if !field.IsExported() {
			if !wantUnexported[field.Name] {
				t.Errorf("test needs updating: new unexported field %s needs a deliberate copy-or-reset decision in clone()", field.Name)
			}
			continue
		}
		if origValue.Field(i).IsZero() {
			t.Fatalf("test needs updating: original.%s is zero, so clone cannot be checked for it", field.Name)
		}
		if cloneValue.Field(i).IsZero() {
			t.Errorf("clone() did not copy exported field %s", field.Name)
		}
	}

	if clone.Registry != orig.Registry {
		t.Error("clone().Registry should share the original pointer")
	}
	if clone.sourceReference != orig.sourceReference {
		t.Errorf("clone().sourceReference = %#v, want %#v", clone.sourceReference, orig.sourceReference)
	}
	if len(orig.mirrors) == 0 {
		t.Fatal("test needs updating: original.mirrors is empty")
	}
	if len(clone.mirrors) != len(orig.mirrors) {
		t.Errorf("len(clone().mirrors) = %d, want %d", len(clone.mirrors), len(orig.mirrors))
	}

	clone.ManifestMediaTypes[0] = "changed"
	if orig.ManifestMediaTypes[0] != ocispec.MediaTypeImageManifest {
		t.Error("clone().ManifestMediaTypes should be an independent copy")
	}

	if got := clone.loadReferrersState(); got != properties.ReferrersAPIUnknown {
		t.Errorf("clone().referrersState = %v, want %v", got, properties.ReferrersAPIUnknown)
	}
	if clone.singleRequestUnsupported.Load() {
		t.Error("clone().singleRequestUnsupported should be reset to false")
	}
	if !clone.referrersPingLock.TryLock() {
		t.Error("clone().referrersPingLock should be unlocked")
	}
	if !reflect.ValueOf(&clone.referrersMergePool).Elem().IsZero() {
		t.Error("clone().referrersMergePool should be zero")
	}
}

func TestManifestStore_ParseReference(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		want      properties.Reference
		wantErr   bool
	}{
		{
			name:      "valid tag",
			reference: "foobar",
			want: properties.Reference{
				Tag: "foobar",
			},
			wantErr: false,
		},
		{
			name:      "valid digest",
			reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: properties.Reference{
				Digest: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: false,
		},
		{
			name:      "valid tag@digest",
			reference: "foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: properties.Reference{
				Digest: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: false,
		},
		{
			name:      "invalid reference",
			reference: "invalid@reference",
			want:      properties.Reference{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &Registry{}
			repo := &Repository{Registry: reg}
			s := &manifestStore{repo: repo}
			got, err := s.ParseReference(tt.reference)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseReference() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManifestStore_generateDescriptor(t *testing.T) {
	data := []byte("test")
	dataSize := int64(len(data))
	dataDigest := digest.FromBytes(data)
	mediaType := "application/vnd.test"

	tests := []struct {
		name           string
		resp           *http.Response
		ref            properties.Reference
		httpMethod     string
		wantDescriptor ocispec.Descriptor
		wantErr        bool
	}{
		{
			name: "valid response with Content-Type and Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod: http.MethodGet,
			wantDescriptor: ocispec.Descriptor{
				MediaType: mediaType,
				Digest:    dataDigest,
				Size:      dataSize,
			},
			wantErr: false,
		},
		{
			name: "invalid Content-Type",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{"invalid content type"},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "unknown Content-Length",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: -1,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "bad Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{"not-a-digest"},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "resp with body, missing Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type": []string{mediaType},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
				Body: io.NopCloser(bytes.NewReader(data)),
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod: http.MethodGet,
			wantDescriptor: ocispec.Descriptor{
				MediaType: mediaType,
				Digest:    dataDigest,
				Size:      dataSize,
			},
			wantErr: false,
		},
		{
			name: "failed to read resp with body, missing Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type": []string{mediaType},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
				Body: &badReader{},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     dataDigest.String(),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "digest mismatch",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{string(dataDigest)},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			ref: properties.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Digest:     string(digest.FromBytes([]byte("whatever"))),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &Registry{
				MaxMetadataBytes: 1024,
			}
			s := &manifestStore{
				repo: &Repository{
					Registry: reg,
				},
			}
			got, err := s.generateDescriptor(tt.resp, tt.ref, tt.httpMethod)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateDescriptor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.wantDescriptor) {
				t.Errorf("generateDescriptor() = %v, want %v", got, tt.wantDescriptor)
			}
		})
	}
}

func Test_generateBlobDescriptor(t *testing.T) {
	data := []byte("test")
	dataSize := int64(len(data))
	dataDigest := digest.FromBytes(data)
	mediaType := "application/vnd.test"

	tests := []struct {
		name           string
		resp           *http.Response
		refDigest      digest.Digest
		wantDescriptor ocispec.Descriptor
		wantErr        bool
	}{
		{
			name: "valid response with Content-Type and Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest: dataDigest,
			wantDescriptor: ocispec.Descriptor{
				MediaType: mediaType,
				Digest:    dataDigest,
				Size:      dataSize,
			},
			wantErr: false,
		},
		{
			name: "missing Content-Type",
			resp: &http.Response{
				Header: http.Header{
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest: dataDigest,
			wantDescriptor: ocispec.Descriptor{
				MediaType: "application/octet-stream",
				Digest:    dataDigest,
				Size:      dataSize,
			},
			wantErr: false,
		},
		{
			name: "invalid Content-Type",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{"invalid content type"},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest: dataDigest,
			wantDescriptor: ocispec.Descriptor{
				MediaType: "application/octet-stream",
				Digest:    dataDigest,
				Size:      dataSize,
			},
			wantErr: false,
		},
		{
			name: "unknown Content-Length",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{dataDigest.String()},
				},
				ContentLength: -1,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest:      dataDigest,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "bad Docker-Content-Digest",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{"not-a-digest"},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest:      dataDigest,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
		{
			name: "digest mismatch",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type":          []string{mediaType},
					"Docker-Content-Digest": []string{string(dataDigest)},
				},
				ContentLength: dataSize,
				Request: &http.Request{
					Method: http.MethodGet,
					URL:    &url.URL{Path: "/test"},
				},
			},
			refDigest:      digest.FromBytes([]byte("mismatch")),
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateBlobDescriptor(tt.resp, tt.refDigest)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateBlobDescriptor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.wantDescriptor) {
				t.Errorf("generateBlobDescriptor() = %v, want %v", got, tt.wantDescriptor)
			}
		})
	}
}

// readSeekerOnly exposes Read+Seek but is not one of the stdlib built-in types
// (*bytes.Reader, *bytes.Buffer, *strings.Reader) that net/http auto-detects to
// set Request.GetBody. It stands in for a seekable, digest-addressed blob
// source (file / tar layout / CAS).
type readSeekerOnly struct {
	io.ReadSeeker
}

// readerOnly exposes only Read, so it is neither a stdlib built-in nor seekable.
type readerOnly struct {
	io.Reader
}

// readSeekCloserOnly is a seekable io.ReadCloser that is NOT a stdlib built-in.
// Wrapped in io.NopCloser it is the shape where completePushAfterInitialPost's
// "undo unwrap if the nopCloser is intended" guard reverts the request body
// back to the NopCloser (the unwrapped inner is a ReadCloser), so the Seeker is
// only visible on the unwrapped value.
type readSeekCloserOnly struct {
	io.ReadSeeker
}

func (readSeekCloserOnly) Close() error { return nil }

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Test_BlobStore_Push_GetBody asserts the body replayability contract of a
// monolithic blob push: seekable or built-in content yields a non-nil
// Request.GetBody that reproduces the full blob from the start, so net/http (on
// redirect or HTTP/2 connection recycle) or a retrying RoundTripper can resend
// the body, while non-seekable content leaves GetBody nil.
func Test_BlobStore_Push_GetBody(t *testing.T) {
	blob := []byte("hello world, a blob payload for GetBody wiring")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	tests := []struct {
		name        string
		content     func() io.Reader
		wantGetBody bool
	}{
		{
			name:        "built-in *bytes.Reader",
			content:     func() io.Reader { return bytes.NewReader(blob) },
			wantGetBody: true, // set by net/http
		},
		{
			name:        "built-in wrapped in io.NopCloser",
			content:     func() io.Reader { return io.NopCloser(bytes.NewReader(blob)) },
			wantGetBody: true, // unwrap restores net/http auto-detection
		},
		{
			name:        "seekable non-built-in",
			content:     func() io.Reader { return readSeekerOnly{bytes.NewReader(blob)} },
			wantGetBody: true, // set by the seekable-GetBody wiring
		},
		{
			name:        "seekable ReadCloser wrapped in io.NopCloser",
			content:     func() io.Reader { return io.NopCloser(readSeekCloserOnly{bytes.NewReader(blob)}) },
			wantGetBody: true, // detected on the unwrapped source despite the guard revert
		},
		{
			name:        "non-seekable",
			content:     func() io.Reader { return readerOnly{bytes.NewReader(blob)} },
			wantGetBody: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
			var putReq *http.Request
			client := &auth.Client{Client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				resp := &http.Response{Request: req, Header: http.Header{}, Body: http.NoBody}
				switch {
				case req.Method == http.MethodPost:
					resp.StatusCode = http.StatusAccepted
					resp.Header.Set("Location", "/v2/test/blobs/uploads/"+uuid)
				case req.Method == http.MethodPut:
					putReq = req
					// drain the first-attempt body as the real transport would
					if req.Body != nil {
						_, _ = io.Copy(io.Discard, req.Body)
					}
					resp.StatusCode = http.StatusCreated
					resp.Header.Set("Docker-Content-Digest", blobDesc.Digest.String())
				default:
					resp.StatusCode = http.StatusForbidden
				}
				return resp, nil
			})}}

			repo, err := NewRepository("registry.example.com/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			repo.Registry.Client = client

			if err := repo.Blobs().Push(context.Background(), blobDesc, tt.content()); err != nil {
				t.Fatalf("Blobs.Push() error = %v", err)
			}
			if putReq == nil {
				t.Fatal("no PUT request captured")
			}
			if putReq.ContentLength != blobDesc.Size {
				t.Errorf("ContentLength = %d, want %d", putReq.ContentLength, blobDesc.Size)
			}
			if gotGetBody := putReq.GetBody != nil; gotGetBody != tt.wantGetBody {
				t.Fatalf("GetBody set = %v, want %v", gotGetBody, tt.wantGetBody)
			}
			if !tt.wantGetBody {
				return
			}
			// GetBody must rewind to reproduce the full blob, even though the
			// first-attempt body was already drained above.
			rc, err := putReq.GetBody()
			if err != nil {
				t.Fatalf("GetBody() error = %v", err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read rewound body error = %v", err)
			}
			if !bytes.Equal(got, blob) {
				t.Errorf("rewound body = %q, want %q", got, blob)
			}
		})
	}
}

// Test_BlobStore_Push_RetriesSeekableBody is an end-to-end check that the
// seekable-GetBody wiring lets a retrying RoundTripper resend the body after
// the connection dies mid-upload. The first attempt reads part of the PUT body
// and then resets the TCP connection (RST via SetLinger(0)+Close), which
// net/http does not retry on its own; the retry transport rewinds via
// Request.GetBody over a fresh connection. The source reader is seekable but
// not a built-in, so GetBody comes from the fix, not net/http.
//
// It runs over both a non-closer seekable reader and a real *os.File: the file
// case is an io.ReadCloser, so GetBody must shield it from the transport's
// per-attempt Body.Close() or the retry would seek a closed file.
func Test_BlobStore_Push_RetriesSeekableBody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a reset surfaces as WSAECONNRESET on windows, which is not retried")
	}
	blob := []byte("hello world, a seekable retryable blob payload")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	tests := []struct {
		name    string
		content func(t *testing.T) io.Reader
	}{
		{
			name:    "non-closer seekable reader",
			content: func(*testing.T) io.Reader { return readSeekerOnly{bytes.NewReader(blob)} },
		},
		{
			// an *os.File is a seekable io.ReadCloser, matching what the oci and
			// file stores return from Fetch; the transport closes it after each
			// attempt, so a replay must not seek the closed file.
			name: "file-backed read closer",
			content: func(t *testing.T) io.Reader {
				f, err := os.CreateTemp(t.TempDir(), "blob")
				if err != nil {
					t.Fatalf("CreateTemp() error = %v", err)
				}
				if _, err := f.Write(blob); err != nil {
					t.Fatalf("write temp blob error = %v", err)
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					t.Fatalf("seek temp blob error = %v", err)
				}
				t.Cleanup(func() { f.Close() })
				return f
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
			// the two PUT attempts are served on different connections, hence the atomic
			var attempts atomic.Int32
			var gotBlob []byte
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					w.Header().Set("Location", "/v2/test/blobs/uploads/"+uuid)
					w.WriteHeader(http.StatusAccepted)
				case r.Method == http.MethodPut:
					if attempts.Add(1) == 1 {
						// read part of the body, then reset the fresh connection
						// instead of answering, which net/http does not retry on its own
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
						return
					}
					gotBlob, _ = io.ReadAll(r.Body)
					w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
					w.WriteHeader(http.StatusCreated)
				default:
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			defer ts.Close()
			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("invalid test http server: %v", err)
			}

			repo, err := NewRepository(uri.Host + "/test")
			if err != nil {
				t.Fatalf("NewRepository() error = %v", err)
			}
			repo.Registry.PlainHTTP = true
			repo.Registry.Client = &auth.Client{Client: &http.Client{Transport: retry.NewTransport(http.DefaultTransport)}}

			if err := repo.Blobs().Push(context.Background(), blobDesc, tt.content(t)); err != nil {
				t.Fatalf("Blobs.Push() error = %v", err)
			}
			if n := attempts.Load(); n != 2 {
				t.Errorf("PUT attempts = %d, want 2", n)
			}
			if !bytes.Equal(gotBlob, blob) {
				t.Errorf("received blob = %q, want %q", gotBlob, blob)
			}
		})
	}
}

// Test_BlobStore_Push_GetBody_ConcurrentReplay verifies that, for a ReaderAt
// source (e.g. the *os.File the oci/file stores return), the request body and
// every GetBody replay are independent readers over the shared source. A failed
// attempt still reading must not race a retry's replay. Meaningful under -race:
// the previous single-shared-seeker wiring would race here.
func Test_BlobStore_Push_GetBody_ConcurrentReplay(t *testing.T) {
	blob := []byte("hello world, a ReaderAt-backed seekable blob payload")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	f, err := os.CreateTemp(t.TempDir(), "blob")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer f.Close()
	if _, err := f.Write(blob); err != nil {
		t.Fatalf("write temp blob error = %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek temp blob error = %v", err)
	}

	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	var putReq *http.Request
	client := &auth.Client{Client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp := &http.Response{Request: req, Header: http.Header{}, Body: http.NoBody}
		switch {
		case req.Method == http.MethodPost:
			resp.StatusCode = http.StatusAccepted
			resp.Header.Set("Location", "/v2/test/blobs/uploads/"+uuid)
		case req.Method == http.MethodPut:
			putReq = req
			resp.StatusCode = http.StatusCreated
			resp.Header.Set("Docker-Content-Digest", blobDesc.Digest.String())
		default:
			resp.StatusCode = http.StatusForbidden
		}
		return resp, nil
	})}}

	repo, err := NewRepository("registry.example.com/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	repo.Registry.Client = client

	if err := repo.Blobs().Push(context.Background(), blobDesc, f); err != nil {
		t.Fatalf("Blobs.Push() error = %v", err)
	}
	if putReq == nil || putReq.GetBody == nil || putReq.Body == nil {
		t.Fatal("GetBody/Body not set for a ReaderAt source")
	}

	// Read the first-attempt body alongside several replays, concurrently.
	readers := []io.Reader{putReq.Body}
	for range 4 {
		rc, err := putReq.GetBody()
		if err != nil {
			t.Fatalf("GetBody() error = %v", err)
		}
		defer rc.Close()
		readers = append(readers, rc)
	}
	var wg sync.WaitGroup
	for _, r := range readers {
		wg.Add(1)
		go func(r io.Reader) {
			defer wg.Done()
			got, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("read error = %v", err)
				return
			}
			if !bytes.Equal(got, blob) {
				t.Errorf("replay = %q, want %q", got, blob)
			}
		}(r)
	}
	wg.Wait()
}
