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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
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
	t.Run("Ping success", func(t *testing.T) {
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
	})

	t.Run("Ping failed for server error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/v2/" {
				t.Errorf("unexpected access: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			w.WriteHeader(http.StatusInternalServerError)
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
		if err := reg.Ping(ctx); err == nil {
			t.Error("Registry.Ping() error = nil, wantErr = true")
		}
	})

	t.Run("Ping failed for connection error", func(t *testing.T) {
		reg, err := NewRegistry("localhost:9876")
		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}

		ctx := context.Background()
		if err := reg.Ping(ctx); err == nil {
			t.Error("Registry.Ping() error = nil, wantErr = true")
		}
	})
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
	if err := reg.Repositories(ctx, "", func(got []string) error {
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
	reg.SkipReferrersGC = true
	reg.RepositoryListPageSize = 50
	reg.TagListPageSize = 100
	reg.ReferrerListPageSize = 10
	reg.MaxMetadataBytes = 8 * 1024 * 1024

	ctx := context.Background()
	got, err := reg.Repository(ctx, "hello-world")
	if err != nil {
		t.Fatalf("Registry.Repository() error = %v", err)
	}
	reg.Reference.Repository = "hello-world"
	want := (*Repository)(&reg.RepositoryOptions)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Registry.Repository() = %v, want %v", got, want)
	}
}

// Testing `last` parameter for Repositories list
func TestRegistry_Repositories_WithLastParam(t *testing.T) {
	repoSet := strings.Split("abcdefghijklmnopqrstuvwxyz", "")
	var offset int
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
		last := q.Get("last")
		if last != "" {
			offset = indexOf(last, repoSet) + 1
		}
		var repos []string
		switch q.Get("test") {
		case "foo":
			repos = repoSet[offset : offset+n]
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/_catalog?n=4&last=v&test=bar>; rel="next"`, ts.URL))
		case "bar":
			repos = repoSet[offset : offset+n]
		default:
			repos = repoSet[offset : offset+n]
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/_catalog?n=4&last=r&test=foo>; rel="next"`, ts.URL))
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
	last := "n"
	startInd := indexOf(last, repoSet) + 1

	ctx := context.Background()
	if err := reg.Repositories(ctx, last, func(got []string) error {
		want := repoSet[startInd : startInd+reg.RepositoryListPageSize]
		startInd += reg.RepositoryListPageSize
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Registry.Repositories() = %v, want %v", got, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("Registry.Repositories() error = %v", err)
	}
}

func TestRegistry_do(t *testing.T) {
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
	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatal("NewRegistry() error =", err)
	}
	req, err := http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		t.Fatal("failed to create test request:", err)
	}
	resp, err := reg.do(req)
	if err != nil {
		t.Fatal("Registry.do() error =", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Registry.do() status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if got := len(resp.Header["Warning"]); got != 6 {
		t.Errorf("Registry.do() warning header len = %v, want %v", got, 6)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal("io.ReadAll() error =", err)
	}
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("Registry.do() = %v, want %v", got, data)
	}

	// test do() with HandleWarning
	reg, err = NewRegistry(uri.Host)
	if err != nil {
		t.Fatal("NewRegistry() error =", err)
	}
	var gotWarnings []Warning
	reg.HandleWarning = func(warning Warning) {
		gotWarnings = append(gotWarnings, warning)
	}

	req, err = http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		t.Fatal("failed to create test request:", err)
	}
	resp, err = reg.do(req)
	if err != nil {
		t.Fatal("Registry.do() error =", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Registry.do() status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if got := len(resp.Header["Warning"]); got != 6 {
		t.Errorf("Registry.do() warning header len = %v, want %v", got, 6)
	}
	got, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("Registry.do() = %v, want %v", got, data)
	}
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("Registry.do() = %v, want %v", got, data)
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
		t.Errorf("Registry.do() = %v, want %v", gotWarnings, wantWarnings)
	}
}

func TestNewRegistry(t *testing.T) {
	tests := []struct {
		name    string
		regName string
		wantErr bool
	}{
		{
			name:    "Valid registry name",
			regName: "localhost:5000",
			wantErr: false,
		},
		{
			name:    "Invalid registry name",
			regName: "invalid registry name",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRegistry(tt.regName)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRegistry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Errorf("NewRegistry() = %v, want non-nil", got)
			}
		})
	}
}

// indexOf returns the index of an element within a slice
func indexOf(element string, data []string) int {
	for ind, val := range data {
		if element == val {
			return ind
		}
	}
	return -1 //not found.
}
