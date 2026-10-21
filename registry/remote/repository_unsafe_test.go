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

package remote

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
)

// routingRegistry records manifest PUTs and serves blob uploads through the
// chunked mock; every HEAD answers 404.
type routingRegistry struct {
	t           *testing.T
	blobs       *chunkedUploadRegistry
	mu          sync.Mutex
	manifestPut []string
	manifest    []byte
}

func (reg *routingRegistry) handler() http.HandlerFunc {
	blobs := reg.blobs.handler()
	const mprefix = "/v2/test/manifests/"
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, mprefix):
			b, err := io.ReadAll(r.Body)
			if err != nil {
				reg.t.Errorf("read manifest body: %v", err)
			}
			ref := strings.TrimPrefix(r.URL.Path, mprefix)
			reg.mu.Lock()
			reg.manifestPut = append(reg.manifestPut, ref)
			reg.manifest = b
			reg.mu.Unlock()
			w.Header().Set("Docker-Content-Digest", ref)
			w.WriteHeader(http.StatusCreated)
		case strings.HasPrefix(r.URL.Path, mprefix):
			w.WriteHeader(http.StatusForbidden)
			reg.t.Errorf("unexpected manifest access: %s %s", r.Method, r.URL)
		default:
			blobs(w, r)
		}
	}
}

func (reg *routingRegistry) manifestSnapshot() ([]string, []byte) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return append([]string(nil), reg.manifestPut...), bytes.Clone(reg.manifest)
}

func newRoutingRepo(t *testing.T) (*Repository, *routingRegistry) {
	t.Helper()
	reg := &routingRegistry{t: t, blobs: &chunkedUploadRegistry{t: t}}
	srv := httptest.NewServer(reg.handler())
	t.Cleanup(srv.Close)
	return newChunkedTestRepo(t, srv, 4), reg
}

func TestRepository_UnsafePush_RoutesByMediaType(t *testing.T) {
	ctx := context.Background()

	t.Run("manifest", func(t *testing.T) {
		repo, reg := newRoutingRepo(t)
		manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[]}`)
		want := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
		got, err := repo.UnsafePush(ctx, ocispec.Descriptor{MediaType: want.MediaType, Size: -1}, bytes.NewReader(manifest))
		if err != nil {
			t.Fatalf("UnsafePush() error = %v", err)
		}
		if got.Digest != want.Digest || got.Size != want.Size || got.MediaType != want.MediaType {
			t.Errorf("UnsafePush() = %+v, want %+v", got, want)
		}
		puts, body := reg.manifestSnapshot()
		if len(puts) != 1 || puts[0] != want.Digest.String() || !bytes.Equal(body, manifest) {
			t.Errorf("manifest PUTs = %v, body %q; want [%s], body %q", puts, body, want.Digest, manifest)
		}
		if methods := reg.blobs.methodSequence(); len(methods) != 0 {
			t.Errorf("manifest push hit the blob upload API: %v", methods)
		}
	})

	t.Run("blob", func(t *testing.T) {
		repo, reg := newRoutingRepo(t)
		blob := []byte("hello world")
		got, err := repo.UnsafePush(ctx, ocispec.Descriptor{MediaType: "application/octet-stream", Size: -1}, bytes.NewReader(blob))
		if err != nil {
			t.Fatalf("UnsafePush() error = %v", err)
		}
		if got.Digest != digest.FromBytes(blob) || got.Size != int64(len(blob)) || got.MediaType != "application/octet-stream" {
			t.Errorf("UnsafePush() = %+v", got)
		}
		reg.blobs.mu.Lock()
		uploadDigest := reg.blobs.digestQuery
		uploaded := bytes.Clone(reg.blobs.uploaded)
		reg.blobs.mu.Unlock()
		if uploadDigest != digest.FromBytes(blob).String() || !bytes.Equal(uploaded, blob) {
			t.Errorf("blob upload digest %q, uploaded %q", uploadDigest, uploaded)
		}
		if methods := reg.blobs.methodSequence(); len(methods) == 0 {
			t.Error("blob push did not hit the blob upload API")
		}
		if puts, _ := reg.manifestSnapshot(); len(puts) != 0 {
			t.Errorf("blob push hit the manifest API: %v", puts)
		}
	})

	t.Run("custom manifest media type", func(t *testing.T) {
		repo, reg := newRoutingRepo(t)
		const mediaType = "application/vnd.example.manifest+json"
		repo.ManifestMediaTypes = []string{mediaType}
		manifest := []byte(`{"example":"manifest"}`)
		want := content.NewDescriptorFromBytes(mediaType, manifest)
		got, err := repo.UnsafePush(ctx, ocispec.Descriptor{MediaType: mediaType, Size: -1}, bytes.NewReader(manifest))
		if err != nil {
			t.Fatalf("UnsafePush() error = %v", err)
		}
		if got.Digest != want.Digest || got.Size != want.Size || got.MediaType != want.MediaType {
			t.Errorf("UnsafePush() = %+v, want %+v", got, want)
		}
		puts, body := reg.manifestSnapshot()
		if len(puts) != 1 || puts[0] != want.Digest.String() || !bytes.Equal(body, manifest) {
			t.Errorf("manifest PUTs = %v, body %q; want [%s], body %q", puts, body, want.Digest, manifest)
		}
		if methods := reg.blobs.methodSequence(); len(methods) != 0 {
			t.Errorf("manifest push hit the blob upload API: %v", methods)
		}
	})
}
