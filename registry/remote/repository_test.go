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
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/interfaces"
	"oras.land/oras-go/v2/internal/spec"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
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
			if got.Reference.String() != tt.reference {
				t.Errorf("NewRepository() got = %v, want %v", got.Reference.String(), tt.reference)
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
	ctx := context.Background()

	err = repo.Mount(ctx, blobDesc, "test", nil)
	if err != nil {
		t.Fatalf("Repository.Push() error = %v", err)
	}
	if gotMount != 1 {
		t.Errorf("did not get expected mount request")
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true

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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test force attempt Referrers
	// remote server supports Referrers, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test force attempt tag schema
	// request tag schema but got 404, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.ReferrerListPageSize = 2
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}

	// test force attempt Referrers
	// remote server does not support Referrers, should return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test force attempt tag schema
	// should request tag schema
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		if !reflect.DeepEqual(got, referrers) {
			t.Errorf("Repository.Referrers() = %v, want %v", got, referrers)
		}
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}

	// test force attempt tag schema
	// tag schema referrers not found, should be no error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}

	// test force attempt Referrers
	// Referrers returns error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test force attempt tag schema
	// Referrers returns error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = nil, wantErr %v", true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	// repository not found, should return error
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}

	// test force attempt Referrers
	// repository not found, should return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err == nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test force attempt tag schema
	// repository not found, but should not return error
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []ocispec.Descriptor) error {
		return nil
	}); err != nil {
		t.Errorf("Repository.Referrers() error = %v, wantErr %v", err, nil)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true

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
					w.Header().Set("Docker-Content-Digest", desc.Digest.String())
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
			repo.PlainHTTP = true
			ctx := context.Background()
			if err := repo.Push(ctx, desc, bytes.NewReader(data)); err != nil {
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
			repo.PlainHTTP = true
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
			repo.PlainHTTP = true
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
			repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
		repo.PlainHTTP = true
		store := repo.Blobs()
		ctx := context.Background()

		if _, err = store.Fetch(ctx, blobDesc); err == nil {
			t.Error("Blobs.Fetch() error = nil, want error")
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
		repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
		repo.PlainHTTP = true
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
		repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
			if d, err = reference.Digest(); err != nil {
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
	var ms interface{} = &manifestStore{}
	if _, ok := ms.(interfaces.ReferenceParser); !ok {
		t.Error("&manifestStore{} does not conform interfaces.ReferenceParser")
	}
}

func TestRepositoryMounterInterface(t *testing.T) {
	var r interface{} = &Repository{}
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
		repo.PlainHTTP = true
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
		repo.PlainHTTP = true
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
		repo.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
		}
	})

	t.Run("fail with mismatching Content-Length", func(t *testing.T) {
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
				if _, err := w.Write([]byte("random")); err != nil {
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
		repo.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
		}
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
		repo.PlainHTTP = true
		store := repo.Manifests()
		ctx := context.Background()

		_, err = store.Fetch(ctx, manifestDesc)
		if err == nil {
			t.Error("Manifests.Fetch() error = nil, wantErr = true")
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = repo.Push(ctx, artifactDesc, bytes.NewReader(artifactJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test pushing image manifest with subject
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = repo.Push(ctx, manifestDesc, bytes.NewReader(manifestJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test pushing image index with subject
	err = repo.Push(ctx, indexDesc, bytes.NewReader(indexJSON))
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	repo.PlainHTTP = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	repo.SkipReferrersGC = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()
	// test deleting artifact with subject
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !artifactDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", artifactDeleted, true)
	}

	// test deleting manifest with subject
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}
	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}

	// test deleting index with subject
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	repo.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	repo.SkipReferrersGC = true
	store := repo.Manifests()
	ctx := context.Background()

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	repo.SkipReferrersGC = true
	store = repo.Manifests()
	ctx = context.Background()

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	store := repo.Manifests()
	ctx := context.Background()
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	store = repo.Manifests()
	ctx = context.Background()
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = store.Delete(ctx, artifactDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
		repo.PlainHTTP = true
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
		repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = repo.PushReference(ctx, artifactDesc, bytes.NewReader(artifactJSON), artifactRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, artifactJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(artifactJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test pushing image manifest with subject
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	err = repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestJSON), manifestRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, manifestJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(manifestJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test pushing image index with subject
	err = repo.PushReference(ctx, indexDesc, bytes.NewReader(indexJSON), indexRef)
	if err != nil {
		t.Fatalf("Manifests.Push() error = %v", err)
	}
	if !bytes.Equal(gotManifest, indexJSON) {
		t.Errorf("Manifests.Push() = %v, want %v", string(gotManifest), string(indexJSON))
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	repo.PlainHTTP = true

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
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
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
}

func Test_ManifestStore_generateDescriptorWithVariousDockerContentDigestHeaders(t *testing.T) {
	reference := registry.Reference{
		Registry:   "eastern.haan.com",
		Reference:  "<calculate>",
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
	repo.Client = &auth.Client{
		Client: &http.Client{
			Transport: &testTransport{
				proxyHost:           uri.Host,
				underlyingTransport: http.DefaultTransport,
				mockHost:            testRegistry,
			},
		},
		Cache: auth.NewCache(),
	}
	repo.PlainHTTP = true
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
	repo.Client = &auth.Client{
		Client: &http.Client{
			Transport: &testTransport{
				proxyHost:           uri.Host,
				underlyingTransport: transport,
				mockHost:            testRegistry,
			},
		},
		Cache: auth.NewCache(),
	}
	repo.PlainHTTP = false
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
	repo.PlainHTTP = true
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

func TestRepository_ParseReference(t *testing.T) {
	type args struct {
		reference string
	}
	tests := []struct {
		name    string
		repoRef registry.Reference
		args    args
		want    registry.Reference
		wantErr error
	}{
		{
			name: "parse tag",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "foobar",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "foobar",
			},
			wantErr: nil,
		},
		{
			name: "parse digest",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse tag@digest",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN tag",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world:foobar",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "foobar",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN digest",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "parse FQDN tag@digest",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: nil,
		},
		{
			name: "empty reference",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing repository",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "myregistry.example.com:hello-world",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "registry mismatch",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "myregistry.example.com/hello-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "repository mismatch",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/goodbye-world:foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "digest posing as a tag",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com:5000/hello-world:sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference after the at sign",
			repoRef: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
			},
			args: args{
				reference: "registry.example.com/hello-world@",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name: "missing reference after the colon",
			repoRef: registry.Reference{
				Registry: "localhost",
			},
			args: args{
				reference: "localhost:5000/hello:",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "zero-size tag, zero-size digest",
			repoRef: registry.Reference{},
			args: args{
				reference: "localhost:5000/hello:@",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "zero-size tag with valid digest",
			repoRef: registry.Reference{},
			args: args{
				reference: "localhost:5000/hello:@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "valid tag with zero-size digest",
			repoRef: registry.Reference{},
			args: args{
				reference: "localhost:5000/hello:foobar@",
			},
			want:    registry.Reference{},
			wantErr: errdef.ErrInvalidReference,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Repository{
				Reference: tt.repoRef,
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
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}

	// valid first time set
	if err := repo.SetReferrersCapability(true); err != nil {
		t.Errorf("Repository.SetReferrersCapability() error = %v", err)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// invalid second time set, state should be no changed
	if err := repo.SetReferrersCapability(false); !errors.Is(err, ErrReferrersCapabilityAlreadySet) {
		t.Errorf("Repository.SetReferrersCapability() error = %v, wantErr %v", err, ErrReferrersCapabilityAlreadySet)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	wantIndexDesc := content.NewDescriptorFromBytes(wantIndex.MediaType, wantIndexJSON)

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
	wantEmptyIndexDesc := content.NewDescriptorFromBytes(wantEmptyIndex.MediaType, wantEmptyIndexJSON)

	tests := []struct {
		name      string
		manifests []ocispec.Descriptor
		wantDesc  ocispec.Descriptor
		wantBytes []byte
		wantErr   bool
	}{
		{
			name:      "non-empty referrers list",
			manifests: referrers,
			wantDesc:  wantIndexDesc,
			wantBytes: wantIndexJSON,
			wantErr:   false,
		},
		{
			name:      "nil referrers list",
			manifests: nil,
			wantDesc:  wantEmptyIndexDesc,
			wantBytes: wantEmptyIndexJSON,
			wantErr:   false,
		},
		{
			name:      "empty referrers list",
			manifests: nil,
			wantDesc:  wantEmptyIndexDesc,
			wantBytes: wantEmptyIndexJSON,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := generateIndex(tt.manifests)
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
		repo.PlainHTTP = true

		// 1st call
		if state := repo.loadReferrersState(); state != referrersStateUnknown {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
		}
		got, err := repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != true {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
		}
		if state := repo.loadReferrersState(); state != referrersStateSupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}

		// 2nd call
		if state := repo.loadReferrersState(); state != referrersStateSupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
		}
		got, err = repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != true {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
		}
		if state := repo.loadReferrersState(); state != referrersStateSupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
		repo.PlainHTTP = true

		// 1st call
		if state := repo.loadReferrersState(); state != referrersStateUnknown {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
		}
		got, err := repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != false {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
		}
		if state := repo.loadReferrersState(); state != referrersStateUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
		}
		if count != 1 {
			t.Errorf("count(Repository.pingReferrers()) = %v, want %v", count, 1)
		}

		// 2nd call
		if state := repo.loadReferrersState(); state != referrersStateUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
		}
		got, err = repo.pingReferrers(ctx)
		if err != nil {
			t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
		}
		if got != false {
			t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
		}
		if state := repo.loadReferrersState(); state != referrersStateUnsupported {
			t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
		repo.PlainHTTP = true

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
	repo.PlainHTTP = true
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	if _, err = repo.pingReferrers(ctx); err == nil {
		t.Fatalf("Repository.pingReferrers() error = %v, wantErr %v", err, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}

	// test referrers state supported
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(true)
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}
	got, err := repo.pingReferrers(ctx)
	if err != nil {
		t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
	}
	if got != true {
		t.Errorf("Repository.pingReferrers() = %v, want %v", got, true)
	}
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
	}

	// test referrers state unsupported
	repo, err = NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	repo.SetReferrersCapability(false)
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
	}
	got, err = repo.pingReferrers(ctx)
	if err != nil {
		t.Errorf("Repository.pingReferrers() error = %v, wantErr %v", err, nil)
	}
	if got != false {
		t.Errorf("Repository.pingReferrers() = %v, want %v", got, false)
	}
	if state := repo.loadReferrersState(); state != referrersStateUnsupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnsupported)
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
	repo.PlainHTTP = true

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)

	if state := repo.loadReferrersState(); state != referrersStateUnknown {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateUnknown)
	}
	for i := 0; i < concurrency; i++ {
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
	if state := repo.loadReferrersState(); state != referrersStateSupported {
		t.Errorf("Repository.loadReferrersState() = %v, want %v", state, referrersStateSupported)
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
	repo.HandleWarning = func(warning Warning) {
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

func TestRepository_newRepositoryWithOptions(t *testing.T) {
	t.Run("valid reference and options", func(t *testing.T) {
		ref := registry.Reference{
			Registry:   "registry.example.com",
			Repository: "test",
			Reference:  "latest",
		}
		opts := &RepositoryOptions{
			PlainHTTP: true,
		}
		repo, err := newRepositoryWithOptions(ref, opts)
		if err != nil {
			t.Fatalf("newRepositoryWithOptions() error = %v", err)
		}
		if repo.PlainHTTP != opts.PlainHTTP {
			t.Errorf("Repository.PlainHTTP = %v, want %v", repo.PlainHTTP, opts.PlainHTTP)
		}
		if !reflect.DeepEqual(repo.Reference, ref) {
			t.Errorf("Repository.Reference = %v, want %v", repo.Reference, ref)
		}
	})

	t.Run("invalid reference", func(t *testing.T) {
		ref := registry.Reference{}
		if _, err := newRepositoryWithOptions(ref, nil); err == nil {
			t.Error("newRepositoryWithOptions() error = nil, wantErr")
		}
	})
}

func TestRepository_clone(t *testing.T) {
	repo, err := NewRepository("localhost:1234/repo/image")
	if err != nil {
		t.Fatalf("invalid repository: %v", err)
	}

	crepo := repo.clone()

	if repo.Reference != crepo.Reference {
		t.Fatal("references should be the same")
	}

	if !reflect.DeepEqual(&repo.referrersPingLock, &crepo.referrersPingLock) {
		t.Fatal("referrersPingLock should be different")
	}

	if !reflect.DeepEqual(&repo.referrersMergePool, &crepo.referrersMergePool) {
		t.Fatal("referrersMergePool should be different")
	}
}

func TestManifestStore_ParseReference(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		want      registry.Reference
		wantErr   bool
	}{
		{
			name:      "valid tag",
			reference: "foobar",
			want: registry.Reference{
				Reference: "foobar",
			},
			wantErr: false,
		},
		{
			name:      "valid digest",
			reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: registry.Reference{
				Reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: false,
		},
		{
			name:      "valid tag@digest",
			reference: "foobar@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: registry.Reference{
				Reference: "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
			wantErr: false,
		},
		{
			name:      "invalid reference",
			reference: "invalid@reference",
			want:      registry.Reference{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{}
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
		ref            registry.Reference
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  dataDigest.String(),
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
			ref: registry.Reference{
				Registry:   "registry.example.com",
				Repository: "hello-world",
				Reference:  string(digest.FromBytes([]byte("whatever"))),
			},
			httpMethod:     http.MethodGet,
			wantDescriptor: ocispec.Descriptor{},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &manifestStore{
				repo: &Repository{
					MaxMetadataBytes: 1024,
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
