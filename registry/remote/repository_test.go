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
	"testing"

	"github.com/opencontainers/distribution-spec/specs-go/v1/extensions"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestRepositoryInterface(t *testing.T) {
	var repo interface{} = &Repository{}
	if _, ok := repo.(registry.Repository); !ok {
		t.Error("&Repository{} does not conform registry.Repository")
	}
	if _, ok := repo.(oras.GraphTarget); !ok {
		t.Error("&Repository{} does not conform oras.GraphTarget")
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
	blobDeleted := false
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	indexDeleted := false
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
		case "/v2/test/manifests/" + indexDesc.Digest.String():
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

func TestRepository_PushTag(t *testing.T) {
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

	err = repo.PushReference(ctx, blobDesc, bytes.NewReader(blob), ref)
	if err == nil {
		t.Fatalf("Repository.PushReference() error = %v, wantErr %v", err, true)
	}
	if gotIndex != nil {
		t.Errorf("Repository.PushReference() = %v, want %v", gotIndex, nil)
	}

	gotIndex = nil
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

func TestRepository_TagReference(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	src := "foobar"
	dst := "myTag"
	var gotIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() || r.URL.Path == "/v2/test/manifests/"+src):
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
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+dst:
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

	// test with manifest tag
	err = repo.TagReference(ctx, src, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest digest
	err = repo.TagReference(ctx, indexDesc.Digest.String(), dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest tag@digest
	tagDigestRef := src + "@" + indexDesc.Digest.String()
	err = repo.TagReference(ctx, tagDigestRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest FQDN
	fqdnRef := repoName + ":" + tagDigestRef
	err = repo.TagReference(ctx, fqdnRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
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
	referrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oras/artifacts/referrers"
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
		w.Header().Set("ORAS-Api-Version", "oras/1.0")
		var referrers []artifactspec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			if q.Get("digest") != manifestDesc.Digest.String() {
				t.Errorf("digest not provided or mismatch: %s %q", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := struct {
			Referrers []artifactspec.Descriptor `json:"referrers"`
		}{
			Referrers: referrers,
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
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	got, err := repo.Predecessors(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Repository.Predecessors() error = %v", err)
	}
	var want []ocispec.Descriptor
	for _, referrers := range referrerSet {
		for _, referrer := range referrers {
			want = append(want, descriptor.ArtifactToOCI(referrer))
		}
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
	referrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oras/artifacts/referrers"
		if r.Method != http.MethodGet || r.URL.Path != path {
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
		w.Header().Set("ORAS-Api-Version", "oras/1.0")
		var referrers []artifactspec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			if q.Get("digest") != manifestDesc.Digest.String() {
				t.Errorf("digest not provided or mismatch: %s %q", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := struct {
			Referrers []artifactspec.Descriptor `json:"referrers"`
		}{
			Referrers: referrers,
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
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []artifactspec.Descriptor) error {
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
}

func TestRepository_Referrers_Incompatible(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oras/artifacts/referrers"
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ORAS-Api-Version", "oras/2.0")
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
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []artifactspec.Descriptor) error {
		return nil
	}); err == nil {
		t.Error("Repository.Referrers() incompatible version not rejected")
	}
}

func Test_verifyOrasApiVersion(t *testing.T) {
	params := []struct {
		name       string
		version    string
		compatible bool
	}{
		{
			name:       "exact",
			version:    "oras/1.0",
			compatible: true,
		},
		{
			name:       "major same, minor different",
			version:    "oras/1.11",
			compatible: true,
		},
		{
			name:       "major different",
			version:    "oras/2.0",
			compatible: false,
		},
		{
			name:       "invalid prefix",
			version:    "*oras/1.0",
			compatible: false,
		},
		{
			name:       "invalid minor version",
			version:    "oras/1.01",
			compatible: false,
		},
		{
			name:       "not dot",
			version:    "oras/1#0",
			compatible: false,
		},
		{
			name:       "no version",
			version:    "",
			compatible: false,
		},
	}

	for _, tt := range params {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tt.version != "" {
				resp.Header.Set("ORAS-Api-Version", tt.version)
			}
			err := verifyOrasApiVersion(resp)
			if (err == nil) != tt.compatible {
				t.Errorf("verifyOrasApiVersion() compatible = %v, want = %v", err == nil, tt.compatible)
			}
		})
	}
}

func TestRepository_Referrers_Fallback(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/oras/artifacts/v1/test/manifests/" + manifestDesc.Digest.String() + "/referrers"
		if r.Method != http.MethodGet || r.URL.Path != path {
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
		var referrers []artifactspec.Descriptor
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
		result := struct {
			References []artifactspec.Descriptor `json:"references"`
		}{
			References: referrers,
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
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "", func(got []artifactspec.Descriptor) error {
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
}

func TestRepository_Referrers_ServerFiltering(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	referrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oras/artifacts/referrers"
		if r.Method != http.MethodGet || r.URL.Path != path {
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
		w.Header().Set("ORAS-Api-Version", "oras/1.0")
		var referrers []artifactspec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			if q.Get("digest") != manifestDesc.Digest.String() {
				t.Errorf("digest not provided or mismatch: %s %q", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := struct {
			Referrers []artifactspec.Descriptor `json:"referrers"`
		}{
			Referrers: referrers,
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
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []artifactspec.Descriptor) error {
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
	referrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         2,
				Digest:       digest.FromString("2"),
				ArtifactType: "application/vnd.foo",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         4,
				Digest:       digest.FromString("4"),
				ArtifactType: "application/vnd.bar",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         5,
				Digest:       digest.FromString("5"),
				ArtifactType: "application/vnd.baz",
			},
		},
	}
	filteredReferrerSet := [][]artifactspec.Descriptor{
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         1,
				Digest:       digest.FromString("1"),
				ArtifactType: "application/vnd.test",
			},
		},
		{
			{
				MediaType:    artifactspec.MediaTypeArtifactManifest,
				Size:         3,
				Digest:       digest.FromString("3"),
				ArtifactType: "application/vnd.test",
			},
		},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oras/artifacts/referrers"
		if r.Method != http.MethodGet || r.URL.Path != path {
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
		w.Header().Set("ORAS-Api-Version", "oras/1.0")
		var referrers []artifactspec.Descriptor
		switch q.Get("test") {
		case "foo":
			referrers = referrerSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?n=2&test=bar>; rel="next"`, ts.URL, path))
		case "bar":
			referrers = referrerSet[2]
		default:
			if q.Get("digest") != manifestDesc.Digest.String() {
				t.Errorf("digest not provided or mismatch: %s %q", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			referrers = referrerSet[0]
			w.Header().Set("Link", fmt.Sprintf(`<%s?n=2&test=foo>; rel="next"`, path))
		}
		result := struct {
			Referrers []artifactspec.Descriptor `json:"referrers"`
		}{
			Referrers: referrers,
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
	repo.ReferrerListPageSize = 2

	ctx := context.Background()
	index := 0
	if err := repo.Referrers(ctx, manifestDesc, "application/vnd.test", func(got []artifactspec.Descriptor) error {
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

func Test_filterReferrers(t *testing.T) {
	refs := []artifactspec.Descriptor{
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         2,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.foo",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.bar",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("5"),
			ArtifactType: "application/vnd.baz",
		},
	}
	got := filterReferrers(refs, "application/vnd.test")
	want := []artifactspec.Descriptor{
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterReferrers() = %v, want %v", got, want)
	}
}

func Test_filterReferrers_allMatch(t *testing.T) {
	refs := []artifactspec.Descriptor{
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    artifactspec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.test",
		},
	}
	got := filterReferrers(refs, "application/vnd.test")
	if !reflect.DeepEqual(got, refs) {
		t.Errorf("filterReferrers() = %v, want %v", got, refs)
	}
}

func TestRepository_DiscoverExtensions(t *testing.T) {
	extList := extensions.ExtensionList{
		Extensions: []extensions.Extension{
			{
				Name:        "foo.bar",
				URL:         "https://example.com",
				Description: "Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
				Endpoints:   []string{"_foo/bar", "_foo/baz"},
			},
			{
				Name:        "cncf.oras.referrers",
				URL:         "https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md",
				Description: "ORAS referrers listing API",
				Endpoints:   []string{"_oras/artifacts/referrers"},
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/test/_oci/ext/discover"
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("unexpected access: %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(extList); err != nil {
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

	got, err := repo.DiscoverExtensions(ctx)
	if err != nil {
		t.Errorf("Repository.DiscoverExtentions() error = %v", err)
	}
	if !reflect.DeepEqual(got, extList.Extensions) {
		t.Errorf("Repository.DiscoverExtentions(): got %v, want %v", got, extList)
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

func Test_ManifestStore_Fetch(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
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
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + manifestDesc.Digest.String():
			manifestDeleted = true
			// no "Docker-Content-Digest" header for manifest deletion
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
	store := repo.Manifests()
	ctx := context.Background()

	err = store.Delete(ctx, manifestDesc)
	if err != nil {
		t.Fatalf("Manifests.Delete() error = %v", err)
	}
	if !manifestDeleted {
		t.Errorf("Manifests.Delete() = %v, want %v", manifestDeleted, true)
	}

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

func Test_ManifestStore_Resolve(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ref := "foobar"
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
