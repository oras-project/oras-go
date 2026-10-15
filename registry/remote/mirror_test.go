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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
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
		{"bare digest sha384", "sha384:abc123", true},
		{"algorithm outside the allowlist", "blake3:abc123", false},
		{"algorithm outside the allowlist with path", "blake3:abc123/repo", false},
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

// newSkippedServer creates a test HTTP server for a mirror that must be
// skipped. Unlike newFailServer, a request to it fails the test, since a
// failing mirror would be followed by a fallback that hides the request.
func newSkippedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to a skipped mirror: %s %s", r.Method, r.URL.Path)
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

// With no mirrors, the primary receives the reference unchanged, exactly once.
func Test_tryMirrorsRef_noMirrors(t *testing.T) {
	const reference = "primary.example.com/library/app:v1"
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}
	primary := &Repository{}
	var calls int
	var gotRepo *Repository
	var gotRef string

	desc, err := tryMirrorsRef(nil, primary, reference,
		func(repo *Repository, ref string) (ocispec.Descriptor, error) {
			calls++
			gotRepo = repo
			gotRef = ref
			return want, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.Digest != want.Digest {
		t.Errorf("got desc %v, want %v", desc, want)
	}
	if calls != 1 {
		t.Errorf("op called %d times, want 1", calls)
	}
	if gotRepo != primary {
		t.Errorf("op received repo %p, want primary %p", gotRepo, primary)
	}
	if gotRef != reference {
		t.Errorf("op received ref %q, want %q", gotRef, reference)
	}
}

// A mirror receives the reference reduced to a bare tag, and the primary it
// falls back to receives the reference unchanged.
func Test_tryMirrorsRef_retargetsMirrorOnly(t *testing.T) {
	const reference = "primary.example.com/library/app:v1"
	want := ocispec.Descriptor{Digest: "sha256:abc", Size: 3}
	mirror := &Repository{}
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: mirror, pullFromMirror: PullFromMirrorAll}}
	var gotMirrorRef, gotPrimaryRef string

	desc, err := tryMirrorsRef(mirrors, primary, reference,
		func(repo *Repository, ref string) (ocispec.Descriptor, error) {
			switch repo {
			case mirror:
				gotMirrorRef = ref
				return ocispec.Descriptor{}, errors.New("mirror unavailable")
			case primary:
				gotPrimaryRef = ref
				return want, nil
			}
			t.Fatalf("op received unexpected repo %p", repo)
			return ocispec.Descriptor{}, nil
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
	if gotPrimaryRef != reference {
		t.Errorf("primary received ref %q, want %q", gotPrimaryRef, reference)
	}
}

func Test_tryMirrors_mirrorFails_fallsToPrimary(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}
	op := func(repo *Repository) (string, error) {
		if repo == primary {
			return "primary", nil
		}
		return "", errors.New("mirror unavailable")
	}

	got, err := tryMirrors(mirrors, primary, "latest", op, op, acceptAll[string])
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "primary" {
		t.Errorf("expected result from primary, got %q", got)
	}
}

func Test_tryMirrors_contextCanceled_noFallback(t *testing.T) {
	primary := &Repository{}
	mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: PullFromMirrorAll}}
	op := func(repo *Repository) (string, error) {
		if repo == primary {
			t.Fatal("primary should not be called after non-retryable error")
		}
		return "", context.Canceled
	}

	_, err := tryMirrors(mirrors, primary, "latest", op, op, acceptAll[string])
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func Test_tryMirrors_skipsMirrorByPolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		reference string
	}{
		{"digest-only mirror skips tag", PullFromMirrorDigestOnly, "latest"},
		{"tag-only mirror skips digest", PullFromMirrorTagOnly, "sha256:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := &Repository{}
			mirrors := []mirrorRepository{{Repository: &Repository{}, pullFromMirror: tt.policy}}
			op := func(repo *Repository) (string, error) {
				if repo != primary {
					t.Fatal("mirror should be skipped by its pull policy")
				}
				return "primary", nil
			}

			got, err := tryMirrors(mirrors, primary, tt.reference, op, op, acceptAll[string])
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != "primary" {
				t.Errorf("expected result from primary, got %q", got)
			}
		})
	}
}

func Test_Repository_Resolve_mirrorFails_fallsToPrimary(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newFailServer(t)
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, manifestJSON, manifestDigest)
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

func Test_Repository_Resolve_digestOnlyMirror_skipsTag(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newSkippedServer(t)
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, manifestJSON, manifestDigest)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorDigestOnly},
	}

	desc, err := primary.Resolve(context.Background(), "latest")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_Repository_FetchReference_digestOnlyMirror_skipsTag(t *testing.T) {
	manifest := ocispec.Manifest{}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)

	mirrorServer := newSkippedServer(t)
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, manifestJSON, manifestDigest)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorDigestOnly},
	}

	desc, rc, err := primary.FetchReference(context.Background(), "latest")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close()
	if desc.Digest != manifestDigest {
		t.Errorf("expected digest %s, got %s", manifestDigest, desc.Digest)
	}
}

func Test_Repository_Fetch_tagOnlyMirror_skipsDigest(t *testing.T) {
	content := []byte("primary blob")
	contentDigest := digest.FromBytes(content)

	mirrorServer := newSkippedServer(t)
	defer mirrorServer.Close()

	primaryServer := newTestServer(t, content, contentDigest)
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorTagOnly},
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

func Test_Repository_Exists_tagOnlyMirror_skipsDigest(t *testing.T) {
	content := []byte("exists on primary")
	contentDigest := digest.FromBytes(content)

	mirrorServer := newSkippedServer(t)
	defer mirrorServer.Close()

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", contentDigest.String())
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primaryServer.Close()

	primary := repoFromServer(t, primaryServer)
	primary.mirrors = []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorTagOnly},
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

func Test_Repository_ReferrersIndex_ignoresMirror(t *testing.T) {
	// Without the Referrers API, the referrers index is read from the primary
	// only, as the Referrers API itself is. Pushing a referrer adds it to the
	// index and pushes the result back over the primary's index, so starting
	// from a stale mirror copy would drop the referrers the copy lacks.
	subject := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("subject"),
		Size:      int64(len("subject")),
	}
	older := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("older"),
		Size:      int64(len("older")),
	}
	newer := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("newer"),
		Size:      int64(len("newer")),
	}
	manifest, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.DescriptorEmptyJSON,
		Layers:    []ocispec.Descriptor{},
		Subject:   &subject,
	})
	if err != nil {
		t.Fatalf("failed to marshal referrer manifest: %v", err)
	}
	referrer := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	marshalIndex := func(manifests ...ocispec.Descriptor) []byte {
		index, err := json.Marshal(ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: manifests,
		})
		if err != nil {
			t.Fatalf("failed to marshal referrers index: %v", err)
		}
		return index
	}
	primaryIndex := marshalIndex(older, newer)
	mirrorIndex := marshalIndex(older)
	referrersTag, err := buildReferrersTag(subject)
	if err != nil {
		t.Fatalf("buildReferrersTag() error = %v", err)
	}
	serveIndex := func(w http.ResponseWriter, index []byte) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(index).String())
		w.Header().Set("Content-Length", strconv.Itoa(len(index)))
		w.Write(index)
	}
	digests := func(descs []ocispec.Descriptor) []digest.Digest {
		var res []digest.Digest
		for _, desc := range descs {
			res = append(res, desc.Digest)
		}
		return res
	}

	tests := []struct {
		name      string
		operation func(context.Context, *Repository) ([]ocispec.Descriptor, error)
		want      []digest.Digest
	}{
		{
			name: "Push",
			operation: func(ctx context.Context, repo *Repository) ([]ocispec.Descriptor, error) {
				return nil, repo.Push(ctx, referrer, bytes.NewReader(manifest))
			},
			want: []digest.Digest{older.Digest, newer.Digest, referrer.Digest},
		},
		{
			name: "Referrers",
			operation: func(ctx context.Context, repo *Repository) ([]ocispec.Descriptor, error) {
				var res []ocispec.Descriptor
				err := repo.Referrers(ctx, subject, "", func(referrers []ocispec.Descriptor) error {
					res = append(res, referrers...)
					return nil
				})
				return res, err
			},
			want: []digest.Digest{older.Digest, newer.Digest},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pushedIndex ocispec.Index
			primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == "/v2/test/repo/manifests/"+referrer.Digest.String():
					w.Header().Set("Docker-Content-Digest", referrer.Digest.String())
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodGet && r.URL.Path == "/v2/test/repo/manifests/"+referrersTag:
					serveIndex(w, primaryIndex)
				case r.Method == http.MethodPut && r.URL.Path == "/v2/test/repo/manifests/"+referrersTag:
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("failed to read referrers index: %v", err)
					}
					if err := json.Unmarshal(body, &pushedIndex); err != nil {
						t.Errorf("failed to unmarshal referrers index: %v", err)
					}
					w.Header().Set("Docker-Content-Digest", digest.FromBytes(body).String())
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodDelete && r.URL.Path == "/v2/test/repo/manifests/"+digest.FromBytes(primaryIndex).String():
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Errorf("unexpected primary access: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer primaryServer.Close()
			mirrorRequests := 0
			mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrorRequests++
				if r.Method == http.MethodGet && r.URL.Path == "/v2/test/repo/manifests/"+referrersTag {
					serveIndex(w, mirrorIndex)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer mirrorServer.Close()

			primary := repoFromServer(t, primaryServer)
			primary.mirrors = []mirrorRepository{
				{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
			}
			primary.SetReferrersCapability(false)

			got, err := tt.operation(context.Background(), primary)
			if err != nil {
				t.Fatalf("%s() error = %v", tt.name, err)
			}
			if tt.name == "Push" {
				got = pushedIndex.Manifests
			}
			if gotDigests := digests(got); !slices.Equal(gotDigests, tt.want) {
				t.Errorf("referrers = %v, want %v", gotDigests, tt.want)
			}
			if mirrorRequests != 0 {
				t.Errorf("mirror requests = %d, want none", mirrorRequests)
			}
		})
	}
}
