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
	_ "crypto/sha512"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
)

func TestManifestPullDigestValidation(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"manifests":[]}`)
	expected := digest.SHA512.FromBytes(body)
	repo, err := NewRepository("example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	store := &manifestStore{repo: repo}
	for _, tt := range []struct {
		name    string
		header  string
		body    []byte
		wantErr bool
	}{
		{"different algorithm", digest.SHA256.FromBytes(body).String(), body, false},
		{"corrupted body", digest.SHA256.FromBytes(body).String(), []byte("corrupted"), true},
		{"same algorithm mismatch", digest.SHA512.FromString("other").String(), body, true},
		{"malformed header", "sha256:invalid", body, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com/v2/test/manifests/"+expected.String(), nil)
			resp := &http.Response{Request: req, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(tt.body))}
			resp.Header.Set("Docker-Content-Digest", tt.header)
			defer func() { resp.Body.Close() }()
			if err := store.verifyPullContentDigest(resp, expected); (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			} else if tt.name == "corrupted body" {
				actual := expected.Algorithm().FromBytes(tt.body)
				if !errors.Is(err, content.ErrMismatchedDigest) || !strings.Contains(err.Error(), actual.String()) || !strings.Contains(err.Error(), expected.String()) {
					t.Fatalf("missing digest mismatch context: %v", err)
				}
			}
		})
	}
	t.Run("push remains strict", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "http://example.com/v2/test/manifests/"+expected.String(), nil)
		resp := &http.Response{Request: req, Header: http.Header{}}
		resp.Header.Set("Docker-Content-Digest", digest.SHA256.FromBytes(body).String())
		if err := verifyContentDigest(resp, expected); err == nil {
			t.Fatal("push accepted a different digest")
		}
	})
}

func TestManifestPullBodyLimits(t *testing.T) {
	body := []byte("manifest")
	for _, tt := range []struct {
		name    string
		limit   int64
		header  string
		payload []byte
		want    error
	}{
		{"exact limit", 8, digest.FromBytes(body).String(), body, nil},
		{"over limit", 7, digest.FromBytes(body).String(), body, errdef.ErrSizeExceedsLimit},
		{"headerless valid", 8, "", body, nil},
		{"headerless corrupt", 8, "", []byte("bad"), content.ErrMismatchedDigest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := NewRepository("example.com/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.MaxMetadataBytes = tt.limit
			resp := &http.Response{
				Request: httptest.NewRequest(http.MethodGet, "http://example.com/v2/test/manifests/latest", nil),
				Header:  http.Header{}, ContentLength: -1,
				Body: io.NopCloser(bytes.NewReader(tt.payload)),
			}
			defer func() { resp.Body.Close() }()
			resp.Header.Set("Docker-Content-Digest", tt.header)
			err = (&manifestStore{repo: repo}).verifyPullContentDigest(resp, digest.SHA512.FromBytes(body))
			if err == nil && tt.header == "" {
				_, err = io.ReadAll(resp.Body)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.want == errdef.ErrSizeExceedsLimit && errors.Is(err, content.ErrMismatchedDigest) {
				t.Fatal("size limit misreported as corruption")
			}
		})
	}
}

func TestManifestPullHeaderlessChunkedStreamsPastMetadataLimit(t *testing.T) {
	body := bytes.Repeat([]byte("manifest"), 700000)
	for _, tt := range []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{"valid", body, nil},
		{"corrupt", append(bytes.Clone(body[:len(body)-1]), 'x'), content.ErrMismatchedDigest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := NewRepository("example.com/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.MaxMetadataBytes = 8
			resp := &http.Response{
				Request:       httptest.NewRequest(http.MethodGet, "http://example.com/v2/test/manifests/latest", nil),
				Header:        http.Header{},
				ContentLength: -1,
				Body:          io.NopCloser(bytes.NewReader(tt.payload)),
			}
			defer resp.Body.Close()
			if err := (&manifestStore{repo: repo}).verifyPullContentDigest(resp, digest.SHA256.FromBytes(body)); err != nil {
				t.Fatalf("verification setup: %v", err)
			}
			got, err := io.ReadAll(resp.Body)
			if !bytes.Equal(got, tt.payload) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("read len=%d, error=%v; want len=%d, error=%v", len(got), err, len(tt.payload), tt.wantErr)
			}
		})
	}
}

func TestManifestPullBodyReadFailures(t *testing.T) {
	body := []byte("manifest")
	for _, tt := range []struct {
		name   string
		limit  int64
		reader io.Reader
	}{
		{"while reading body", int64(len(body)), &badReader{}},
		{"while checking size limit", int64(len(body)), io.MultiReader(bytes.NewReader(body), &badReader{})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := NewRepository("example.com/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.MaxMetadataBytes = tt.limit
			resp := &http.Response{
				Request:       httptest.NewRequest(http.MethodGet, "http://example.com/v2/test/manifests/latest", nil),
				Header:        http.Header{},
				ContentLength: -1,
				Body:          io.NopCloser(tt.reader),
			}
			resp.Header.Set("Docker-Content-Digest", digest.SHA256.FromBytes(body).String())
			err = (&manifestStore{repo: repo}).verifyPullContentDigest(resp, digest.SHA512.FromBytes(body))
			if err == nil || !strings.Contains(err.Error(), `GET "http://example.com/v2/test/manifests/latest": failed to read response body: read error`) {
				t.Fatalf("missing request and read-error context: %v", err)
			}
			if errors.Is(err, errdef.ErrSizeExceedsLimit) || errors.Is(err, content.ErrMismatchedDigest) {
				t.Fatalf("read error misclassified: %v", err)
			}
		})
	}
}

func TestManifestPullUnavailableDigestAlgorithm(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		w.Header().Set("Docker-Content-Digest", digest.SHA256.FromBytes(manifest).String())
		_, _ = w.Write(manifest)
	}))
	defer server.Close()
	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	target := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.Digest("unknown:" + strings.Repeat("a", 64)),
		Size:      int64(len(manifest)),
	}
	r, err := repo.Fetch(context.Background(), target)
	if r != nil {
		r.Close()
	}
	if err == nil {
		t.Fatal("Fetch accepted an unavailable digest algorithm")
	}
}

func TestManifestPullDifferentDigestAlgorithm(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	var chunked bool
	target := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.SHA512.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/test/manifests/"+target.Digest.String() {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", target.MediaType)
		if !chunked || r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		}
		w.Header().Set("Docker-Content-Digest", digest.SHA256.FromBytes(manifest).String())
		if chunked && r.Method == http.MethodHead {
			w.Header().Set("Docker-Content-Digest", target.Digest.String())
		}
		if chunked && r.Method == http.MethodGet {
			w.(http.Flusher).Flush()
		}
		if r.Method != http.MethodHead {
			_, _ = w.Write(manifest)
		}
	}))
	defer server.Close()
	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()
	t.Run("Resolve", func(t *testing.T) {
		_, err := repo.Resolve(ctx, target.Digest.String())
		if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("HEAD must reject an unverifiable alternate digest, got %v", err)
		}
	})
	t.Run("Fetch", func(t *testing.T) {
		r, err := repo.Fetch(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(manifest) {
			t.Fatalf("unexpected body: %s", body)
		}
	})
	t.Run("FetchReference", func(t *testing.T) {
		desc, r, err := repo.FetchReference(ctx, target.Digest.String())
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if desc.Digest != target.Digest {
			t.Fatalf("digest = %s, want %s", desc.Digest, target.Digest)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(manifest) {
			t.Fatalf("unexpected body: %s", body)
		}
	})
	t.Run("FetchReferenceChunked", func(t *testing.T) {
		chunked = true
		desc, r, err := repo.FetchReference(ctx, target.Digest.String())
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if desc.Digest != target.Digest {
			t.Fatalf("digest = %s, want %s", desc.Digest, target.Digest)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(manifest) {
			t.Fatalf("unexpected body: %s", body)
		}
	})
}

func TestManifestPullWithoutDigestHeader(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	for _, corrupted := range []bool{false, true} {
		t.Run(strconv.FormatBool(corrupted), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
				body := manifest
				if corrupted {
					body = []byte("corrupted")
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
			if err != nil {
				t.Fatal(err)
			}
			repo.Registry.PlainHTTP = true
			expected := digest.SHA512.FromBytes(manifest)
			desc, r, err := repo.FetchReference(context.Background(), expected.String())
			if r != nil {
				defer r.Close()
			}
			if (err != nil) != corrupted {
				t.Fatalf("error = %v, want error %v", err, corrupted)
			}
			if !corrupted {
				body, err := io.ReadAll(r)
				if err != nil || !bytes.Equal(body, manifest) || desc.Digest != expected {
					t.Fatalf("unexpected descriptor/body: %v, %s, %v", desc, body, err)
				}
			}
		})
	}
}

func TestManifestPullChunkedTagChanged(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
			w.Header().Set("Docker-Content-Digest", digest.FromString("changed").String())
			return
		}
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifest).String())
		w.(http.Flusher).Flush()
		_, _ = w.Write(manifest)
	}))
	defer server.Close()
	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	_, r, err := repo.FetchReference(context.Background(), "latest")
	if r != nil {
		r.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch after tag changed, got %v", err)
	}
}
