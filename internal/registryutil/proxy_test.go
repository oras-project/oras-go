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

package registryutil_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/registryutil"
	"oras.land/oras-go/v2/registry/remote"
)

func TestProxy_FetchReference(t *testing.T) {
	content := []byte(`{"manifests":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"
	// prepare repository server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead && r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + desc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, desc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", desc.MediaType)
			w.Header().Set("Docker-Content-Digest", desc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(desc.Size)))
			if r.Method == http.MethodGet {
				if _, err := w.Write(content); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
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
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true

	s := registryutil.NewProxy(repo, cas.NewMemory())
	ctx := context.Background()

	// first FetchReference
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	gotDesc, rc, err := s.FetchReference(ctx, ref)
	if err != nil {
		t.Fatal("Proxy.FetchReference() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Proxy.FetchReference() = %v, want %v", gotDesc, desc)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Proxy.FetchReference().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Proxy.FetchReference().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Proxy.FetchReference() = %v, want %v", got, content)
	}

	// the subsequent fetch should not touch base CAS
	// nil base will generate panic if the base CAS is touched
	s.ReadOnlyStorage = nil

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	rc, err = s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Proxy.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Proxy.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Proxy.Fetch() = %v, want %v", got, content)
	}

	// repeated FetchReference should succeed
	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	gotDesc, rc, err = s.FetchReference(ctx, ref)
	if err != nil {
		t.Fatal("Proxy.FetchReference() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Proxy.FetchReference() = %v, want %v", gotDesc, desc)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Proxy.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Proxy.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Proxy.Fetch() = %v, want %v", got, content)
	}
}
