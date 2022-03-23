package remote_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"oras.land/oras-go/v2/registry/remote"
)

var exampleRegistry *remote.Registry

func TestMain(m *testing.M) {
	// Mocking local registry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := struct {
			Repositories []string `json:"repositories"`
		}{
			Repositories: []string{"public/repo1", "public/repo2", "internal/repo3"},
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer ts.Close()
	exampleUri, err := url.Parse(ts.URL)
	if err != nil {
		panic(err)
	}
	exampleRegistry, err = remote.NewRegistry(exampleUri.Host) // Create a registry via the remote host
	if err != nil {
		panic(err) // Handle error
	}
	exampleRegistry.PlainHTTP = true // Use HTTP
	os.Exit(m.Run())
}

// ExampleRegistry_Repositories gives example snippets for listing respositories in the registry with pagination.
func ExampleRegistry_Repositories() {
	fn := func(repos []string) error { // Setup a callback function to process returned repository list
		for _, repo := range repos {
			fmt.Println(repo)
		}
		return nil
	}

	ctx := context.Background()
	err := exampleRegistry.Repositories(ctx, fn)
	if err != nil {
		//handle it
		panic(err)
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}
