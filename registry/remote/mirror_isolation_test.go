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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// Test_withMirrorFallbackExists_mirrorMissing_fallsToPrimary covers a mirror
// that answers authoritatively "not here". That is not an error, so the
// fallback loop has to distinguish it from a hit: content present on the
// primary but absent from the mirror must still be reported as existing.
func Test_withMirrorFallbackExists_mirrorMissing_fallsToPrimary(t *testing.T) {
	content := []byte("only on the primary")
	contentDigest := digest.FromBytes(content)

	// Mirror has nothing: HEAD 404 makes Exists return (false, nil).
	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
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

	mirrors := []mirrorRepository{
		{Repository: repoFromServer(t, mirrorServer), pullFromMirror: PullFromMirrorAll},
	}
	primaryRepo := repoFromServer(t, primaryServer)

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
		t.Error("content exists on the primary but Exists reported false after the mirror missed")
	}
}

// Test_withMirrorFallbackExists_allMiss reports false only once every mirror
// and the primary have been consulted.
func Test_withMirrorFallbackExists_allMiss(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()

	primaryHits := 0
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primaryServer.Close()

	mirrors := []mirrorRepository{
		{Repository: repoFromServer(t, notFound), pullFromMirror: PullFromMirrorAll},
	}

	target := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes([]byte("nowhere")),
		Size:      7,
	}

	ok, err := withMirrorFallbackExists(context.Background(), mirrors, repoFromServer(t, primaryServer), target,
		func(ctx context.Context, repo *Repository, t ocispec.Descriptor) (bool, error) {
			return repo.blobStore(t).Exists(ctx, t)
		})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ok {
		t.Error("expected exists to return false")
	}
	if primaryHits == 0 {
		t.Error("primary was never consulted after the mirror missed")
	}
}

func Test_isMirrorFallbackError_wrappedContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// The transport wraps context errors before they reach us, so the
		// bare-sentinel cases are not representative on their own.
		{"wrapped canceled", fmt.Errorf("Get %q: %w", "http://x/v2/", context.Canceled), false},
		{"wrapped deadline", fmt.Errorf("Get %q: %w", "http://x/v2/", context.DeadlineExceeded), false},
		{"doubly wrapped canceled", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.Canceled)), false},
		{"wrapped generic", fmt.Errorf("outer: %w", fmt.Errorf("connection refused")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMirrorFallbackError(tt.err); got != tt.want {
				t.Errorf("isMirrorFallbackError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// Test_buildCredentialFunc_scopedToItsOwnRegistry is the containment property:
// a credential configured for one registry must not be handed to any other
// host the client happens to talk to.
func Test_buildCredentialFunc_scopedToItsOwnRegistry(t *testing.T) {
	cred := credentials.Credential{Username: "user", Password: "secret"}
	builder := NewClientBuilder()
	props := &properties.Registry{
		Reference:  properties.Reference{Registry: "registry.example.com", Repository: "app"},
		Credential: cred,
	}

	credFunc := builder.buildCredentialFunc(props)

	got, err := credFunc(context.Background(), "registry.example.com")
	if err != nil {
		t.Fatalf("own registry: unexpected error %v", err)
	}
	if got != cred {
		t.Errorf("own registry: got %v, want the configured credential", got)
	}

	for _, other := range []string{"mirror.internal", "registry.example.com.evil.test", "evil.test"} {
		got, err := credFunc(context.Background(), other)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", other, err)
		}
		if got != credentials.EmptyCredential {
			t.Errorf("%s: leaked credential %v", other, got)
		}
	}
}

// Test_buildCredentialFunc_dockerIOAlias guards the host normalization: the
// credential func is called with the host the transport dials, which for the
// "docker.io" alias is "registry-1.docker.io".
func Test_buildCredentialFunc_dockerIOAlias(t *testing.T) {
	cred := credentials.Credential{Username: "user", Password: "secret"}
	builder := NewClientBuilder()
	props := &properties.Registry{
		Reference:  properties.Reference{Registry: "docker.io", Repository: "library/alpine"},
		Credential: cred,
	}

	got, err := builder.buildCredentialFunc(props)(context.Background(), "registry-1.docker.io")
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if got != cred {
		t.Errorf("got %v, want the configured credential for the docker.io alias host", got)
	}
}

// Test_buildMirrorRepositories_doesNotShareCredential checks the same property
// end to end: the client built for a mirror must not carry the primary's
// credential.
func Test_buildMirrorRepositories_doesNotShareCredential(t *testing.T) {
	cred := credentials.Credential{Username: "user", Password: "secret"}
	props := &properties.Registry{
		Reference:  properties.Reference{Registry: "registry.example.com", Repository: "app"},
		Credential: cred,
		Mirrors: []properties.Mirror{
			{Location: "mirror.internal"},
		},
	}

	mirrors, err := buildMirrorRepositories(props, NewClientBuilder())
	if err != nil {
		t.Fatalf("buildMirrorRepositories: %v", err)
	}
	if len(mirrors) != 1 {
		t.Fatalf("got %d mirrors, want 1", len(mirrors))
	}

	authClient, ok := mirrors[0].Registry.Client.(*auth.Client)
	if !ok {
		t.Fatalf("mirror client is %T, want *auth.Client", mirrors[0].Registry.Client)
	}
	got, err := authClient.CredentialFunc(context.Background(), "mirror.internal")
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if got != credentials.EmptyCredential {
		t.Errorf("mirror client resolved credential %v; the primary's credential must not reach a mirror", got)
	}
}
