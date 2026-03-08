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
	_ "crypto/sha256" // required to parse sha256 digest. See [Reference.Digest]
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/oras-project/oras-go/v3/registry"
	. "github.com/oras-project/oras-go/v3/registry/internal/doc"
	"github.com/oras-project/oras-go/v3/registry/remote"
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

// ExampleParseReference_digest demonstrates parsing a reference string with
// digest and print its components.
func ExampleParseReference_digest() {
	rawRef := "ghcr.io/oras-project/oras-go@sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76"
	ref, err := registry.ParseReference(rawRef)
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", ref.Registry)
	fmt.Println("Repository:", ref.Repository)

	digest, err := ref.GetDigest()
	if err != nil {
		panic(err)
	}
	fmt.Println("Digest:", digest)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras-go
	// Digest: sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76
}

// ExampleParseReference_withScheme demonstrates parsing a reference with
// a URI scheme and printing its components.
func ExampleParseReference_withScheme() {
	rawRef := "oci://ghcr.io/oras-project/oras-go:v3.0.0"
	ref, err := registry.ParseReference(rawRef)
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", ref.Registry)
	fmt.Println("Repository:", ref.Repository)
	fmt.Println("Tag:", ref.Reference)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras-go
	// Tag: v3.0.0
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
