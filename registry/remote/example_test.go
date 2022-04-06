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
// Package remote_test includes all the testable examples for remote repository type

package remote_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	ocidigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	exampleRepositoryName = "example"
	exampleDigest         = "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	exampleTag            = "latest"
	exampleBlob           = "Example blob content"
	exampleUploadUUid     = "0bc84d80-837c-41d9-824e-1907463c53b3"
)

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
		case p == fmt.Sprintf("/v2/%s/blobs/uploads/", exampleRepositoryName):
			w.Header().Set("Location", p+exampleUploadUUid)
			w.Header().Set("Docker-Upload-UUID", exampleUploadUUid)
			w.WriteHeader(http.StatusAccepted)
		case p == fmt.Sprintf("/v2/%s/blobs/uploads/%s", exampleRepositoryName, exampleUploadUUid):
			w.WriteHeader(http.StatusCreated)
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
			w.WriteHeader(http.StatusPartialContent)
			if h := r.Header.Get("Range"); h != "" {
				w.WriteHeader(http.StatusPartialContent)
				indices := strings.Split(strings.Split(h, "=")[1], "-")
				start, err := strconv.Atoi(indices[0])
				if err != nil {
					panic(err) // Handle error
				}
				end, err := strconv.Atoi(indices[1])
				if err != nil {
					panic(err) // Handle error
				}
				resultBlob := exampleBlob[start : end+1]
				w.Header().Set("Content-Length", strconv.Itoa(len([]byte(resultBlob))))
				w.Write([]byte(resultBlob))
			}
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

// ExampleRepository_Tags gives example snippets for listing tags in a repository.
func ExampleRepository_Tags() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName) // Get the repository from registry
	if err != nil {
		panic(err) // Handle error
	}

	err = repo.Tags(ctx, func(tags []string) error {
		for _, tag := range tags {
			fmt.Println(tag)
		}
		return nil
	})

	if err != nil {
		panic(err) // Handle error
	}

	// Output:
	// tag1
	// tag2
}

// ExampleRepository_Push gives example snippets for pushing a blob.
func ExampleRepository_Push() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName) // Get the repository from registry
	if err != nil {
		panic(err) // Handle error
	}

	mediaType, content := ocispec.MediaTypeImageLayer, []byte("Example blob content") // Setup input: 1) media type and 2)[]byte content
	desc := ocispec.Descriptor{                                                       // Assemble a descriptor
		MediaType: mediaType,                    // Set mediatype
		Digest:    ocidigest.FromBytes(content), // Calculate digest
		Size:      int64(len(content)),          // Include content size
	}
	err = repo.Push(ctx, desc, bytes.NewReader(content)) // Push the blob
	if err != nil {
		panic(err) // Handle error
	}

	fmt.Println("Push finished")
	// Output:
	// Push finished
}

// ExampleRepository_Resolve_byTag gives example snippets for resolving a tag.
func ExampleRepository_Resolve_byTag() {
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
	descriptor, err := repo.Resolve(ctx, tag) // Resolve the descriptor
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(descriptor.MediaType)
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// Output:
	// application/vnd.oci.image.layer.v1.tar
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
	// 20
}

// ExampleRepository_Resolve_byDigest gives example snippets for resolving a digest.
func ExampleRepository_Resolve_byDigest() {
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
	descriptor, err := repo.Resolve(ctx, digest) // Resolve digest to the descriptor
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(descriptor.MediaType)
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// Output:
	// application/vnd.oci.image.layer.v1.tar
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
	// 20
}

// ExampleRepository_Fetch_byTag gives example snippets for downloading a blob by tag.
func ExampleRepository_Fetch_byTag() {
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
	r, err := repo.Fetch(ctx, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err) // Handle error
	}
	defer r.Close() // don't forget to close
	pulledBlob, err := io.ReadAll(r)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(string(pulledBlob))
	// caller SHOULD verify what is fetched
	if descriptor.Digest != ocidigest.FromBytes(pulledBlob) || descriptor.Size != int64(len(pulledBlob)) {
		panic(err) // Handle error
	}

	// Output:
	// Example blob content
}

// ExampleRepository_Fetch_byDigest gives example snippets for downloading a blob by digest.
func ExampleRepository_Fetch_byDigest() {
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
	descriptor, err := repo.Resolve(ctx, digest) // Still need to resolve first, don't create a new descriptor with the digest, blob size is unknown
	if err != nil {
		panic(err) // Handle error
	}
	r, err := repo.Fetch(ctx, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err) // Handle error
	}
	defer r.Close() // don't forget to close

	// option 1: fetch a WHOLE blob
	pulledBlob, err := io.ReadAll(r)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(string(pulledBlob))

	// option 2: partially read, if the remote registry supports
	if seeker, ok := r.(io.ReadSeeker); ok {
		_, err = seeker.Seek(8, io.SeekStart)
		if err != nil {
			panic(err) // Handle error
		}
		pulledBlob, err := io.ReadAll(r)
		if err != nil {
			panic(err) // Handle error
		}
		fmt.Println(string(pulledBlob))
	}

	// caller SHOULD verify what is fetched
	if descriptor.Digest != ocidigest.FromBytes(pulledBlob) || descriptor.Size != int64(len(pulledBlob)) {
		panic(err) // Handle error
	}

	// Output:
	// Example blob content
	// blob content
}

// ExampleRepository_Tag gives example snippets for tagging a descriptor.
func ExampleRepository_Tag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName) // Get the repository from registry
	if err != nil {
		panic(err) // Handle error
	}

	// suppose we are going to tag a blob with below digest
	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"

	// 1. Resolve the target desc
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err) // Handle error
	}

	// 2. Tag the resolved desc
	tag := "latest"
	err = repo.Tag(ctx, descriptor, tag)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println("Succeed")

	// Output:
	// Succeed
}

// ExampleRegistry_Repositories gives example snippets for listing respositories in a HTTPS registry with pagination.
func ExampleRegistry_Repositories() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	// Override the `host` variable to play with local registry.
	// Uncomment below line to reset HTTP option:
	// reg.PlainHTTP = true
	ctx := context.Background()
	err = reg.Repositories(ctx, func(repos []string) error {
		for _, repo := range repos {
			fmt.Println(repo)
		}
		return nil
	})
	if err != nil {
		panic(err) // Handle error
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}

func Example_pullByTag() {
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
	descriptor, err := repo.Resolve(ctx, tag) // First resolve the descriptor
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

func Example_pullByDigest() {
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
	descriptor, err := repo.Resolve(ctx, digest) // Resolve the descriptor
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
