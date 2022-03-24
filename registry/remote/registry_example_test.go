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

// Package registry_test gives examples code of functions in the remote package.
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

var registryUrl string

func TestMain(m *testing.M) {
	// Mocking local registry
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := struct {
			Repositories []string `json:"repositories"`
		}{
			Repositories: []string{"public/repo1", "public/repo2", "internal/repo3"},
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer ts.Close()
	registryUrl = ts.URL
	http.DefaultClient = ts.Client()
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
	// If you want to play with your local registry
	// Try to set registryUrl to its URL, like localhost:5000
	exampleUri, err := url.Parse(registryUrl)
	if err != nil {
		panic(err)
	}
	exampleRegistry, err := remote.NewRegistry(exampleUri.Host) // Create a registry via the remote host
	exampleRegistry.PlainHTTP = true                            // Use HTTP
	if err != nil {
		panic(err) // Handle error
	}

	err = exampleRegistry.Repositories(ctx, fn)
	if err != nil {
		//handle it
		panic(err)
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}
