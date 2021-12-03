package distribution

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

	"oras.land/oras-go/v2/registry"
)

func TestRegistryInterface(t *testing.T) {
	var reg interface{} = &Registry{}
	if _, ok := reg.(registry.Registry); !ok {
		t.Error("&Registry{} does not conform registry.Registry")
	}
}

func TestRegistry_TLS(t *testing.T) {
	repos := []string{"the", "quick", "brown", "fox", "jumps", "over", "the", "lazy", "dog"}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected access: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
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
		t.Fatalf("invalid test http server: %s", err)
	}

	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.Client = ts.Client()

	ctx := context.Background()
	if err := reg.Repositories(ctx, func(got []string) error {
		if !reflect.DeepEqual(got, repos) {
			t.Errorf("Registry.Repositories() = %v, want %v", got, repos)
		}
		return nil
	}); err != nil {
		t.Fatalf("Registry.Repositories() error = %v", err)
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
		if r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected access: %s", r.URL)
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
		t.Fatalf("invalid test http server: %s", err)
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
