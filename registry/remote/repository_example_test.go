// This file includes all the testable examples for remote repository type

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
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

const exampleRepoName = "example"
const exampleDigest = "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
const exampleTag = "latest"
const exampleBlob = "Example blob content"

func testService() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		m := r.Method
		switch {
		case p == fmt.Sprintf("/v2/%s/tags/list", exampleRepoName) && m == "GET":
			result := struct {
				Tags []string `json:"tags"`
			}{
				Tags: []string{"tag1", "tag2"},
			}
			json.NewEncoder(w).Encode(result)
		case p == fmt.Sprintf("/v2/%s/blobs/uploads/", exampleRepoName) && m == "GET":
			w.WriteHeader(http.StatusCreated)
		case (p == fmt.Sprintf("/v2/%s/manifests/latest", exampleRepoName) || p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepoName, exampleDigest)) && m == "HEAD":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
		case (p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepoName, exampleDigest)) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepoName, exampleDigest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepoName, exampleTag) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		}

	}))
}

var exampleReg *remote.Registry

func TestMain(m *testing.M) {
	// Mocking local registry
	ts := testService()
	defer ts.Close()
	exampleUrl, err := url.Parse(ts.URL)
	if err != nil {
		panic(err)
	}
	exampleReg, err = remote.NewRegistry(exampleUrl.Host) // Create a registry via the remote host
	if err != nil {
		panic(err) // Handle error
	}
	exampleReg.PlainHTTP = true // Use HTTP
	os.Exit(m.Run())
}

func ExampleRepository_Tags() {
	// Example: List tags in a repository
	ctx := context.Background()
	repo, err := exampleReg.Repository(ctx, exampleRepoName) // Get the repository from registry
	if err != nil {
		panic(err)
	}

	fn := func(tags []string) error { // Setup a callback function to process returned tag list
		for _, tag := range tags {
			fmt.Println(tag)
		}
		return nil
	}
	repo.Tags(ctx, fn)

	// Output:
	// tag1
	// tag2
}

func ExampleRepository_Push() {
	// Example: Push a blob to a repository
	ctx := context.Background()
	repo, err := exampleReg.Repository(ctx, exampleRepoName) // Get the repository from registry
	if err != nil {
		panic(err)
	}

	mediaType, content := ocispec.MediaTypeImageLayer, []byte("Example blob content") // Setup input: 1) media type and 2)[]byte content
	desc := ocispec.Descriptor{                                                       // Assemble a descriptor
		MediaType: mediaType,                 // Set mediatype
		Digest:    digest.FromBytes(content), // Calculate digest
		Size:      int64(len(content)),       // Include content size
	}
	repo.Push(ctx, desc, bytes.NewReader(content)) // Push the blob

	fmt.Println("Push finished")
	// Output:
	// Push finished
}

func ExampleRepository_Resolve() {
	// Example: Resolve a blob from a registry
	ctx := context.Background()
	repo, err := exampleReg.Repository(ctx, exampleRepoName) // Get the repository from registry
	if err != nil {
		panic(err)
	}
	// suppose we are going to pull a blob with below digest and tag
	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	tag := "latest"

	// 1. Resolve via tag
	descriptor, err := repo.Resolve(ctx, tag) // Resolve tag to the descriptor
	if err != nil {
		panic(err)
	}
	fmt.Println(descriptor.Digest)

	// 2. Resolve via digest
	descriptor, err = repo.Resolve(ctx, digest) // Resolve digest to the descriptor
	if err != nil {
		panic(err)
	}
	fmt.Println(descriptor.Size)

	// Output:
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
	// 20
}

func ExampleRepository_Fetch() {
	// Example: Pull a blob from a registry
	ctx := context.Background()
	repo, err := exampleReg.Repository(ctx, exampleRepoName) // Get the repository from registry
	if err != nil {
		panic(err)
	}
	// suppose we are going to pull a blob with below digest and tag
	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	tag := "latest"

	// 1. pull with tag
	descriptor, err := repo.Resolve(ctx, tag) // First resolve the tag to the descriptor
	if err != nil {
		panic(err)
	}
	r, err := repo.Fetch(ctx, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err)
	}
	defer r.Close() // don't forget to close
	pulledBlob, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledBlob))

	// 2. pull with digest
	descriptor, err = repo.Resolve(ctx, digest) // We still need to resolve first, don't create a new descriptor with the digest, blob size is unknown
	if err != nil {
		panic(err)
	}
	r, err = repo.Fetch(ctx, descriptor) // Fetch the blob from the repository
	if err != nil {
		panic(err)
	}
	defer r.Close() // don't forget to close
	pulledBlob, err = io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledBlob))

	// Output:
	// Example blob content
	// Example blob content
}

func ExampleRepository_Tag() {
	// Example: Tag a manifest a blob from a registry
	ctx := context.Background()
	repo, err := exampleReg.Repository(ctx, exampleRepoName) // Get the repository from registry
	if err != nil {
		panic(err)
	}
	// suppose we are going to pull a blob with below digest and tag
	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	tag := "latest"

	// 1. Resolve the target desc
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err)
	}

	// 2. Tag the resolved desc
	err = repo.Tag(ctx, descriptor, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println("Succeed")

	// Output:
	// Succeed
}
