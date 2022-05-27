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
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

var exampleMemoryStore oras.Target
var remoteHost string

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
	configDesc, err := pushBlob(ctx, ocispec.MediaTypeImageLayer, configBlob, exampleMemoryStore) // push config blob
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
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.CopyOptions{})
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:6f2590d54af17afaca41a6243e3c01b368117d24b32e7581a6dee1d856dd3c4b
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
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.CopyOptions{})
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:6f2590d54af17afaca41a6243e3c01b368117d24b32e7581a6dee1d856dd3c4b
}

func ExampleCopy_localToLocal() {
	src := exampleMemoryStore
	dst := memory.New()

	tagName := "latest"
	ctx := context.Background()
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.CopyOptions{})
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:6f2590d54af17afaca41a6243e3c01b368117d24b32e7581a6dee1d856dd3c4b
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
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.CopyOptions{})
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:6f2590d54af17afaca41a6243e3c01b368117d24b32e7581a6dee1d856dd3c4b
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
	desc, err := oras.Copy(ctx, src, tagName, dst, tagName, oras.CopyOptions{})
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Output:
	// sha256:6f2590d54af17afaca41a6243e3c01b368117d24b32e7581a6dee1d856dd3c4b
}
