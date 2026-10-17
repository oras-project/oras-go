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
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
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
	t.Run("unsupported algorithm rejects even without a header", func(t *testing.T) {
		// A headerless response must not let a non-allowlisted algorithm
		// skip verification by falling through to verifyContentDigest.
		req := httptest.NewRequest(http.MethodGet, "http://example.com/v2/test/manifests/"+expected.String(), nil)
		resp := &http.Response{Request: req, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
		err := store.verifyPullContentDigest(resp, digest.Digest("unvetted:abc123"))
		if !errors.Is(err, errdef.ErrUnsupported) {
			t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
		}
	})
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

func TestManifestPullHeaderlessContentLengthVerifiesBody(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	for _, algorithm := range []digest.Algorithm{digest.SHA256, digest.SHA512} {
		for _, corrupted := range []bool{false, true} {
			t.Run(algorithm.String()+"/corrupted="+strconv.FormatBool(corrupted), func(t *testing.T) {
				payload := bytes.Clone(manifest)
				if corrupted {
					payload[0] = 'x'
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
					_, _ = w.Write(payload)
				}))
				defer server.Close()
				repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
				if err != nil {
					t.Fatal(err)
				}
				repo.Registry.PlainHTTP = true
				repo.Registry.MaxMetadataBytes = 8
				target := ocispec.Descriptor{
					MediaType: ocispec.MediaTypeImageIndex,
					Digest:    algorithm.FromBytes(manifest),
					Size:      int64(len(manifest)),
				}
				reader, err := repo.Fetch(context.Background(), target)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				got, err := io.ReadAll(reader)
				if !bytes.Equal(got, payload) {
					t.Fatalf("body = %q, want %q", got, payload)
				}
				if corrupted {
					if !errors.Is(err, content.ErrMismatchedDigest) {
						t.Fatalf("read error = %v, want digest mismatch", err)
					}
				} else if err != nil {
					t.Fatalf("read error = %v, want nil", err)
				}
			})
		}
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
		w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		w.Header().Set("Docker-Content-Digest", digest.SHA256.FromBytes(manifest).String())
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
		// A registry reporting its own canonical algorithm on both methods.
		chunkedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", target.MediaType)
			w.Header().Set("Docker-Content-Digest", digest.SHA256.FromBytes(manifest).String())
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
				return
			}
			w.(http.Flusher).Flush()
			_, _ = w.Write(manifest)
		}))
		defer chunkedServer.Close()
		chunkedRepo, err := NewRepository(strings.TrimPrefix(chunkedServer.URL, "http://") + "/test")
		if err != nil {
			t.Fatal(err)
		}
		chunkedRepo.Registry.PlainHTTP = true
		// Identity comes from Resolve's HEAD, which cannot verify an alternate
		// algorithm, so FetchReference rejects what Fetch accepts.
		_, r, err := chunkedRepo.FetchReference(ctx, target.Digest.String())
		if r != nil {
			r.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("FetchReference on an unknown-length response = %v, want digest mismatch", err)
		}
		rc, err := chunkedRepo.Fetch(ctx, target)
		if err != nil {
			t.Fatalf("Fetch = %v, want success", err)
		}
		defer rc.Close()
		if body, err := io.ReadAll(rc); err != nil || string(body) != string(manifest) {
			t.Fatalf("Fetch body = %s, %v", body, err)
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

func tagPushTestDesc(alg digest.Algorithm, body []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    alg.FromBytes(body),
		Size:      int64(len(body)),
	}
}

func tagPushTestRepo(t *testing.T, handler http.Handler) *Repository {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	return repo
}

func TestManifestTagPushDigestAlgorithm(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"manifests":[]}`)
	sha256OfBody := digest.SHA256.FromBytes(body).String()
	ctx := context.Background()

	putOK := func(header string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			got, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("pushed body = %q, want %q", got, body)
			}
			if header != "" {
				w.Header().Set(headerDockerContentDigest, header)
			}
			w.WriteHeader(http.StatusCreated)
		})
	}

	for _, alg := range []digest.Algorithm{digest.SHA512, digest.SHA384} {
		t.Run(alg.String()+" by tag, registry reports sha256 of bytes", func(t *testing.T) {
			desc := tagPushTestDesc(alg, body)
			repo := tagPushTestRepo(t, putOK(sha256OfBody))
			if err := repo.PushReference(ctx, desc, bytes.NewReader(body), "latest"); err != nil {
				t.Fatalf("PushReference() = %v, want success", err)
			}
		})
	}

	t.Run("sha512 by tag, registry reports sha384 of bytes", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, putOK(digest.SHA384.FromBytes(body).String()))
		if err := repo.PushReference(ctx, desc, bytes.NewReader(body), "latest"); err != nil {
			t.Fatalf("PushReference() = %v, want success", err)
		}
	})

	t.Run("allowlisted algo but wrong digest", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, putOK(digest.SHA256.FromBytes([]byte("other")).String()))
		err := repo.PushReference(ctx, desc, bytes.NewReader(body), "latest")
		if err == nil {
			t.Fatal("PushReference() = nil, want mismatch")
		}
		if errors.Is(err, errdef.ErrUnsupported) {
			t.Fatalf("error = %v, want mismatch not ErrUnsupported", err)
		}
		var mismatch *digestHeaderMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("error = %v, want digestHeaderMismatchError", err)
		}
	})

	t.Run("sha1 header", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, putOK(unregisteredDigest.String()))
		err := repo.PushReference(ctx, desc, bytes.NewReader(body), "latest")
		if !errors.Is(err, errdef.ErrUnsupported) {
			t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
		}
	})

	t.Run("no header", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, putOK(""))
		if err := repo.PushReference(ctx, desc, bytes.NewReader(body), "latest"); err != nil {
			t.Fatalf("PushReference() = %v, want success", err)
		}
	})

	t.Run("Tag path", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+desc.Digest.String()):
				w.Header().Set("Content-Type", desc.MediaType)
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.Header().Set(headerDockerContentDigest, desc.Digest.String())
				_, _ = w.Write(body)
			case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/latest"):
				got, _ := io.ReadAll(r.Body)
				if !bytes.Equal(got, body) {
					t.Errorf("tagged body = %q, want %q", got, body)
				}
				w.Header().Set(headerDockerContentDigest, sha256OfBody)
				w.WriteHeader(http.StatusCreated)
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		if err := repo.Tag(ctx, desc, "latest"); err != nil {
			t.Fatalf("Tag() = %v, want success", err)
		}
	})

	t.Run("auth retry GetBody replay", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		var bodies [][]byte
		repo := tagPushTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			bodies = append(bodies, got)
			if user, pass, ok := r.BasicAuth(); !ok || user != "user" || pass != "pass" {
				w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !bytes.Equal(got, body) {
				t.Errorf("pushed body = %q, want %q", got, body)
			}
			w.Header().Set(headerDockerContentDigest, sha256OfBody)
			w.WriteHeader(http.StatusCreated)
		}))
		uri, err := url.Parse("http://" + repo.Reference().Registry)
		if err != nil {
			t.Fatal(err)
		}
		repo.Registry.Client = &auth.Client{
			Client: &http.Client{},
			CredentialFunc: credentials.StaticCredentialFunc(uri.Host, credentials.Credential{
				Username: "user",
				Password: "pass",
			}),
		}
		if err := repo.PushReference(ctx, desc, nonSeekableReader{bytes.NewReader(body)}, "latest"); err != nil {
			t.Fatalf("PushReference() = %v, want success", err)
		}
		if len(bodies) != 2 {
			t.Fatalf("body sent %d times, want 2", len(bodies))
		}
		for i, got := range bodies {
			if !bytes.Equal(got, body) {
				t.Errorf("body[%d] = %q, want %q", i, got, body)
			}
		}
	})

	t.Run("sha512 by digest, header sha256", func(t *testing.T) {
		desc := tagPushTestDesc(digest.SHA512, body)
		repo := tagPushTestRepo(t, putOK(sha256OfBody))
		err := repo.Manifests().Push(ctx, desc, bytes.NewReader(body))
		if !errors.Is(err, errdef.ErrUnsupported) {
			t.Fatalf("Manifests().Push() = %v, want errdef.ErrUnsupported", err)
		}
		if errors.Is(err, content.ErrMismatchedDigest) {
			t.Errorf("error wraps content.ErrMismatchedDigest, want only ErrUnsupported")
		}
		var mismatch *digestHeaderMismatchError
		if errors.As(err, &mismatch) {
			t.Errorf("error is generic mismatch %v, want algorithm ErrUnsupported", err)
		}
		if !strings.Contains(err.Error(), "sha256") || !strings.Contains(err.Error(), "sha512") {
			t.Errorf("error = %v, want both algorithms named", err)
		}

		err = repo.PushReference(ctx, desc, bytes.NewReader(body), desc.Digest.String())
		if !errors.Is(err, errdef.ErrUnsupported) {
			t.Fatalf("PushReference(digest) = %v, want errdef.ErrUnsupported", err)
		}
	})
}
