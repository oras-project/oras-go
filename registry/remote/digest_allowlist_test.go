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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/errdef"
)

// disallowAlgorithm removes alg from the allowlist for the duration of the
// test. alg stays registered in go-digest, so this builds the "registered but
// not allowlisted" state that go-digest v1.0.0 offers no other way to reach:
// its registered set equals the allowlist and it has no RegisterAlgorithm.
// Tests in this package do not run in parallel, so swapping the package
// variable is safe.
func disallowAlgorithm(t *testing.T, alg digest.Algorithm) {
	t.Helper()
	if !alg.Available() {
		t.Fatalf("%s must be registered in go-digest for this test to mean anything", alg)
	}
	orig := isSupportedAlgorithm
	isSupportedAlgorithm = func(a digest.Algorithm) bool {
		return a != alg && orig(a)
	}
	t.Cleanup(func() { isSupportedAlgorithm = orig })
}

// assertUnsupported checks that err wraps errdef.ErrUnsupported and names
// where the rejected digest came from.
func assertUnsupported(t *testing.T, err error, source string) {
	t.Helper()
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), source) {
		t.Errorf("error = %v, want it to name the source %q", err, source)
	}
}

func TestCheckDigestAlgorithm_RegisteredNotAllowlisted(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)

	if err := checkDigestAlgorithm(digestSourceHeader, digest.SHA256.FromString("x")); err != nil {
		t.Errorf("checkDigestAlgorithm(sha256) error = %v, want nil", err)
	}
	err := checkDigestAlgorithm(digestSourceHeader, digest.SHA512.FromString("x"))
	assertUnsupported(t, err, digestSourceHeader)
}

func TestCheckReferenceDigestAlgorithm_RegisteredNotAllowlisted(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)

	repo, err := NewRepository("localhost:5000/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	ref, err := repo.ParseReference(digest.SHA512.FromString("x").String())
	if err != nil {
		t.Fatalf("ParseReference() error = %v; a registered digest must stay a valid reference", err)
	}
	err = checkReferenceDigestAlgorithm(ref)
	assertUnsupported(t, err, digestSourceReference)
}

// allowlistTestRegistry serves any manifest or blob with body, reporting
// headerDigest in Docker-Content-Digest when it is set, and counts requests.
type allowlistTestRegistry struct {
	t            *testing.T
	body         []byte
	headerDigest digest.Digest
	requests     atomic.Int32
}

func (r *allowlistTestRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	switch {
	case strings.HasPrefix(req.URL.Path, "/v2/test/manifests/"):
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
	case strings.HasPrefix(req.URL.Path, "/v2/test/blobs/"):
		w.Header().Set("Content-Type", "application/octet-stream")
	default:
		r.t.Errorf("unexpected access: %s %s", req.Method, req.URL)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.headerDigest != "" {
		w.Header().Set(headerDockerContentDigest, r.headerDigest.String())
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(r.body)))
	if req.Method == http.MethodGet {
		w.Write(r.body)
	}
}

func newAllowlistTestRepository(t *testing.T, reg *allowlistTestRegistry) *Repository {
	t.Helper()
	ts := httptest.NewServer(reg)
	t.Cleanup(ts.Close)
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	repo, err := NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.Registry.PlainHTTP = true
	return repo
}

// The registry names a registered but non-allowlisted algorithm in
// Docker-Content-Digest. Resolve, FetchReference and Exists must fail with
// ErrUnsupported instead of returning a descriptor that Fetch would then
// refuse halfway through a copy.
func TestManifest_RegisteredNotAllowlistedHeader(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)
	body := []byte(`{"layers":[]}`)
	reg := &allowlistTestRegistry{t: t, body: body, headerDigest: digest.SHA512.FromBytes(body)}
	repo := newAllowlistTestRepository(t, reg)
	ctx := context.Background()

	t.Run("Resolve by tag", func(t *testing.T) {
		_, err := repo.Resolve(ctx, "latest")
		assertUnsupported(t, err, digestSourceHeader)
	})
	t.Run("FetchReference by tag", func(t *testing.T) {
		_, _, err := repo.FetchReference(ctx, "latest")
		assertUnsupported(t, err, digestSourceHeader)
	})
	t.Run("Exists", func(t *testing.T) {
		target := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    digest.SHA256.FromBytes(body),
			Size:      int64(len(body)),
		}
		exists, err := repo.Exists(ctx, target)
		if exists {
			t.Error("Exists() = true, want false")
		}
		assertUnsupported(t, err, digestSourceHeader)
	})
}

// A manifest referenced by a non-allowlisted digest. Without a header, the
// GET path would hash the body with that algorithm, so the reference must be
// rejected before any request is sent.
func TestManifest_RegisteredNotAllowlistedReference(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)
	body := []byte(`{"layers":[]}`)
	reg := &allowlistTestRegistry{t: t, body: body} // headerless
	repo := newAllowlistTestRepository(t, reg)
	ctx := context.Background()
	ref := digest.SHA512.FromBytes(body).String()

	t.Run("FetchReference headerless GET", func(t *testing.T) {
		_, _, err := repo.FetchReference(ctx, ref)
		assertUnsupported(t, err, digestSourceReference)
	})
	t.Run("Resolve", func(t *testing.T) {
		_, err := repo.Resolve(ctx, ref)
		assertUnsupported(t, err, digestSourceReference)
	})
	if n := reg.requests.Load(); n != 0 {
		t.Errorf("registry received %d requests, want 0", n)
	}
}

func TestBlob_RegisteredNotAllowlistedReference(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)
	body := []byte("hello world")
	reg := &allowlistTestRegistry{t: t, body: body}
	repo := newAllowlistTestRepository(t, reg)
	ctx := context.Background()
	ref := digest.SHA512.FromBytes(body).String()

	t.Run("Resolve", func(t *testing.T) {
		_, err := repo.Blobs().Resolve(ctx, ref)
		assertUnsupported(t, err, digestSourceReference)
	})
	t.Run("FetchReference", func(t *testing.T) {
		_, _, err := repo.Blobs().FetchReference(ctx, ref)
		assertUnsupported(t, err, digestSourceReference)
	})
	if n := reg.requests.Load(); n != 0 {
		t.Errorf("registry received %d requests, want 0", n)
	}
}

func TestBlob_RegisteredNotAllowlistedHeader(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)
	body := []byte("hello world")
	reg := &allowlistTestRegistry{t: t, body: body, headerDigest: digest.SHA512.FromBytes(body)}
	repo := newAllowlistTestRepository(t, reg)

	_, err := repo.Blobs().Resolve(context.Background(), digest.SHA256.FromBytes(body).String())
	assertUnsupported(t, err, digestSourceHeader)
}

// Fetch, Delete and Mount take the digest from the caller's descriptor rather
// than from a reference, so they must check it themselves before sending a
// request.
func TestDescriptorDigest_RegisteredNotAllowlisted(t *testing.T) {
	disallowAlgorithm(t, digest.SHA512)
	body := []byte("hello world")
	reg := &allowlistTestRegistry{t: t, body: body}
	repo := newAllowlistTestRepository(t, reg)
	ctx := context.Background()
	blobDesc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.SHA512.FromBytes(body),
		Size:      int64(len(body)),
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.SHA512.FromBytes(body),
		Size:      int64(len(body)),
	}

	t.Run("blob Fetch", func(t *testing.T) {
		_, err := repo.Blobs().Fetch(ctx, blobDesc)
		assertUnsupported(t, err, digestSourceDescriptor)
	})
	t.Run("blob Delete", func(t *testing.T) {
		err := repo.Blobs().Delete(ctx, blobDesc)
		assertUnsupported(t, err, digestSourceDescriptor)
	})
	t.Run("blob Mount", func(t *testing.T) {
		err := repo.Mount(ctx, blobDesc, "other", nil)
		assertUnsupported(t, err, digestSourceDescriptor)
	})
	t.Run("manifest Fetch", func(t *testing.T) {
		_, err := repo.Manifests().Fetch(ctx, manifestDesc)
		assertUnsupported(t, err, digestSourceDescriptor)
	})
	t.Run("manifest Delete", func(t *testing.T) {
		err := repo.Manifests().Delete(ctx, manifestDesc)
		assertUnsupported(t, err, digestSourceDescriptor)
	})
	if n := reg.requests.Load(); n != 0 {
		t.Errorf("registry received %d requests, want 0", n)
	}
}
