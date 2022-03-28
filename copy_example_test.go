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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
)

var host string

func TestMain(m *testing.M) {
	// Setup mocked registries
	const exampleDigest = "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	const exampleTag = "latest"
	const exampleBlob = "Example blob content"
	httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		m := r.Method
		switch {
		case strings.HasSuffix(p, "/blobs/uploads/") && m == "GET":
			w.WriteHeader(http.StatusCreated)
		case (strings.HasSuffix(p, "/manifests/latest") || strings.HasSuffix(p, fmt.Sprintf("/manifests/%s", exampleDigest))) && m == "HEAD":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
		case strings.HasSuffix(p, fmt.Sprintf("/manifests/%s", exampleDigest)) && m == "GET":
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case strings.HasSuffix(p, fmt.Sprintf("/blobs/%s", exampleDigest)) && (m == "GET" || m == "HEAD"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Docker-Content-Digest", exampleDigest)
			w.Header().Set("Content-Length", strconv.Itoa(len([]byte(exampleBlob))))
			w.Write([]byte(exampleBlob))
		case (strings.HasSuffix(p, fmt.Sprintf("/manifests/%s", exampleDigest)) || strings.HasSuffix(p, fmt.Sprintf("/manifests/%s", exampleTag))) && m == "PUT":
			w.WriteHeader(http.StatusCreated)
		}

	}))
	defer httpsServer.Close()
	u, err := url.Parse(httpsServer.URL)
	if err != nil {
		panic(err)
	}
	host = u.Host
	http.DefaultClient = httpsServer.Client()
	os.Exit(m.Run())
}

func ExampleCopy() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	srcRepo, err := reg.Repository(ctx, "source")
	if err != nil {
		panic(err) // Handle error
	}
	tarRepo, err := reg.Repository(ctx, "target")
	if err != nil {
		panic(err) // Handle error
	}

	// We can copy via tag
	tagName := "latest"
	desc, err := oras.Copy(ctx, srcRepo, tagName, tarRepo, tagName)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Or copy via digest
	digest := "sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec"
	desc, err = oras.Copy(ctx, srcRepo, digest, tarRepo, digest)
	if err != nil {
		panic(err) // Handle error
	}
	fmt.Println(desc.Digest)

	// Or use a mix
	desc, err = oras.Copy(ctx, srcRepo, digest, tarRepo, tagName)
	//oras.Copy(ctx, srcRepo, tagName, tarRepo, digest)
	if err != nil {
		panic(err) // Handle error
	}

	fmt.Println(desc.Digest)

	// Output:
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
	// sha256:aafc6b9fa2094cbfb97eca0355105b9e8f5dfa1a4b3dbe9375a30b836f6db5ec
}
