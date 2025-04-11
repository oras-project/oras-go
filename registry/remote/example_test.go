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
	"errors"
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
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	. "oras.land/oras-go/v2/registry/internal/doc"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	_                     = ExampleUnplayable
	exampleRepositoryName = "example"
	exampleTag            = "latest"
	exampleConfig         = "Example config content"
	exampleLayer          = "Example layer content"
	exampleUploadUUid     = "0bc84d80-837c-41d9-824e-1907463c53b3"
	// For ExampleRepository_Push_artifactReferenceManifest:
	ManifestDigest          = "sha256:a3f9d449466b9b7194c3a76ca4890d792e11eb4e62e59aa8b4c3cce0a56f129d"
	ReferenceManifestDigest = "sha256:a510c27b6bfbb3976fbdd80b42db476f306a9f693095ac0fe114f36bb01ebe87"
	// For Example_pushAndIgnoreReferrersIndexError:
	referrersAPIUnavailableRepositoryName = "no-referrers-api"
	referrerDigest                        = "sha256:4caba1e18385eb152bd92e9fee1dc01e47c436e594123b3c2833acfcad9883e2"
	referrersTag                          = "sha256-c824a9aa7d2e3471306648c6d4baa1abbcb97ff0276181ab4722ca27127cdba0"
	referrerIndexDigest                   = "sha256:7baac5147dd58d56fdbaad5a888fa919235a3a90cb71aaa8b56ee5d19f4cd838"
)

var (
	exampleLayerDescriptor = content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, []byte(exampleLayer))
	exampleLayerDigest     = exampleLayerDescriptor.Digest.String()
	exampleManifest, _     = json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config: content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte(exampleConfig)),
		Layers: []ocispec.Descriptor{
			exampleLayerDescriptor,
		},
	})
	exampleManifestDescriptor   = content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, exampleManifest)
	exampleManifestDigest       = exampleManifestDescriptor.Digest.String()
	exampleSignatureManifest, _ = json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("config bytes")),
		ArtifactType: "example/signature",
		Layers:       []ocispec.Descriptor{exampleLayerDescriptor},
		Subject:      &exampleManifestDescriptor})
	exampleSignatureManifestDescriptor = ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: "example/signature",
		Digest:       digest.FromBytes(exampleSignatureManifest),
		Size:         int64(len(exampleSignatureManifest))}
	exampleSBoMManifest, _ = json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("config bytes")),
		ArtifactType: "example/SBoM",
		Layers:       []ocispec.Descriptor{exampleLayerDescriptor},
		Subject:      &exampleManifestDescriptor})
	exampleSBoMManifestDescriptor = ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: "example/SBoM",
		Digest:       digest.FromBytes(exampleSBoMManifest),
		Size:         int64(len(exampleSBoMManifest))}
	exampleReferrerDescriptors = [][]ocispec.Descriptor{
		{exampleSBoMManifestDescriptor},      // page 0
		{exampleSignatureManifestDescriptor}, // page 1
	}
	blobContent    = "example blob content"
	blobDescriptor = ocispec.Descriptor{
		MediaType: "application/tar",
		Digest:    digest.FromBytes([]byte(blobContent)),
		Size:      int64(len(blobContent))}
	exampleManifestWithBlobs, _ = json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("config bytes")),
		ArtifactType: "example/manifest",
		Layers:       []ocispec.Descriptor{blobDescriptor},
		Subject:      &exampleManifestDescriptor})
	exampleManifestWithBlobsDescriptor = ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: "example/manifest",
		Digest:       digest.FromBytes(exampleManifestWithBlobs),
		Size:         int64(len(exampleManifestWithBlobs))}
	subjectDescriptor          = content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte(`{"layers":[]}`))
	referrerManifestContent, _ = json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Subject:   &subjectDescriptor,
		Config:    ocispec.DescriptorEmptyJSON,
	})
	referrerDescriptor = content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, referrerManifestContent)
	referrerIndex, _   = json.Marshal(ocispec.Index{
		Manifests: []ocispec.Descriptor{},
	})
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
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleTag) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, ManifestDigest) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, ReferenceManifestDigest) && m == "PUT":
			w.Header().Set("OCI-Subject", "sha256:a3f9d449466b9b7194c3a76ca4890d792e11eb4e62e59aa8b4c3cce0a56f129d")
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleSignatureManifestDescriptor.Digest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Content-Digest", string(exampleSignatureManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleSignatureManifest)))
			w.Write(exampleSignatureManifest)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleSBoMManifestDescriptor.Digest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Content-Digest", string(exampleSBoMManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleSBoMManifest)))
			w.Write(exampleSBoMManifest)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleManifestWithBlobsDescriptor.Digest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Content-Digest", string(exampleManifestWithBlobsDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleManifestWithBlobs)))
			w.Write(exampleManifestWithBlobs)
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepositoryName, blobDescriptor.Digest) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Content-Digest", string(blobDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(blobContent)))
			w.Write([]byte(blobContent))
		case p == fmt.Sprintf("/v2/%s/referrers/%s", exampleRepositoryName, "sha256:0000000000000000000000000000000000000000000000000000000000000000"):
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				panic(err)
			}
		case p == fmt.Sprintf("/v2/%s/referrers/%s", exampleRepositoryName, exampleManifestDescriptor.Digest.String()):
			q := r.URL.Query()
			var referrers []ocispec.Descriptor
			switch q.Get("test") {
			case "page1":
				referrers = exampleReferrerDescriptors[1]
			default:
				referrers = exampleReferrerDescriptors[0]
				w.Header().Set("Link", fmt.Sprintf(`<%s?n=1&test=page1>; rel="next"`, p))
			}
			result := ocispec.Index{
				Versioned: specs.Versioned{
					SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
				},
				MediaType: ocispec.MediaTypeImageIndex,
				Manifests: referrers,
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				panic(err)
			}
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleTag) || p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleManifestDigest):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", exampleManifestDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleManifest))))
			w.Header().Set("Warning", `299 - "This image is deprecated and will be removed soon."`)
			if m == "GET" {
				w.Write([]byte(exampleManifest))
			}
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepositoryName, exampleLayerDigest):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", string(exampleLayerDigest))
			w.Header().Set("Accept-Ranges", "bytes")
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
		case p == fmt.Sprintf("/v2/%s/referrers/%s", referrersAPIUnavailableRepositoryName, "sha256:0000000000000000000000000000000000000000000000000000000000000000"):
			w.WriteHeader(http.StatusNotFound)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", referrersAPIUnavailableRepositoryName, referrerDigest) && m == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", referrersAPIUnavailableRepositoryName, referrersTag) && m == http.MethodGet:
			w.Write(referrerIndex)
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Header().Set("Content-Length", strconv.Itoa(len(referrerIndex)))
			w.Header().Set("Docker-Content-Digest", digest.Digest(string(referrerIndex)).String())
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", referrersAPIUnavailableRepositoryName, referrersTag) && m == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", referrersAPIUnavailableRepositoryName, referrerIndexDigest) && m == http.MethodDelete:
			w.WriteHeader(http.StatusMethodNotAllowed)
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

// ExampleRepository_Tags gives example snippets for listing tags in a repository.
func ExampleRepository_Tags() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	err = repo.Tags(ctx, "", func(tags []string) error {
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
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// 1. assemble a descriptor
	layer := []byte("Example layer content")
	descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, layer)
	// 2. push the descriptor and blob content
	err = repo.Push(ctx, descriptor, bytes.NewReader(layer))
	if err != nil {
		panic(err)
	}

	fmt.Println("Push finished")
	// Output:
	// Push finished
}

// ExampleRepository_Push_referrerManifest gives an example snippet for pushing a manifest as a referrer to another manifest.
func ExampleRepository_Push_referrerManifest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// 1. assemble the referenced manifest
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("config bytes")),
	}
	manifestContent, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	manifestDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestContent)

	// 2. push the manifest descriptor and content
	err = repo.Push(ctx, manifestDescriptor, bytes.NewReader(manifestContent))
	if err != nil {
		panic(err)
	}

	// 3. assemble the reference manifest
	referenceManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("config bytes")),
		ArtifactType: "sbom/example",
		Subject:      &manifestDescriptor,
	}
	referenceManifestContent, err := json.Marshal(referenceManifest)
	if err != nil {
		panic(err)
	}
	referenceManifestDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, referenceManifestContent)
	// 4. push the reference manifest descriptor and content
	err = repo.Push(ctx, referenceManifestDescriptor, bytes.NewReader(referenceManifestContent))
	if err != nil {
		panic(err)
	}

	fmt.Println("Push finished")
	// Output:
	// Push finished
}

// ExampleRepository_Resolve_byTag gives example snippets for resolving a tag to a manifest descriptor.
func ExampleRepository_Resolve_byTag() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

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
	// sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7
	// 337
}

// ExampleRepository_Resolve_byDigest gives example snippets for resolving a digest to a manifest descriptor.
func ExampleRepository_Resolve_byDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	descriptor, err := repo.Resolve(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}

	fmt.Println(descriptor.MediaType)
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// Output:
	// application/vnd.oci.image.manifest.v1+json
	// sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7
	// 337
}

// ExampleRepository_Fetch_byTag gives example snippets for downloading a manifest by tag.
func ExampleRepository_Fetch_manifestByTag() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

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
	pulledBlob, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

// ExampleRepository_Fetch_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_Fetch_manifestByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	// resolve the blob descriptor to obtain the size of the blob
	descriptor, err := repo.Resolve(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulled, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulled))
	// Output:
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

// ExampleRepository_Fetch_referrerManifest gives an example of fetching
// the referrers of a given manifest by using the Referrers API.
func ExampleRepository_Fetch_referrerManifest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// resolve a manifest by tag
	tag := "latest"
	descriptor, err := repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}
	// find its referrers by calling Referrers
	if err := repo.Referrers(ctx, descriptor, "", func(referrers []ocispec.Descriptor) error {
		// for each page of the results, do the following:
		for _, referrer := range referrers {
			// for each item in this page, pull the manifest and verify its content
			rc, err := repo.Fetch(ctx, referrer)
			if err != nil {
				panic(err)
			}
			defer rc.Close() // don't forget to close
			pulledBlob, err := content.ReadAll(rc, referrer)
			if err != nil {
				panic(err)
			}
			fmt.Println(string(pulledBlob))
		}
		return nil
	}); err != nil {
		panic(err)
	}
	// Output:
	// {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"example/SBoM","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:fa7972d3a05c37631474cd92cbd08c3986a84b5db9e884b6fddfa8a2d41bae4d","size":12},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}],"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7","size":337}}
	// {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"example/signature","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:fa7972d3a05c37631474cd92cbd08c3986a84b5db9e884b6fddfa8a2d41bae4d","size":12},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}],"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7","size":337}}
}

// ExampleRepository_fetchManifestLayers gives an example of pulling the layers
// of an image manifest.
func ExampleRepository_fetchManifestLayers() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// 1. Fetch the artifact manifest by digest.
	exampleDigest := "sha256:1224272f27dd616e7db5c809bb8919f84b0fc7b0b357d1df0828c21f533f58bd"
	descriptor, rc, err := repo.FetchReference(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}
	defer rc.Close()

	pulledContent, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledContent))

	// 2. Parse the pulled manifest and fetch its blobs.
	var pulledManifest ocispec.Manifest
	if err := json.Unmarshal(pulledContent, &pulledManifest); err != nil {
		panic(err)
	}
	for _, blob := range pulledManifest.Layers {
		content, err := content.FetchAll(ctx, repo, blob)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(content))
	}

	// Output:
	// {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"example/manifest","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:fa7972d3a05c37631474cd92cbd08c3986a84b5db9e884b6fddfa8a2d41bae4d","size":12},"layers":[{"mediaType":"application/tar","digest":"sha256:8d6497c94694a292c04f85cd055d8b5c03eda835dd311e20dfbbf029ff9748cc","size":20}],"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7","size":337}}
	// example blob content
}

// ExampleRepository_FetchReference_manifestByTag gives example snippets for downloading a manifest by tag with only one API call.
func ExampleRepository_FetchReference_manifestByTag() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	tag := "latest"
	descriptor, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulledBlob, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulledBlob))

	// Output:
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

// ExampleRepository_FetchReference_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_FetchReference_manifestByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	descriptor, rc, err := repo.FetchReference(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	pulled, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(pulled))

	// Output:
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

// ExampleRepository_Fetch_layer gives example snippets for downloading a layer blob by digest.
func ExampleRepository_Fetch_layer() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	descriptor, err := repo.Blobs().Resolve(ctx, exampleLayerDigest)
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close

	// option 1: sequential fetch
	pulledBlob, err := content.ReadAll(rc, descriptor)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledBlob))

	// option 2: random access, if the remote registry supports
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
			panic("wrong content")
		}
		fmt.Println(string(pulledBlob))
	}

	// Output:
	// Example layer content
	// layer content
}

// ExampleRepository_Tag gives example snippets for tagging a descriptor.
func ExampleRepository_Tag() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	descriptor, err := repo.Resolve(ctx, exampleDigest)
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
	err = reg.Repositories(ctx, "", func(repos []string) error {
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
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

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
	// sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7
	// 337
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

func Example_pullByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	// 1. resolve the descriptor
	descriptor, err := repo.Resolve(ctx, exampleDigest)
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
	// sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7
	// 337
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

func Example_handleWarning() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	// 1. specify HandleWarning
	repo.HandleWarning = func(warning remote.Warning) {
		fmt.Printf("Warning from %s: %s\n", repo.Reference.Repository, warning.Text)
	}

	ctx := context.Background()
	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	// 2. resolve the descriptor
	descriptor, err := repo.Resolve(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}
	fmt.Println(descriptor.Digest)
	fmt.Println(descriptor.Size)

	// 3. fetch the content byte[] from the repository
	pulledBlob, err := content.FetchAll(ctx, repo, descriptor)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(pulledBlob))

	// Output:
	// Warning from example: This image is deprecated and will be removed soon.
	// sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7
	// 337
	// Warning from example: This image is deprecated and will be removed soon.
	// {"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:569224ae188c06e97b9fcadaeb2358fb0fb7c4eb105d49aee2620b2719abea43","size":22},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:ef79e47691ad1bc702d7a256da6323ec369a8fc3159b4f1798a47136f3b38c10","size":21}]}
}

// Example_pushAndTag gives example snippet of pushing an OCI image with a tag.
func Example_pushAndTag() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

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

	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) ([]byte, error) {
		content := ocispec.Manifest{
			Config:    config,
			Layers:    layers,
			Versioned: specs.Versioned{SchemaVersion: 2},
		}
		return json.Marshal(content)
	}
	// 1. assemble descriptors and manifest
	layerBlob := []byte("Hello layer")
	layerDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, layerBlob)
	configBlob := []byte("Hello config")
	configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, configBlob)
	manifestBlob, err := generateManifest(configDesc, layerDesc)
	if err != nil {
		panic(err)
	}
	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBlob)

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

// Example_tagReference gives example snippets for tagging
// a manifest.
func Example_tagReference() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	repo, err := reg.Repository(ctx, exampleRepositoryName)
	if err != nil {
		panic(err)
	}

	// tag a manifest referenced by the exampleDigest below
	exampleDigest := "sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7"
	tag := "latest"
	desc, err := oras.Tag(ctx, repo, exampleDigest, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println("Tagged", desc.Digest, "as", tag)

	// Output:
	// Tagged sha256:b53dc03a49f383ba230d8ac2b78a9c4aec132e4a9f36cc96524df98163202cc7 as latest
}

// Example_pushAndIgnoreReferrersIndexError gives example snippets on how to
// ignore referrer index deletion error during push a referrer manifest.
func Example_pushAndIgnoreReferrersIndexError() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, referrersAPIUnavailableRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// push a referrer manifest and ignore cleaning up error
	err = repo.Push(ctx, referrerDescriptor, bytes.NewReader(referrerManifestContent))
	if err != nil {
		var re *remote.ReferrersError
		if !errors.As(err, &re) || !re.IsReferrersIndexDelete() {
			panic(err)
		}
		fmt.Println("ignoring error occurred during cleaning obsolete referrers index")
	}
	fmt.Println("Push finished")

	// Output:
	// ignoring error occurred during cleaning obsolete referrers index
	// Push finished
}
