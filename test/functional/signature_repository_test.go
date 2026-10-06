//go:build functional

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

package functional_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// The tests in this file drive signature verification through
// remote.Repository with Registry.Policy set, rather than calling the
// evaluator directly. A tag reference carries no digest, so the signedBy
// requirement is only evaluated after the tag resolves (checkPolicyResolved),
// against the resolved digest and the caller's tag.

// newGPGEntity generates an OpenPGP key pair for signing.
func newGPGEntity(t *testing.T, name string) *openpgp.Entity {
	t.Helper()
	entity, err := openpgp.NewEntity(name, "", strings.ToLower(strings.ReplaceAll(name, " ", "-"))+"@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate GPG entity: %v", err)
	}
	return entity
}

// newSignedByRepository returns a repository for repoName whose registry
// policy requires every image to be signed by the key at keyPath, with
// signatures looked up in store.
func newSignedByRepository(t *testing.T, repoName string, store signature.SignatureStore, keyPath string) *remote.Repository {
	t.Helper()
	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyPath,
	})
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(signature.NewSignedByVerifier(store)))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}
	repo := newRepository(t, repoName)
	repo.Registry.Policy = evaluator
	return repo
}

// requireSignatureRejected asserts that err reports a present signature that
// failed verification for ref, as opposed to a plain "no signature" denial.
func requireSignatureRejected(t *testing.T, err error, ref string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected signature verification to fail for %s, got a nil error", ref)
	}
	want := "no valid signature found for " + ref
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}

// assertSignedAccess resolves and fetches tag through repo and asserts that
// every path returns the expected manifest.
func assertSignedAccess(t *testing.T, ctx context.Context, repo *remote.Repository, tag string, want ocispec.Descriptor, wantBytes []byte) {
	t.Helper()

	got, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", tag, err)
	}
	if got.Digest != want.Digest {
		t.Fatalf("Resolve(%q) digest = %s, want %s", tag, got.Digest, want.Digest)
	}

	got, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		t.Fatalf("FetchReference(%q) error = %v", tag, err)
	}
	body, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("reading FetchReference(%q) body: %v", tag, err)
	}
	if got.Digest != want.Digest || !bytes.Equal(body, wantBytes) {
		t.Fatalf("FetchReference(%q) returned %s, want %s", tag, got.Digest, want.Digest)
	}

	fetchAndVerify(t, ctx, repo, want, wantBytes)
}

// TestSignatureRepository_SignedTagAllowed verifies that Resolve,
// FetchReference and Fetch succeed for a tag whose manifest carries a valid
// signature.
func TestSignatureRepository_SignedTagAllowed(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, manifestBytes := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	entity := newGPGEntity(t, "Repo Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, desc, tag, entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))
	assertSignedAccess(t, ctx, repo, tag, desc, manifestBytes)
}

// TestSignatureRepository_UnsignedTagRejected verifies that a tag with no
// signature is denied by Resolve and FetchReference, and that FetchReference
// does not hand back the manifest body.
func TestSignatureRepository_UnsignedTagRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, _ := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	entity := newGPGEntity(t, "Repo Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))

	_, err := repo.Resolve(ctx, tag)
	requirePolicyDenied(t, err, scope+":"+tag)

	_, rc, err := repo.FetchReference(ctx, tag)
	if rc != nil {
		rc.Close()
		t.Error("FetchReference returned a manifest body for an unsigned tag")
	}
	requirePolicyDenied(t, err, scope+":"+tag)

	_, err = repo.Fetch(ctx, desc)
	requirePolicyDenied(t, err, scope+"@"+desc.Digest.String())
}

// TestSignatureRepository_RetaggedToUnsignedRejected signs the manifest a tag
// points at, then moves the tag to a different, unsigned manifest. Resolving
// the tag must check the signature against the digest the registry returns
// now, not the one that was signed.
func TestSignatureRepository_RetaggedToUnsignedRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"
	plainRepo := newRepository(t, repoName)

	signedDesc, signedBytes := pushManifest(t, ctx, plainRepo, tag, nil)

	entity := newGPGEntity(t, "Repo Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, signedDesc, tag, entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))
	assertSignedAccess(t, ctx, repo, tag, signedDesc, signedBytes)

	// Move the tag to a different manifest, bypassing the policy.
	unsignedDesc, _ := pushManifest(t, ctx, plainRepo, tag, []layerData{
		{MediaType: ocispec.MediaTypeImageLayer, Content: []byte("unsigned layer")},
	})
	if unsignedDesc.Digest == signedDesc.Digest {
		t.Fatal("test setup: retagged manifest has the same digest as the signed one")
	}

	_, err := repo.Resolve(ctx, tag)
	requirePolicyDenied(t, err, scope+":"+tag)

	_, rc, err := repo.FetchReference(ctx, tag)
	if rc != nil {
		rc.Close()
		t.Error("FetchReference returned a manifest body for a retagged, unsigned manifest")
	}
	requirePolicyDenied(t, err, scope+":"+tag)

	// The originally signed manifest is still reachable by digest.
	fetchAndVerify(t, ctx, repo, signedDesc, signedBytes)
}

// TestSignatureRepository_SignedForOtherTagRejected signs a manifest for one
// tag and resolves it through another. The default identity rule
// (matchRepoDigestOrExact) requires the tag the caller asked for to be the one
// that was signed, so the caller's reference must reach the verifier rather
// than only the resolved digest.
func TestSignatureRepository_SignedForOtherTagRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	plainRepo := newRepository(t, repoName)

	desc, manifestBytes := pushManifest(t, ctx, plainRepo, "signed", nil)
	if err := plainRepo.Tag(ctx, desc, "other"); err != nil {
		t.Fatalf("failed to add second tag: %v", err)
	}

	entity := newGPGEntity(t, "Repo Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, desc, "signed", entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))
	assertSignedAccess(t, ctx, repo, "signed", desc, manifestBytes)

	_, err := repo.Resolve(ctx, "other")
	requireSignatureRejected(t, err, scope+":other")
}

// newHTTPLookaside serves dir as an HTTP lookaside store and returns its URL.
// Requests for which fail returns true get a 500 instead.
func newHTTPLookaside(t *testing.T, dir string, fail func(*http.Request) bool) string {
	t.Helper()
	files := http.FileServer(http.Dir(dir))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only lookaside", http.StatusMethodNotAllowed)
			return
		}
		if fail != nil && fail(r) {
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestSignatureRepository_HTTPLookaside_SignedTagAllowed writes a signature to
// a directory, serves that directory over HTTP, and verifies a tag through a
// repository whose verifier reads signatures from the HTTP URL.
func TestSignatureRepository_HTTPLookaside_SignedTagAllowed(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, manifestBytes := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	entity := newGPGEntity(t, "HTTP Signer")
	sigDir := t.TempDir()
	writer := signature.NewLookasideStore("", "file://"+sigDir)
	signImage(t, ctx, writer, scope, desc, tag, entity)

	readURL := newHTTPLookaside(t, sigDir, nil)
	reader := signature.NewLookasideStore(readURL, "")

	repo := newSignedByRepository(t, repoName, reader, writeGPGKeyFile(t, entity))
	assertSignedAccess(t, ctx, repo, tag, desc, manifestBytes)
}

// TestSignatureRepository_HTTPLookaside_NotFoundRejected verifies that a 404
// from an HTTP lookaside is treated as "unsigned": the image is denied, and
// the denial is a plain policy rejection rather than a fetch error.
func TestSignatureRepository_HTTPLookaside_NotFoundRejected(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	entity := newGPGEntity(t, "HTTP Signer")
	readURL := newHTTPLookaside(t, t.TempDir(), nil)
	reader := signature.NewLookasideStore(readURL, "")

	repo := newSignedByRepository(t, repoName, reader, writeGPGKeyFile(t, entity))
	_, err := repo.Resolve(ctx, tag)
	requirePolicyDenied(t, err, scope+":"+tag)
	if strings.Contains(err.Error(), "failed to fetch signatures") {
		t.Errorf("a 404 from the lookaside was reported as a fetch failure: %v", err)
	}
}

// TestSignatureRepository_HTTPLookaside_ServerErrorFailsClosed verifies that
// a lookaside server error denies access even when a valid signature exists,
// and that the error is surfaced rather than reported as unsigned content.
func TestSignatureRepository_HTTPLookaside_ServerErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, _ := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	entity := newGPGEntity(t, "HTTP Signer")
	sigDir := t.TempDir()
	writer := signature.NewLookasideStore("", "file://"+sigDir)
	signImage(t, ctx, writer, scope, desc, tag, entity)

	readURL := newHTTPLookaside(t, sigDir, func(*http.Request) bool { return true })
	reader := signature.NewLookasideStore(readURL, "")

	repo := newSignedByRepository(t, repoName, reader, writeGPGKeyFile(t, entity))
	_, err := repo.Resolve(ctx, tag)
	if err == nil {
		t.Fatal("expected Resolve to fail when the lookaside returns 500, got a nil error")
	}
	if !strings.Contains(err.Error(), "failed to fetch signatures") || !strings.Contains(err.Error(), "500") {
		t.Errorf("Resolve error = %v, want it to report the lookaside fetch failure with its status", err)
	}
}
