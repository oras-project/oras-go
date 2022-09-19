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
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	. "oras.land/oras-go/v2/registry/internal/doc"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	exampleRepositoryName   = "example"
	exampleTag              = "latest"
	exampleManifest         = "Example manifest content"
	exampleLayer            = "Example layer content"
	exampleUploadUUid       = "0bc84d80-837c-41d9-824e-1907463c53b3"
	ManifestDigest          = "sha256:0b696106ecd0654e031f19e0a8cbd1aee4ad457d7c9cea881f07b12a930cd307"
	ReferenceManifestDigest = "sha256:b2122d3fd728173dd6b68a0b73caa129302b78c78273ba43ead541a88169c855"
	_                       = ExampleUnplayable
)

var (
	exampleLayerDigest        = digest.FromBytes([]byte(exampleLayer)).String()
	exampleManifestDigest     = digest.FromBytes([]byte(exampleManifest)).String()
	exampleManifestDescriptor = artifactspec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.Digest(exampleManifestDigest),
		Size:      int64(len(exampleManifest))}
	exampleSignatureManifest, _ = json.Marshal(artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/signature",
		Subject:      &exampleManifestDescriptor})
	exampleSignatureManifestDescriptor = ocispec.Descriptor{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/signature",
		Digest:       digest.FromBytes(exampleSignatureManifest),
		Size:         int64(len(exampleSignatureManifest))}
	exampleSBoMManifest, _ = json.Marshal(artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/SBoM",
		Subject:      &exampleManifestDescriptor})
	exampleSBoMManifestDescriptor = ocispec.Descriptor{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/SBoM",
		Digest:       digest.FromBytes(exampleSBoMManifest),
		Size:         int64(len(exampleSBoMManifest))}
	exampleReferrerDescriptors = [][]ocispec.Descriptor{
		{exampleSBoMManifestDescriptor},      // page 0
		{exampleSignatureManifestDescriptor}, // page 1
	}
	blobContent    = "example blob content"
	blobDescriptor = artifactspec.Descriptor{
		MediaType: "application/tar",
		Digest:    digest.FromBytes([]byte(blobContent)),
		Size:      int64(len(blobContent))}
	exampleManifestWithBlobs, _ = json.Marshal(artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/manifest",
		Blobs:        []artifactspec.Descriptor{blobDescriptor},
		Subject:      &exampleManifestDescriptor})
	exampleManifestWithBlobsDescriptor = ocispec.Descriptor{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/manifest",
		Digest:       digest.FromBytes(exampleManifestWithBlobs),
		Size:         int64(len(exampleManifestWithBlobs))}
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
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, ReferenceManifestDigest) && m == "PUT":
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.WriteHeader(http.StatusCreated)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleSignatureManifestDescriptor.Digest) && m == "GET":
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", string(exampleSignatureManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleSignatureManifest)))
			w.Write(exampleSignatureManifest)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleSBoMManifestDescriptor.Digest) && m == "GET":
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", string(exampleSBoMManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleSBoMManifest)))
			w.Write(exampleSBoMManifest)
		case p == fmt.Sprintf("/v2/%s/manifests/%s", exampleRepositoryName, exampleManifestWithBlobsDescriptor.Digest) && m == "GET":
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", string(exampleManifestWithBlobsDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleManifestWithBlobs)))
			w.Write(exampleManifestWithBlobs)
		case p == fmt.Sprintf("/v2/%s/blobs/%s", exampleRepositoryName, blobDescriptor.Digest) && m == "GET":
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Content-Digest", string(blobDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(blobContent)))
			w.Write([]byte(blobContent))
		case p == fmt.Sprintf("/v2/%s/_oras/artifacts/referrers", exampleRepositoryName):
			q := r.URL.Query()
			var referrers []ocispec.Descriptor
			switch q.Get("test") {
			case "page1":
				referrers = exampleReferrerDescriptors[1]
				w.Header().Set("ORAS-Api-Version", "oras/1.0")
			default:
				referrers = exampleReferrerDescriptors[0]
				w.Header().Set("ORAS-Api-Version", "oras/1.0")
				w.Header().Set("Link", fmt.Sprintf(`<%s?n=1&test=page1>; rel="next"`, p))
			}
			result := struct {
				Referrers []ocispec.Descriptor `json:"referrers"`
			}{
				Referrers: referrers,
			}
			if err := json.NewEncoder(w).Encode(result); err != nil {
				panic(err)
			}
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
	content := []byte("Example layer content")
	descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer, // Set media type
		Digest:    digest.FromBytes(content),   // Calculate digest
		Size:      int64(len(content)),         // Include content size
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

// ExampleRepository_Push_artifactReferenceManifest gives an example snippet for pushing a reference manifest.
func ExampleRepository_Push_artifactReferenceManifest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// 1. assemble the referenced artifact manifest
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
	}
	manifestContent, _ := json.Marshal(manifest)
	manifestDescriptor := artifactspec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestContent),
		Size:      int64(len(manifestContent)),
	}

	// 2. push the manifest descriptor and content
	err = repo.Push(ctx, ocispec.Descriptor{
		MediaType: manifestDescriptor.MediaType,
		Digest:    manifestDescriptor.Digest,
		Size:      manifestDescriptor.Size,
	}, bytes.NewReader(manifestContent))
	if err != nil {
		panic(err)
	}

	// 3. assemble the reference artifact manifest
	referenceManifest := artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "sbom/example",
		Subject:      &manifestDescriptor,
	}
	referenceManifestContent, _ := json.Marshal(referenceManifest)
	referenceManifestDescriptor := ocispec.Descriptor{
		MediaType: artifactspec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes(referenceManifestContent),
		Size:      int64(len(referenceManifestContent)),
	}

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
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
}

// ExampleRepository_Resolve_byDigest gives example snippets for resolving a digest to a manifest descriptor.
func ExampleRepository_Resolve_byDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	descriptor, err := repo.Resolve(ctx, exampleDigest)
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
	// Example manifest content
}

// ExampleRepository_Fetch_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_Fetch_manifestByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
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
	// Example manifest content
}

// ExampleRepository_Fetch_artifactReferenceManifest gives an example of fetching
// the referrers of a given manifest by using the Referrers API.
func ExampleRepository_Fetch_artifactReferenceManifest() {
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
			rc, err := repo.Fetch(ctx, ocispec.Descriptor{
				MediaType: referrer.MediaType,
				Digest:    referrer.Digest,
				Size:      referrer.Size})
			if err != nil {
				panic(err)
			}
			defer rc.Close() // don't forget to close
			pulledBlob, err := content.ReadAll(rc, ocispec.Descriptor{
				MediaType: referrer.MediaType,
				Digest:    referrer.Digest,
				Size:      referrer.Size})
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
	// {"mediaType":"application/vnd.cncf.oras.artifact.manifest.v1+json","artifactType":"example/SBoM","blobs":null,"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b","size":24}}
	// {"mediaType":"application/vnd.cncf.oras.artifact.manifest.v1+json","artifactType":"example/signature","blobs":null,"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b","size":24}}
}

// ExampleRepository_fetchArtifactBlobs gives an example of pulling the blobs
// of an artifact manifest.
func ExampleRepository_fetchArtifactBlobs() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// 1. Fetch the artifact manifest by digest.
	exampleDigest := "sha256:73bccdadf23b5df306bf77b23e1b00944c7bbce44cf63439afe15507a73413b5"
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
	var pulledManifest artifactspec.Manifest
	if err := json.Unmarshal(pulledContent, &pulledManifest); err != nil {
		panic(err)
	}
	for _, blob := range pulledManifest.Blobs {
		content, err := content.FetchAll(ctx, repo, ocispec.Descriptor{
			MediaType: blob.MediaType,
			Digest:    blob.Digest,
			Size:      blob.Size,
		})
		if err != nil {
			panic(err)
		}
		fmt.Println(string(content))
	}

	// Output:
	// {"mediaType":"application/vnd.cncf.oras.artifact.manifest.v1+json","artifactType":"example/manifest","blobs":[{"mediaType":"application/tar","digest":"sha256:8d6497c94694a292c04f85cd055d8b5c03eda835dd311e20dfbbf029ff9748cc","size":20}],"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b","size":24}}
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
	// Example manifest content
}

// ExampleRepository_FetchReference_manifestByDigest gives example snippets for downloading a manifest by digest.
func ExampleRepository_FetchReference_manifestByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
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
	// Example manifest content
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

	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
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

// ExampleRepository_TagReference gives example snippets for tagging
// a manifest.
func ExampleRepository_TagReference() {
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
	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
	tag := "latest"
	err = oras.Tag(ctx, repo, exampleDigest, tag)
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
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
	// Example manifest content
}

func Example_pullByDigest() {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", host, exampleRepositoryName))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	exampleDigest := "sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b"
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
	// sha256:00e5ffa7d914b4e6aa3f1a324f37df0625ccc400be333deea5ecaa199f9eff5b
	// 24
	// Example manifest content
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

	generateDescriptor := func(mediaType string, blob []byte) (desc ocispec.Descriptor) {
		return ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob), // Calculate digest
			Size:      int64(len(blob)),       // Include blob size
		}
	}
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
