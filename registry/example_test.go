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
	. "oras.land/oras-go/v2/registry/internal/doc"
	"oras.land/oras-go/v2/registry/remote"
)

const _ = ExampleUnplayable

const exampleRepositoryName = "example"

var host string

func TestMain(m *testing.M) {
	// Setup a local HTTPS registry
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		m := r.Method
		switch {
		case p == "/v2/_catalog" && m == "GET":
			result := struct {
				Repositories []string `json:"repositories"`
			}{
				Repositories: []string{"public/repo1", "public/repo2", "internal/repo3"},
			}
			json.NewEncoder(w).Encode(result)
		case p == fmt.Sprintf("/v2/%s/tags/list", exampleRepositoryName) && m == "GET":
			result := struct {
				Tags []string `json:"tags"`
			}{
				Tags: []string{"tag1", "tag2"},
			}
			json.NewEncoder(w).Encode(result)
		}
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		panic(err)
	}
	host = u.Host
	http.DefaultTransport = ts.Client().Transport

	os.Exit(m.Run())
}

// ExampleRepositories gives example snippets for listing repositories in the registry without pagination.
func ExampleRepositories() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	repos, err := registry.Repositories(ctx, reg)
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

// ExampleTags gives example snippets for listing tags in the repository without pagination.
func ExampleTags() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	tags, err := registry.Tags(ctx, repo)
	if err != nil {
		panic(err) // Handle error
	}
	for _, tag := range tags {
		fmt.Println(tag)
	}
	// Output:
	// tag1
	// tag2
}
