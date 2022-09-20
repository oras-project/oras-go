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

package oras_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

var exampleMemoryStore oras.Target
var remoteHost string
var (
	exampleManifest, _ = json.Marshal(artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/content"})
	exampleManifestDescriptor = ocispec.Descriptor{
		MediaType: artifactspec.MediaTypeArtifactManifest,
		Digest:    digest.Digest(digest.FromBytes(exampleManifest)),
		Size:      int64(len(exampleManifest))}
	exampleManifestDescriptorArtifactspec = artifactspec.Descriptor{
		MediaType: exampleManifestDescriptor.MediaType,
		Digest:    exampleManifestDescriptor.Digest,
		Size:      exampleManifestDescriptor.Size}
	exampleSignatureManifest, _ = json.Marshal(artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: "example/signature",
		Subject:      &exampleManifestDescriptorArtifactspec})
	exampleSignatureManifestDescriptor = ocispec.Descriptor{
		MediaType: artifactspec.MediaTypeArtifactManifest,
		Digest:    digest.FromBytes(exampleSignatureManifest),
		Size:      int64(len(exampleSignatureManifest))}
)

func pushBlob(ctx context.Context, mediaType string, blob []byte, target oras.Target) (desc ocispec.Descriptor, err error) {
	desc = ocispec.Descriptor{ // Generate descriptor based on the media type and blob content
		MediaType: mediaType,
		Digest:    digest.FromBytes(blob), // Calculate digest
		Size:      int64(len(blob)),       // Include blob size
	}
	return desc, target.Push(ctx, desc, bytes.NewReader(blob)) // Push the blob to the registry target
}

func generateManifestContent(config ocispec.Descriptor, layers ...ocispec.Descriptor) ([]byte, error) {
	content := ocispec.Manifest{
		Config:    config, // Set config blob
		Layers:    layers, // Set layer blobs
		Versioned: specs.Versioned{SchemaVersion: 2},
	}
	return json.Marshal(content) // Get json content
}

func TestMain(m *testing.M) {
	const exampleTag = "latest"
	const exampleUploadUUid = "0bc84d80-837c-41d9-824e-1907463c53b3"

	// Setup example local target
	exampleMemoryStore = memory.New()
	layerBlob := []byte("Hello layer")
	ctx := context.Background()
	layerDesc, err := pushBlob(ctx, ocispec.MediaTypeImageLayer, layerBlob, exampleMemoryStore) // push layer blob
	if err != nil {
		panic(err)
	}
	configBlob := []byte("Hello config")
	configDesc, err := pushBlob(ctx, ocispec.MediaTypeImageConfig, configBlob, exampleMemoryStore) // push config blob
	if err != nil {
		panic(err)
	}
	manifestBlob, err := generateManifestContent(configDesc, layerDesc) // generate a image manifest
	if err != nil {
		panic(err)
	}
	manifestDesc, err := pushBlob(ctx, ocispec.MediaTypeImageManifest, manifestBlob, exampleMemoryStore) // push manifest blob
	if err != nil {
		panic(err)
	}
	err = exampleMemoryStore.Tag(ctx, manifestDesc, exampleTag)
	if err != nil {
		panic(err)
	}

	// Setup example remote target
	httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		m := r.Method
		switch {
		case strings.Contains(p, "/blobs/uploads/") && m == "POST":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Location", p+exampleUploadUUid)
			w.WriteHeader(http.StatusAccepted)
		case strings.Contains(p, "/blobs/uploads/"+exampleUploadUUid) && m == "GET":
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(p, "/manifests/"+string(exampleSignatureManifestDescriptor.Digest)):
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Docker-Content-Digest", string(exampleSignatureManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleSignatureManifest)))
			w.Write(exampleSignatureManifest)
		case strings.Contains(p, "/manifests/latest") && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(p, "/manifests/"+string(exampleManifestDescriptor.Digest)),
			strings.Contains(p, "/manifests/latest") && m == "HEAD":
			w.Header().Set("Content-Type", artifactspec.MediaTypeArtifactManifest)
			w.Header().Set("Docker-Content-Digest", string(exampleManifestDescriptor.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len(exampleManifest)))
			if m == "GET" {
				w.Write(exampleManifest)
			}
		case strings.Contains(p, "/artifacts/referrers"):
			w.Header().Set("ORAS-Api-Version", "oras/1.0")
			q := r.URL.Query()
			var referrers []ocispec.Descriptor
			if q.Get("digest") == exampleManifestDescriptor.Digest.String() {
				referrers = []ocispec.Descriptor{exampleSignatureManifestDescriptor}
			} else if q.Get("digest") == exampleSignatureManifestDescriptor.Digest.String() {
				referrers = []ocispec.Descriptor{}
			}
			result := struct {
				Referrers []ocispec.Descriptor `json:"referrers"`
			}{
				Referrers: referrers,
			}
			if err := json.NewEncoder(w).Encode(result); err != nil {
				panic(err)
			}
		case strings.Contains(p, "/manifests/") && (m == "HEAD" || m == "GET"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", string(manifestDesc.Digest))
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(manifestBlob))))
			w.Write([]byte(manifestBlob))
		case strings.Contains(p, "/blobs/") && (m == "GET" || m == "HEAD"):
			arr := strings.Split(p, "/")
			digest := arr[len(arr)-1]
			var desc ocispec.Descriptor
			var content []byte
			switch digest {
			case layerDesc.Digest.String():
				desc = layerDesc
				content = layerBlob
			case configDesc.Digest.String():
				desc = configDesc
				content = configBlob
			case manifestDesc.Digest.String():
				desc = manifestDesc
				content = manifestBlob
			}
			w.Header().Set("Content-Type", desc.MediaType)
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(content))))
			w.Write([]byte(content))
		case strings.Contains(p, "/manifests/") && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		}

	}))
	defer httpsServer.Close()
	u, err := url.Parse(httpsServer.URL)
	if err != nil {
		panic(err)
	}
	remoteHost = u.Host
	http.DefaultClient = httpsServer.Client()

	os.Exit(m.Run())
}

func ExampleCopy_remoteToRemote() {
	reg, err := remote.NewRegistry(remoteHost)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	src, err := reg.Repository(ctx, "source")
	if err != nil {
		panic(err) // Handle error
	}
	dst, err := reg.Repository(ctx, "target")
	if err != nil {
		panic(err) // Handle error
	}

	tagName := "latest"
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.DefaultCopyOptions)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:7cbb44b44e8ede5a89cf193db3f5f2fd019d89697e6b87e8ed2589e60649b0d1
}

func ExampleCopy_remoteToLocal() {
	reg, err := remote.NewRegistry(remoteHost)
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	src, err := reg.Repository(ctx, "source")
	if err != nil {
		panic(err) // Handle error
	}
	dst := memory.New()

	tagName := "latest"
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.DefaultCopyOptions)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:7cbb44b44e8ede5a89cf193db3f5f2fd019d89697e6b87e8ed2589e60649b0d1
}

func ExampleCopy_localToLocal() {
	src := exampleMemoryStore
	dst := memory.New()

	tagName := "latest"
	ctx := context.Background()
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.DefaultCopyOptions)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:7cbb44b44e8ede5a89cf193db3f5f2fd019d89697e6b87e8ed2589e60649b0d1
}

func ExampleCopy_localToOciFile() {
	src := exampleMemoryStore
	tempDir, err := os.MkdirTemp("", "oras_oci_example_*")
	if err != nil {
		panic(err) // Handle error
	}
	defer os.RemoveAll(tempDir)
	dst, err := oci.New(tempDir)
	if err != nil {
		panic(err) // Handle error
	}

	tagName := "latest"
	ctx := context.Background()
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.DefaultCopyOptions)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:7cbb44b44e8ede5a89cf193db3f5f2fd019d89697e6b87e8ed2589e60649b0d1
}

func ExampleCopy_localToRemote() {
	src := exampleMemoryStore
	reg, err := remote.NewRegistry(remoteHost)
	if err != nil {
		panic(err) // Handle error
	}
	ctx := context.Background()
	dst, err := reg.Repository(ctx, "target")
	if err != nil {
		panic(err) // Handle error
	}

	tagName := "latest"
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.DefaultCopyOptions)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:7cbb44b44e8ede5a89cf193db3f5f2fd019d89697e6b87e8ed2589e60649b0d1
}

// Example_copyArtifactManifestRemoteToLocal gives an example of copying
// an artifact manifest from a remote repository to local.
func Example_copyArtifactManifestRemoteToLocal() {
	src, err := remote.NewRepository(fmt.Sprintf("%s/source", remoteHost))
	if err != nil {
		panic(err)
	}
	dst := memory.New()
	ctx := context.Background()

	exampleDigest := "sha256:f9308ac4616a808210c12d049b4eb684754a5acf2c3c8d353a9fa2b3c47c274a"
	descriptor, err := src.Resolve(ctx, exampleDigest)
	if err != nil {
		panic(err)
	}
	err = oras.CopyGraph(ctx, src, dst, descriptor, oras.DefaultCopyGraphOptions)
	if err != nil {
		panic(err)
	}

	// verify that the artifact manifest described by the descriptor exists in dst
	contentExists, err := dst.Exists(ctx, descriptor)
	if err != nil {
		panic(err)
	}
	fmt.Println(contentExists)

	// Output:
	// true
}

// Example_extendedCopyArtifactAndReferrersRemoteToLocal gives an example of
// copying an artifact along with its referrers from a remote repository to local.
func Example_extendedCopyArtifactAndReferrersRemoteToLocal() {
	src, err := remote.NewRepository(fmt.Sprintf("%s/source", remoteHost))
	if err != nil {
		panic(err)
	}
	dst := memory.New()
	ctx := context.Background()

	tagName := "latest"
	// ExtendedCopy will copy the artifact tagged by "latest" along with all of its
	// referrers from src to dst.
	desc, err := oras.ExtendedCopy(ctx, src, tagName, dst, tagName, oras.DefaultExtendedCopyOptions)
	if err != nil {
		panic(err)
	}

	fmt.Println(desc.Digest)
	// Output:
	// sha256:1f3e679d4fc05dca20a699ae5af5fb2b7d481d5694aff929165d1c8b0f4c8598
}
