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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func Test_isDigestReference(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		want      bool
	}{
		{"tag", "latest", false},
		{"tag with dots", "v1.2.3", false},
		{"digest with at sign", "repo@sha256:abc123", true},
		{"bare digest", "sha256:abc123def456", true},
		{"empty", "", false},
		{"localhost port no path", "localhost:5000", false},
		{"localhost port with path", "localhost:5000/repo", false},
		{"localhost port with tag", "localhost:5000/repo:latest", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDigestReference(tt.reference); got != tt.want {
				t.Errorf("isDigestReference(%q) = %v, want %v", tt.reference, got, tt.want)
			}
		})
	}
}

func Test_mirrorRepository_shouldUseForReference(t *testing.T) {
	tests := []struct {
		name           string
		pullFromMirror string
		reference      string
		want           bool
	}{
		{"all allows tag", PullFromMirrorAll, "latest", true},
		{"all allows digest", PullFromMirrorAll, "sha256:abc123", true},
		{"empty allows tag", "", "latest", true},
		{"empty allows digest", "", "sha256:abc123", true},
		{"digest-only allows digest", PullFromMirrorDigestOnly, "sha256:abc123", true},
		{"digest-only rejects tag", PullFromMirrorDigestOnly, "latest", false},
		{"tag-only allows tag", PullFromMirrorTagOnly, "latest", true},
		{"tag-only rejects digest", PullFromMirrorTagOnly, "sha256:abc123", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &mirrorRepository{
				Repository:     &Repository{},
				pullFromMirror: tt.pullFromMirror,
			}
			if got := m.shouldUseForReference(tt.reference); got != tt.want {
				t.Errorf("shouldUseForReference(%q) = %v, want %v", tt.reference, got, tt.want)
			}
		})
	}
}

func Test_isMirrorFallbackError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("connection refused"), true},
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMirrorFallbackError(tt.err); got != tt.want {
				t.Errorf("isMirrorFallbackError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// newTestServer creates a test HTTP server that serves a manifest.
func newTestServer(t *testing.T, manifestContent []byte, manifestDigest digest.Digest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/manifests/"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.WriteHeader(http.StatusOK)
			w.Write(manifestContent)
		case strings.Contains(path, "/blobs/"):
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.WriteHeader(http.StatusOK)
			w.Write(manifestContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// newFailServer creates a test HTTP server that always returns errors.
func newFailServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

// repoFromServer creates a Repository pointing at the given test server.
func repoFromServer(t *testing.T, server *httptest.Server) *Repository {
	t.Helper()
	// Extract host from server URL (strip http://)
	host := strings.TrimPrefix(server.URL, "http://")
	return &Repository{
		Registry: &Registry{
			Client:    server.Client(),
			Reference: properties.Reference{Registry: host},
			PlainHTTP: true,
		},
		RepositoryName: "test/repo",
	}
}

func Test_mirrorReference(t *testing.T) {
	dig := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name      string
		reference string
		want      string
	}{
		{"fully-qualified tag", "primary.example.com/library/app:v1", "v1"},
		{"fully-qualified digest", "primary.example.com/library/app@" + dig, "@" + dig},
		{"host:port with tag", "localhost:5001/charts/app:0.1.0", "0.1.0"},
		{"bare tag", "v1", "v1"},
		{"bare digest with @", "@" + dig, "@" + dig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mirrorReference(tt.reference); got != tt.want {
				t.Errorf("mirrorReference(%q) = %q, want %q", tt.reference, got, tt.want)
			}
		})
	}
}

// A fully-qualified reference carries the primary's registry host. A mirror
// Repository rejects a reference that does not match its own base, so the
// fallback must reduce it to a bare tag/digest before handing it to the mirror.
func Test_withMirrorFallbackFetchReference_retargetsFullyQualifiedRef(t *testing.T) {
	var gotMirrorRef string
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}

	desc, rc, err := withMirrorFallbackFetchReference(context.Background(), mirrors, &Repository{},
		"primary.example.com/library/app:v1",
		func(_ context.Context, _ *Repository, ref string) (ocispec.Descriptor, io.ReadCloser, error) {
			gotMirrorRef = ref
			return want, io.NopCloser(strings.NewReader("abc")), nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rc.Close()
	if desc.Digest != want.Digest {
		t.Errorf("got desc %v, want %v", desc, want)
	}
	if gotMirrorRef != "v1" {
		t.Errorf("mirror received ref %q, want bare tag %q", gotMirrorRef, "v1")
	}
}

func Test_withMirrorFallbackResolve_retargetsFullyQualifiedRef(t *testing.T) {
	var gotMirrorRef string
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}

	desc, err := withMirrorFallbackResolve(context.Background(), mirrors, &Repository{},
		"primary.example.com/library/app:v1",
		func(_ context.Context, _ *Repository, ref string) (ocispec.Descriptor, error) {
			gotMirrorRef = ref
			return want, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.Digest != want.Digest {
		t.Errorf("got desc %v, want %v", desc, want)
	}
	if gotMirrorRef != "v1" {
		t.Errorf("mirror received ref %q, want bare tag %q", gotMirrorRef, "v1")
	}
}

func Test_withMirrorFallbackResolve_mirrorSucceeds(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newTestServer(t, manifestJSON, manifestDigest)
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	mirrorRepo := repoFromServer(t, mirrorServer)
	primaryRepo := repoFromServer(t, primaryServer)

	mirrors := []mirrorRepository{
		{Repository: mirrorRepo, pullFromMirror: PullFromMirrorAll},
	}

	desc, err := withMirrorFallbackResolve(context.Background(), mirrors, primaryRepo, "latest",
		func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, error) {
			return repo.Manifests().Resolve(ctx, ref)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_withMirrorFallbackResolve_mirrorFails_fallsToPrimary(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newFailServer(t)
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, manifestJSON, manifestDigest)
	defer primaryServer.Close()

	mirrorRepo := repoFromServer(t, mirrorServer)
	primaryRepo := repoFromServer(t, primaryServer)

	mirrors := []mirrorRepository{
		{Repository: mirrorRepo, pullFromMirror: PullFromMirrorAll},
	}

	desc, err := withMirrorFallbackResolve(context.Background(), mirrors, primaryRepo, "latest",
		func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, error) {
			return repo.Manifests().Resolve(ctx, ref)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_withMirrorFallbackResolve_digestOnlyMirror_skipsTag(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newFailServer(t) // should not be called
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, manifestJSON, manifestDigest)
	defer primaryServer.Close()

	mirrorRepo := repoFromServer(t, mirrorServer)
	primaryRepo := repoFromServer(t, primaryServer)

	mirrors := []mirrorRepository{
		{Repository: mirrorRepo, pullFromMirror: PullFromMirrorDigestOnly},
	}

	// Tag reference should skip the digest-only mirror and go to primary.
	desc, err := withMirrorFallbackResolve(context.Background(), mirrors, primaryRepo, "latest",
		func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, error) {
			return repo.Manifests().Resolve(ctx, ref)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_withMirrorFallbackResolve_contextCanceled_noFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mirrors := []mirrorRepository{
		{
			Repository:     &Repository{Registry: &Registry{PlainHTTP: true}},
			pullFromMirror: PullFromMirrorAll,
		},
	}

	_, err := withMirrorFallbackResolve(ctx, mirrors, &Repository{}, "latest",
		func(ctx context.Context, repo *Repository, ref string) (ocispec.Descriptor, error) {
			return ocispec.Descriptor{}, ctx.Err()
		})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func Test_withMirrorFallbackFetch_mirrorSucceeds(t *testing.T) {
	content := []byte("hello mirror")
	contentDigest := digest.FromBytes(content)

	mirrorServer := newTestServer(t, content, contentDigest)
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	mirrorRepo := repoFromServer(t, mirrorServer)
	primaryRepo := repoFromServer(t, primaryServer)

	mirrors := []mirrorRepository{
		{Repository: mirrorRepo, pullFromMirror: PullFromMirrorAll},
	}

	target := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    contentDigest,
		Size:      int64(len(content)),
	}

	rc, err := withMirrorFallbackFetch(context.Background(), mirrors, primaryRepo, target,
		func(ctx context.Context, repo *Repository, t ocispec.Descriptor) (io.ReadCloser, error) {
			return repo.blobStore(t).Fetch(ctx, t)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if string(got) != string(content) {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func Test_withMirrorFallbackExists_mirrorSucceeds(t *testing.T) {
	content := []byte("exists test")
	contentDigest := digest.FromBytes(content)

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", contentDigest.String())
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	mirrorRepo := repoFromServer(t, mirrorServer)
	primaryRepo := repoFromServer(t, primaryServer)

	mirrors := []mirrorRepository{
		{Repository: mirrorRepo, pullFromMirror: PullFromMirrorAll},
	}

	target := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    contentDigest,
		Size:      int64(len(content)),
	}

	ok, err := withMirrorFallbackExists(context.Background(), mirrors, primaryRepo, target,
		func(ctx context.Context, repo *Repository, t ocispec.Descriptor) (bool, error) {
			return repo.blobStore(t).Exists(ctx, t)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Error("expected exists to return true")
	}
}

func Test_withMirrorFallbackFetch_mirrorFails_fallsToPrimary(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}
	target := ocispec.Descriptor{Digest: "sha256:abc"}

	rc, err := withMirrorFallbackFetch(context.Background(), mirrors, primary, target,
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (io.ReadCloser, error) {
			if repo == primary {
				return io.NopCloser(strings.NewReader("primary")), nil
			}
			return nil, fmt.Errorf("mirror unavailable")
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "primary" {
		t.Errorf("expected content from primary, got %q", got)
	}
}

func Test_withMirrorFallbackFetch_contextCanceled_noFallback(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}

	_, err := withMirrorFallbackFetch(context.Background(), mirrors, primary, ocispec.Descriptor{Digest: "sha256:abc"},
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (io.ReadCloser, error) {
			if repo == primary {
				t.Fatal("primary should not be called after non-retryable error")
			}
			return nil, context.Canceled
		})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func Test_withMirrorFallbackFetch_tagOnlyMirror_skipsDigest(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorTagOnly}}
	target := ocispec.Descriptor{Digest: "sha256:abc"}

	rc, err := withMirrorFallbackFetch(context.Background(), mirrors, primary, target,
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (io.ReadCloser, error) {
			if repo != primary {
				t.Fatal("tag-only mirror should be skipped for a digest target")
			}
			return io.NopCloser(strings.NewReader("primary")), nil
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "primary" {
		t.Errorf("expected content from primary, got %q", got)
	}
}

func Test_withMirrorFallbackFetchReference_mirrorFails_fallsToPrimary(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}

	desc, rc, err := withMirrorFallbackFetchReference(context.Background(), mirrors, primary, "latest",
		func(_ context.Context, repo *Repository, _ string) (ocispec.Descriptor, io.ReadCloser, error) {
			if repo == primary {
				return want, io.NopCloser(strings.NewReader("abc")), nil
			}
			return ocispec.Descriptor{}, nil, fmt.Errorf("mirror unavailable")
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	if desc.Digest != want.Digest {
		t.Errorf("expected digest %s, got %s", want.Digest, desc.Digest)
	}
}

func Test_withMirrorFallbackFetchReference_contextCanceled_noFallback(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}

	_, _, err := withMirrorFallbackFetchReference(context.Background(), mirrors, primary, "latest",
		func(_ context.Context, repo *Repository, _ string) (ocispec.Descriptor, io.ReadCloser, error) {
			if repo == primary {
				t.Fatal("primary should not be called after non-retryable error")
			}
			return ocispec.Descriptor{}, nil, context.Canceled
		})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func Test_withMirrorFallbackFetchReference_digestOnlyMirror_skipsTag(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorDigestOnly}}
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}

	desc, rc, err := withMirrorFallbackFetchReference(context.Background(), mirrors, primary, "latest",
		func(_ context.Context, repo *Repository, _ string) (ocispec.Descriptor, io.ReadCloser, error) {
			if repo != primary {
				t.Fatal("digest-only mirror should be skipped for a tag reference")
			}
			return want, io.NopCloser(strings.NewReader("abc")), nil
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	if desc.Digest != want.Digest {
		t.Errorf("expected digest %s, got %s", want.Digest, desc.Digest)
	}
}

func Test_withMirrorFallbackExists_mirrorFails_fallsToPrimary(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}
	target := ocispec.Descriptor{Digest: "sha256:abc"}

	ok, err := withMirrorFallbackExists(context.Background(), mirrors, primary, target,
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (bool, error) {
			if repo == primary {
				return true, nil
			}
			return false, fmt.Errorf("mirror unavailable")
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Error("expected exists to return true from primary")
	}
}

func Test_withMirrorFallbackExists_contextCanceled_noFallback(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}

	_, err := withMirrorFallbackExists(context.Background(), mirrors, primary, ocispec.Descriptor{Digest: "sha256:abc"},
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (bool, error) {
			if repo == primary {
				t.Fatal("primary should not be called after non-retryable error")
			}
			return false, context.Canceled
		})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func Test_withMirrorFallbackExists_tagOnlyMirror_skipsDigest(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorTagOnly}}
	target := ocispec.Descriptor{Digest: "sha256:abc"}

	ok, err := withMirrorFallbackExists(context.Background(), mirrors, primary, target,
		func(_ context.Context, repo *Repository, _ ocispec.Descriptor) (bool, error) {
			if repo != primary {
				t.Fatal("tag-only mirror should be skipped for a digest target")
			}
			return true, nil
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Error("expected exists to return true from primary")
	}
}

func Test_Repository_Fetch_usesMirror(t *testing.T) {
	content := []byte("mirror blob")
	contentDigest := digest.FromBytes(content)

	mirrorServer := newTestServer(t, content, contentDigest)
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
	}

	target := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    contentDigest,
		Size:      int64(len(content)),
	}

	rc, err := primary.Fetch(context.Background(), target)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(content) {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func Test_Repository_Exists_usesMirror(t *testing.T) {
	content := []byte("exists via mirror")
	contentDigest := digest.FromBytes(content)

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", contentDigest.String())
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
	}

	target := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    contentDigest,
		Size:      int64(len(content)),
	}

	ok, err := primary.Exists(context.Background(), target)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Error("expected exists to return true from mirror")
	}
}

func Test_Repository_Resolve_usesMirror(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newTestServer(t, manifestJSON, manifestDigest)
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
	}

	desc, err := primary.Resolve(context.Background(), "latest")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_Repository_FetchReference_usesMirror(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newTestServer(t, manifestJSON, manifestDigest)
	defer mirrorServer.Close()

	primaryServer := newFailServer(t)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
	}

	desc, rc, err := primary.FetchReference(context.Background(), "latest")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
	got, _ := io.ReadAll(rc)
	if string(got) != string(manifestJSON) {
		t.Errorf("expected manifest body %q, got %q", manifestJSON, got)
	}
}
