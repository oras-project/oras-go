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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
)

func TestRegistryInterface(t *testing.T) {
	var reg interface{} = &Registry{}
	if _, ok := reg.(registry.Registry); !ok {
		t.Error("&Registry{} does not conform registry.Registry")
	}
}

func TestRegistry_TLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.Client = ts.Client()

	ctx := context.Background()
	if err := reg.Ping(ctx); err != nil {
		t.Errorf("Registry.Ping() error = %v", err)
	}
}

func TestRegistry_Ping(t *testing.T) {
	v2Implemented := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if v2Implemented {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.PlainHTTP = true

	ctx := context.Background()
	if err := reg.Ping(ctx); err != nil {
		t.Errorf("Registry.Ping() error = %v", err)
	}

	v2Implemented = false
	if err := reg.Ping(ctx); err == nil {
		t.Errorf("Registry.Ping() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestRegistry_Repositories(t *testing.T) {
	repoSet := [][]string{
		{"the", "quick", "brown", "fox"},
		{"jumps", "over", "the", "lazy"},
		{"dog"},
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/_catalog" {
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
		var repos []string
		switch q.Get("test") {
		case "foo":
			repos = repoSet[1]
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/_catalog?n=4&test=bar>; rel="next"`, ts.URL))
		case "bar":
			repos = repoSet[2]
		default:
			repos = repoSet[0]
			w.Header().Set("Link", `</v2/_catalog?n=4&test=foo>; rel="next"`)
		}
		result := struct {
			Repositories []string `json:"repositories"`
		}{
			Repositories: repos,
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

	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.PlainHTTP = true
	reg.RepositoryListPageSize = 4

	ctx := context.Background()
	index := 0
	if err := reg.Repositories(ctx, func(got []string) error {
		if index > 2 {
			t.Fatalf("out of index bound: %d", index)
		}
		repos := repoSet[index]
		index++
		if !reflect.DeepEqual(got, repos) {
			t.Errorf("Registry.Repositories() = %v, want %v", got, repos)
		}
		return nil
	}); err != nil {
		t.Fatalf("Registry.Repositories() error = %v", err)
	}
}

func TestRegistry_Repository(t *testing.T) {
	reg, err := NewRegistry("localhost:5000")
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.PlainHTTP = true
	reg.RepositoryListPageSize = 50
	reg.TagListPageSize = 100
	reg.ReferrerListPageSize = 10
	reg.MaxMetadataBytes = 8 * 1024 * 1024

	ctx := context.Background()
	want := &Repository{}
	*want = Repository(reg.RepositoryOptions)
	want.Reference.Repository = "hello-world"
	got, err := reg.Repository(ctx, "hello-world")
	if err != nil {
		t.Fatalf("Registry.Repository() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Registry.Repository() = %v, want %v", got, want)
	}
}
