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

// Package content_test gives examples code of functions in the content package.
package content_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	exampleRepositoryName = "example"
	exampleDigest         = "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	exampleTag            = "latest"
	exampleBlob           = "Example blob content"
)

var host string

func TestMain(m *testing.M) {
	// Setup a local HTTPS registry
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		m := r.Method
		switch {

		case (p == fmt.Sprintf("/v2/%s/manifests/latest", exampleRepositoryName) || p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleDigest)) && m == "HEAD":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
		case (p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleDigest)) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepositoryName, exampleDigest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleTag) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		}

	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		panic(err) // Handle error
	}
	host = u.Host
	http.DefaultClient = ts.Client()

	os.Exit(m.Run())
}

// ExampleFetchAll_byTag gives example snippets for downloading a blob by tag.
func ExampleFetchAll_remoteByTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName) // Get the repository from registry
	if err != nil {
		panic(err) // Handle error
	}

	tag := "latest"
	descriptor, err := repo.Resolve(ctx, tag) // First resolve the tag to the descriptor
	if err != nil {
		panic(err) // Handle error
	}
	pulledBlob, err := content.FetchAll(ctx, repo, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(string(pulledBlob))

	// Output:
	// Example blob content
}

// ExampleFetchAll_byDigest gives example snippets for downloading a blob by digest.
func ExampleFetchAll_remoteByDigest() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName) // Get the repository from registry
	if err != nil {
		panic(err) // Handle error
	}

	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	descriptor, err := repo.Resolve(ctx, digest) // First resolve the tag to the descriptor
	if err != nil {
		panic(err) // Handle error
	}
	pulledBlob, err := content.FetchAll(ctx, repo, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(string(pulledBlob))

	// Output:
	// Example blob content
}
