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

	"github.com/opencontainers/go-digest"
	ocidigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	exampleRepositoryName = "example"
	exampleTag            = "latest"
	exampleManifest       = "Example manifest content"
	exampleLayer          = "Example layer content"
	exampleUploadUUid     = "0bc84d80-837c-41d9-824e-1907463c53b3"
)

var host string

func TestMain(m *testing.M) {
	exampleLayerDigest := digest.FromBytes([]byte(exampleLayer)).String()
	exampleManifestDigest := digest.FromBytes([]byte(exampleManifest)).String()
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
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleTag) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleTag) || p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleManifestDigest):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", exampleManifestDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleManifest))))
			if m == "GET" {
				w.Write([]byte(exampleManifest))
			}
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepositoryName, exampleLayerDigest):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", string(exampleLayerDigest))
			var start, end = 0, len(exampleLayer) - 1
			if h := r.Header.Get("Range"); h != "" {
				w.WriteHeader(http.StatusPartialContent)
				indices := strings.Split(strings.Split(h, "=")[1], "-")
				var err error
				start, err = strconv.Atoi(indices[0])
				if err != nil {
					panic(err)
				}
				end, err = strconv.Atoi(indices[1])
				if err != nil {
					panic(err)
				}
			}
			resultBlob := exampleLayer[start : end+1]
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(resultBlob))))
			if m == "GET" {
				w.Write([]byte(resultBlob))
			}
		}

	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		panic(err)
	}
	host = u.Host
	http.DefaultClient = ts.Client()

	os.Exit(m.Run())
}

// ExampleRepository_Tags gives example snippets for listing tags in a repository.
func ExampleRepository_Tags() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	err = repo.Tags(ctx, func(tags []string) error {
		for _, tag := range tags {
			fmt.Println(tag)
		}
		return nil
	})

	if err != nil {
		panic(err)
	}

	// Output:
	// tag1
	// tag2
}

// ExampleRepository_Push gives example snippets for pushing a layer.
func ExampleRepository_Push() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	// 1. assemble a descriptor
	mediaType, content := ocispec.MediaTypeImageLayer, []byte("Example layer content")
	descriptor := ocispec.Descriptor{
		MediaType: mediaType,                    // Set media type
		Digest:    ocidigest.FromBytes(content), // Calculate digest
		Size:      int64(len(content)),          // Include content size
	}
	// 2. push the descriptor and blob content
	err = repo.Push(ctx, descriptor, bytes.NewReader(content))
	if err != nil {
		panic(err)
	}

	fmt.Println("Push finished")
	// Output:
	// Push finished
}

// ExampleRepository_Resolve_byTag gives example snippets for resolving a tag to a manifest descriptor.
func ExampleRepository_Resolve_byTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	tag := "latest"
	descriptor, err := repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}

	fmt.Println(descriptor.MediaType)
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// Output:
	// application/vnd.oci.image.manifest.v1+json
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
}

// ExampleRepository_Resolve_byDigest gives example snippets for resolving a digest to a manifest descriptor.
func ExampleRepository_Resolve_byDigest() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}
	digest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err)
	}

	fmt.Println(descriptor.MediaType)
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// Output:
	// application/vnd.oci.image.manifest.v1+json
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
}

// ExampleRepository_Fetch_byTag gives example snippets for downloading a manifest by tag.
func ExampleRepository_Fetch_manifestByTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	tag := "latest"
	descriptor, err := repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulledBlob, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	// verify the fetched content
	if descriptor.Digest != ocidigest.FromBytes(pulledBlob) || descriptor.Size != int64(len(pulledBlob)) {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// Example manifest content
}

// ExampleRepository_Fetch_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_Fetch_manifestByDigest() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	digest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	// resolve the blob descriptor to obtain the size of the blob
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulled, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	// verify the fetched content
	if descriptor.Digest != ocidigest.FromBytes(pulled) || descriptor.Size != int64(len(pulled)) {
		panic(err)
	}

	fmt.Println(string(pulled))
	// Output:
	// Example manifest content
}

// ExampleRepository_FetchReference_manifestByTag gives example snippets for downloading a manifest by tag with only one API call.
func ExampleRepository_FetchReference_manifestByTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	tag := "latest"
	// specify the expected manifest media type for the blob
	reg.ManifestMediaTypes = append(reg.ManifestMediaTypes, ocispec.MediaTypeImageManifest)
	descriptor, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulledBlob, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	// verify the fetched content
	if descriptor.Digest != ocidigest.FromBytes(pulledBlob) || descriptor.Size != int64(len(pulledBlob)) {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// Example manifest content
}

// ExampleRepository_FetchReference_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_FetchReference_manifestByDigest() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	digest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	descriptor, rc, err := repo.FetchReference(ctx, digest)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulled, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	// verify the fetched content
	if descriptor.Digest != ocidigest.FromBytes(pulled) || descriptor.Size != int64(len(pulled)) {
		panic(err)
	}

	fmt.Println(string(pulled))

	// Output:
	// Example manifest content
}

// ExampleRepository_Fetch_layer gives example snippets for downloading a layer blob by digest.
func ExampleRepository_Fetch_layer() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	descriptor, err := repo.Blobs().Resolve(ctx, digest.FromBytes([]byte(exampleLayer)).String())
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close

	// option 1: fetch a WHOLE blob
	pulledBlob, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledBlob))

	// verify the fetched content
	if descriptor.Digest != ocidigest.FromBytes(pulledBlob) || descriptor.Size != int64(len(pulledBlob)) {
		panic(err)
	}

	// option 2: partially read, if the remote registry supports
	if seeker, ok := rc.(io.ReadSeeker); ok {
		offset := int64(8)
		_, err = seeker.Seek(offset, io.SeekStart)
		if err != nil {
			panic(err)
		}
		pulledBlob, err := io.ReadAll(rc)
		if err != nil {
			panic(err)
		}
		if descriptor.Size-offset != int64(len(pulledBlob)) {
			panic(err)
		}
		fmt.Println(string(pulledBlob))
	}

	// Output:
	// Example layer content
	// layer content
}

// ExampleRepository_Tag gives example snippets for tagging a manifest.
func ExampleRepository_Tag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	// tag a manifest referenced by the digest below
	digest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err)
	}
	tag := "latest"
	err = repo.Tag(ctx, descriptor, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println("Succeed")

	// Output:
	// Succeed
}

// ExampleRegistry_Repositories gives example snippets for listing respositories in a HTTPS registry with pagination.
func ExampleRegistry_Repositories() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
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
		panic(err)
	}

	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}

func Example_pullByTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	// 1. resolve the descriptor
	tag := "latest"
	descriptor, err := repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)
	// 2. fetch the content byte[] from the repository
	pulledBlob, err := content.FetchAll(ctx, repo, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
	// Example manifest content
}

func Example_pullByDigest() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	digest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	// 1. resolve the descriptor
	descriptor, err := repo.Resolve(ctx, digest)
	if err != nil {
		panic(err)
	}
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)
	// 2. fetch the content byte[] from the repository
	pulledBlob, err := content.FetchAll(ctx, repo, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
	// Example manifest content
}

// Example_pushAndTag gives example snippet of pushing a OCI image with a tag.
func Example_pushAndTag() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	// Assemble the below OCI image, push and tag it
	//   +---------------------------------------------------+
	//   |                                +----------------+ |
	//   |                             +--> "Hello Config" | |
	//   |            +-------------+  |  +---+ Config +---+ |
	//   | (latest)+-->     ...     +--+                     |
	//   |            ++ Manifest  ++  |  +----------------+ |
	//   |                             +--> "Hello Layer"  | |
	//   |                                +---+ Layer  +---+ |
	//   |                                                   |
	//   +--------+ localhost:5000/example/registry +--------+

	generateDescriptor := func(mediaType string, blob []byte) (desc ocispec.Descriptor) {
		return ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob), // Calculate digest
			Size:      int64(len(blob)),       // Include blob size
		}
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) ([]byte, error) {
		content := ocispec.Manifest{
			Config:    config, // Set config blob
			Layers:    layers, // Set layer blobs
			Versioned: specs.Versioned{SchemaVersion: 2},
		}
		return json.Marshal(content)
	}

	// 1. assemble descriptors and manifest
	layerBlob := []byte("Hello layer")
	layerDesc := generateDescriptor(ocispec.MediaTypeImageLayer, layerBlob)
	configBlob := []byte("Hello config")
	configDesc := generateDescriptor(ocispec.MediaTypeImageConfig, configBlob)
	manifestBlob, err := generateManifest(configDesc, layerDesc)
	if err != nil {
		panic(err)
	}
	manifestDesc := generateDescriptor(ocispec.MediaTypeImageManifest, manifestBlob)

	// 2. push and tag
	err = repo.Push(ctx, layerDesc, bytes.NewReader(layerBlob))
	if err != nil {
		panic(err)
	}
	err = repo.Push(ctx, configDesc, bytes.NewReader(configBlob))
	if err != nil {
		panic(err)
	}
	err = repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBlob), "latest")
	if err != nil {
		panic(err)
	}

	fmt.Println("Succeed")

	// Output:
	// Succeed
}
