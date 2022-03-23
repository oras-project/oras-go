package registry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

func testRegistry() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := struct {
			Repositories []string `json:"repositories"`
		}{
			Repositories: []string{"public/repo1", "public/repo2", "internal/repo3"},
		}
		json.NewEncoder(w).Encode(result)
	}))
}

var exampleReg *remote.Registry

func TestMain(m *testing.M) {
	// Mocking local registry
	ts := testRegistry()
	defer ts.Close()
	exampleUri, err := url.Parse(ts.URL)
	if err != nil {
		panic(err)
	}
	exampleReg, err = remote.NewRegistry(exampleUri.Host) // Create a registry via the remote host
	if err != nil {
		panic(err) // Handle error
	}
	exampleReg.PlainHTTP = true // Use HTTP
	os.Exit(m.Run())
}

// This is a example for listing respositories in the registry:
func ExampleRepositories() {
	// Example: List repositories in a registry
	ctx := context.Background()

	repos, err := registry.Repositories(ctx, exampleReg)
	if err != nil {
		panic(err) // Handle error
	}

	for _, repo := range repos {
		fmt.Println(repo)
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}
