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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
)

// Test_DigestHeaderMismatch_SentinelOnPullOnly pins which call sites of
// verifyContentDigest report content.ErrMismatchedDigest: only pulls, where
// the registry served content that does not match the requested digest.
func Test_DigestHeaderMismatch_SentinelOnPullOnly(t *testing.T) {
	blob := []byte("hello")
	blobDesc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	for _, header := range []struct {
		name  string
		value string
	}{
		{"mismatching", digest.FromString("wrong").String()},
		{"malformed", "sha256:invalid"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(headerDockerContentDigest, header.value)
			switch {
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v2/test/blobs/"):
				w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
				w.Write(blob)
			case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, "/v2/test/blobs/"):
				w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v2/test/manifests/"):
				w.Header().Set("Content-Type", manifestDesc.MediaType)
				w.Write(manifest)
			case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v2/test/manifests/"):
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusCreated)
			case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/" && r.URL.Query().Get("mount") != "":
				w.WriteHeader(http.StatusCreated)
			case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v2/test/blobs/"):
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotImplemented)
			}
		}))
		uri, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		repo, err := NewRepository(uri.Host + "/test")
		if err != nil {
			t.Fatal(err)
		}
		repo.Registry.PlainHTTP = true
		repo.Registry.Client = &auth.Client{Client: &http.Client{}}
		ctx := context.Background()

		for _, tt := range []struct {
			name string
			pull bool
			call func() error
		}{
			{"blob Fetch", true, func() error {
				rc, err := repo.Blobs().Fetch(ctx, blobDesc)
				if err == nil {
					rc.Close()
				}
				return err
			}},
			{"blob Resolve", true, func() error {
				_, err := repo.Blobs().Resolve(ctx, blobDesc.Digest.String())
				return err
			}},
			{"manifest Fetch", true, func() error {
				rc, err := repo.Manifests().Fetch(ctx, manifestDesc)
				if err == nil {
					rc.Close()
				}
				return err
			}},
			{"manifest Push", false, func() error {
				return repo.Manifests().Push(ctx, manifestDesc, bytes.NewReader(manifest))
			}},
			{"blob Mount", false, func() error {
				return repo.Mount(ctx, blobDesc, "other", nil)
			}},
			{"blob Delete", false, func() error {
				return repo.Blobs().Delete(ctx, blobDesc)
			}},
		} {
			t.Run(header.name+" header/"+tt.name, func(t *testing.T) {
				err := tt.call()
				if err == nil {
					t.Fatal("error = nil, want an error")
				}
				want := tt.pull && header.name == "mismatching"
				if got := errors.Is(err, content.ErrMismatchedDigest); got != want {
					t.Errorf("errors.Is(%v, content.ErrMismatchedDigest) = %v, want %v", err, got, want)
				}
			})
		}
		srv.Close()
	}
}

func TestRepository_Push_ChunkedRegistryDigestMismatchIsNotSentinel(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, putDigestOverride: digest.FromBytes([]byte("wrong")).String()}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	blob := []byte("hello")
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	repo := newChunkedTestRepo(t, srv, 2)
	err := repo.Blobs().Push(context.Background(), desc, bytes.NewReader(blob))
	if err == nil {
		t.Fatal("expected error when registry returns a mismatching digest, got nil")
	}
	if errors.Is(err, content.ErrMismatchedDigest) {
		t.Errorf("errors.Is(%v, content.ErrMismatchedDigest) = true, want false on push", err)
	}
}
