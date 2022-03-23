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

// Package registry_test gives examples code of functions in the registry package.
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

// ExampleRepositories gives example snippets for listing respositories in the registry without pagination.
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
