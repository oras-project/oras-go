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
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

var testReference = registry.Reference{
	Registry:   "localhost:5000",
	Repository: "test/repo",
}

func newRejectPolicyRepo(t *testing.T) *Repository {
	t.Helper()
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	return &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}
}

func assertPolicyDenied(t *testing.T, err error, operation string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s should fail due to reject policy", operation)
		return
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("%s error should mention access denied, got: %v", operation, err)
	}
}

func TestRepository_PolicyEnforcement(t *testing.T) {
	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// Test without policy - should work
	t.Run("no policy", func(t *testing.T) {
		err := repo.checkPolicy(context.Background(), "")
		if err != nil {
			t.Errorf("checkPolicy() without policy should not error, got: %v", err)
		}
	})

	// Test with reject policy
	t.Run("reject policy", func(t *testing.T) {
		pol := &policy.Policy{
			Default: policy.PolicyRequirements{&policy.Reject{}},
		}
		evaluator, err := policy.NewEvaluator(pol)
		if err != nil {
			t.Fatalf("failed to create evaluator: %v", err)
		}
		reg.Policy = evaluator

		err = repo.checkPolicy(context.Background(), "")
		if err == nil {
			t.Error("checkPolicy() with reject policy should error, got nil")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error should mention access denied, got: %v", err)
		}
	})

	// Test with accept policy
	t.Run("accept policy", func(t *testing.T) {
		pol := &policy.Policy{
			Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
		}
		evaluator, err := policy.NewEvaluator(pol)
		if err != nil {
			t.Fatalf("failed to create evaluator: %v", err)
		}
		reg.Policy = evaluator

		err = repo.checkPolicy(context.Background(), "")
		if err != nil {
			t.Errorf("checkPolicy() with accept policy should not error, got: %v", err)
		}
	})
}

func TestRepository_PolicyCheckedContext(t *testing.T) {
	// Verify that policyCheckedKey in context skips the check
	repo := newRejectPolicyRepo(t)

	// Without the key, policy should reject
	err := repo.checkPolicy(context.Background(), "")
	if err == nil {
		t.Error("checkPolicy() should fail without policyCheckedKey")
	}

	// With the key set, policy should be skipped
	ctx := withPolicyChecked(context.Background())
	err = repo.checkPolicy(ctx, "")
	if err != nil {
		t.Errorf("checkPolicy() should be skipped with policyCheckedKey, got: %v", err)
	}
}

func TestRepository_PolicyScope(t *testing.T) {
	// Verify that the scope is fully qualified (registry/repository)
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				testReference.Registry + "/" + testReference.Repository: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// The fully-qualified scope should match, allowing access
	err = repo.checkPolicy(context.Background(), "")
	if err != nil {
		t.Errorf("checkPolicy() should succeed with fully-qualified scope match, got: %v", err)
	}
}

func TestRepository_Clone_Policy(t *testing.T) {
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	original := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	cloned := original.clone()

	if cloned.Registry.Policy != original.Registry.Policy {
		t.Error("cloned repository should have the same policy evaluator via Registry")
	}
}

func TestRepository_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Fetch(context.Background(), desc)
	assertPolicyDenied(t, err, "Fetch()")
}

func TestRepository_Push_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Push(context.Background(), desc, strings.NewReader("test content"))
	assertPolicyDenied(t, err, "Push()")
}

func TestRepository_Resolve_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	_, err := repo.Resolve(context.Background(), "latest")
	assertPolicyDenied(t, err, "Resolve()")
}

func TestRepository_Delete_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Delete(context.Background(), desc)
	assertPolicyDenied(t, err, "Delete()")
}

func TestRepository_Tag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Tag(context.Background(), desc, "v1.0")
	assertPolicyDenied(t, err, "Tag()")
}

func TestRepository_Untag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Untag(context.Background(), "latest")
	assertPolicyDenied(t, err, "Untag()")
}

func TestRepository_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.PushReference(context.Background(), desc, strings.NewReader("test content"), "v1.0")
	assertPolicyDenied(t, err, "PushReference()")
}

func TestRepository_FetchReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	_, _, err := repo.FetchReference(context.Background(), "latest")
	assertPolicyDenied(t, err, "FetchReference()")
}

func TestRepository_Exists_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Exists(context.Background(), desc)
	assertPolicyDenied(t, err, "Exists()")
}

func TestRepository_Tags_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)

	err := repo.Tags(context.Background(), "", func(tags []string) error {
		return nil
	})
	assertPolicyDenied(t, err, "Tags()")
}

func TestRepository_Referrers_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Referrers(context.Background(), desc, "", func(referrers []ocispec.Descriptor) error {
		return nil
	})
	assertPolicyDenied(t, err, "Referrers()")
}

func TestRepository_Predecessors_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Predecessors(context.Background(), desc)
	assertPolicyDenied(t, err, "Predecessors()")
}

func TestRepository_Mount_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Mount(context.Background(), desc, "source/repo", nil)
	assertPolicyDenied(t, err, "Mount()")
}

func TestRepository_ManifestStore_PushReference_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Manifests().PushReference(context.Background(), desc, strings.NewReader("test content"), "v1.0")
	assertPolicyDenied(t, err, "Manifests().PushReference()")
}

func TestRepository_ManifestStore_Untag_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	err := repo.Manifests().Untag(context.Background(), "latest")
	assertPolicyDenied(t, err, "Manifests().Untag()")
}

func TestRepository_BlobStore_Fetch_PolicyCheck(t *testing.T) {
	repo := newRejectPolicyRepo(t)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	_, err := repo.Blobs().Fetch(context.Background(), desc)
	assertPolicyDenied(t, err, "Blobs().Fetch()")
}

func TestRepository_ScopeSpecificPolicy(t *testing.T) {
	// Test that scope-specific policies work correctly
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				// Allow all docker repositories
				"": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}

	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    evaluator,
	}
	repo := &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}

	// Since the policy allows docker transport, checkPolicy should succeed
	err = repo.checkPolicy(context.Background(), "")
	if err != nil {
		t.Errorf("checkPolicy() should succeed for allowed docker transport, got: %v", err)
	}
}

// mockReadCloser is a simple mock for testing
type mockReadCloser struct {
	io.Reader
	closed bool
}

func (m *mockReadCloser) Close() error {
	m.closed = true
	return nil
}

// recordingSignedByVerifier implements policy.SignedByVerifier and records
// the image references it is asked to verify.
type recordingSignedByVerifier struct {
	allow bool
	seen  []policy.ImageReference
}

func (v *recordingSignedByVerifier) Verify(ctx context.Context, req *policy.PRSignedBy, image policy.ImageReference) (bool, error) {
	v.seen = append(v.seen, image)
	return v.allow, nil
}

func newSignedByEvaluator(t *testing.T, verifier policy.SignedByVerifier) *policy.Evaluator {
	t.Helper()
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{
			&policy.PRSignedBy{KeyType: "GPGKeys", KeyPath: "/path/to/key.gpg"},
		},
	}
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(verifier))
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	return evaluator
}

func newSignedByPolicyRepo(t *testing.T, verifier policy.SignedByVerifier) *Repository {
	t.Helper()
	reg := &Registry{
		Reference: registry.Reference{Registry: testReference.Registry},
		Policy:    newSignedByEvaluator(t, verifier),
	}
	return &Repository{
		Registry:       reg,
		RepositoryName: testReference.Repository,
	}
}

// newSignedByTestServer starts a test registry serving a single index
// manifest under the tag "signed" and returns a policy-enforcing repository
// pointing at it. Fixes #1337: a signedBy requirement used to fail for any
// reference without an explicit digest.
func newSignedByTestServer(t *testing.T, verifier policy.SignedByVerifier) (*Repository, ocispec.Descriptor) {
	t.Helper()
	repo, desc, _ := newSignedByTestServerWithRequests(t, verifier)
	return repo, desc
}

func newSignedByTestServerWithRequests(t *testing.T, verifier policy.SignedByVerifier) (*Repository, ocispec.Descriptor, *int) {
	t.Helper()
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/v2/test/manifests/signed",
			"/v2/test/manifests/" + indexDesc.Digest.String():
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(indexDesc.Size)))
			if r.Method == http.MethodGet {
				w.Write(index)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
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
	repo.Registry.Policy = newSignedByEvaluator(t, verifier)
	return repo, indexDesc, &requests
}

func TestRepository_SignedByPolicy_TaglessOperation(t *testing.T) {
	// Operations without a manifest reference must not evaluate signature
	// requirements, which would otherwise always fail for lack of a digest.
	verifier := &recordingSignedByVerifier{allow: false}
	repo := newSignedByPolicyRepo(t, verifier)

	if err := repo.checkPolicy(context.Background(), ""); err != nil {
		t.Errorf("checkPolicy() with signedBy-only policy should defer signature checks, got: %v", err)
	}
	if len(verifier.seen) != 0 {
		t.Errorf("signature verifier should not be called without a manifest reference, got %d calls", len(verifier.seen))
	}
}

func TestRepository_ManifestDescriptorOperations_SignedByPolicy_Denied(t *testing.T) {
	tests := []struct {
		name      string
		operation func(context.Context, *Repository, ocispec.Descriptor) error
	}{
		{
			name: "Fetch",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				_, err := repo.Fetch(ctx, desc)
				return err
			},
		},
		{
			name: "Manifests().Fetch",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				_, err := repo.Manifests().Fetch(ctx, desc)
				return err
			},
		},
		{
			name: "Push",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				return repo.Push(ctx, desc, strings.NewReader(`{"manifests":[]}`))
			},
		},
		{
			name: "Manifests().Push",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				return repo.Manifests().Push(ctx, desc, strings.NewReader(`{"manifests":[]}`))
			},
		},
		{
			name: "Delete",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				repo.SetReferrersCapability(true)
				return repo.Delete(ctx, desc)
			},
		},
		{
			name: "Manifests().Delete",
			operation: func(ctx context.Context, repo *Repository, desc ocispec.Descriptor) error {
				repo.SetReferrersCapability(true)
				return repo.Manifests().Delete(ctx, desc)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := &recordingSignedByVerifier{allow: false}
			repo, desc, requests := newSignedByTestServerWithRequests(t, verifier)

			err := tt.operation(context.Background(), repo, desc)
			assertPolicyDenied(t, err, tt.name)
			if *requests != 0 {
				t.Errorf("registry requests = %d, want 0", *requests)
			}
			if len(verifier.seen) != 1 || verifier.seen[0].Digest != desc.Digest {
				t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, desc.Digest)
			}
		})
	}
}

func TestRepository_Resolve_SignedByPolicy_Tag(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: true}
	repo, indexDesc := newSignedByTestServer(t, verifier)

	got, err := repo.Resolve(context.Background(), "signed")
	if err != nil {
		t.Fatalf("Repository.Resolve() error = %v", err)
	}
	if got.Digest != indexDesc.Digest {
		t.Errorf("Repository.Resolve() = %v, want %v", got, indexDesc)
	}

	if len(verifier.seen) != 1 {
		t.Fatalf("signature verifier called %d times, want 1", len(verifier.seen))
	}
	if verifier.seen[0].Digest != indexDesc.Digest {
		t.Errorf("verifier got digest %q, want %q", verifier.seen[0].Digest, indexDesc.Digest)
	}
	wantReference := repo.Reference().String() + ":signed"
	if verifier.seen[0].Reference != wantReference {
		t.Errorf("verifier got reference %q, want %q", verifier.seen[0].Reference, wantReference)
	}
}

func TestRepository_DefaultSignedByVerifier_EndToEnd(t *testing.T) {
	repo, desc := newSignedByTestServer(t, &recordingSignedByVerifier{allow: true})
	imageReference := repo.Reference().String() + ":signed"

	signer, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("openpgp.NewEntity() error = %v", err)
	}
	payload := signature.NewSimpleSigningPayload(desc.Digest, imageReference)
	payloadBytes, err := payload.Marshal()
	if err != nil {
		t.Fatalf("payload.Marshal() error = %v", err)
	}
	signedData, err := signature.CreateOpenPGPSignature(payloadBytes, signer)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error = %v", err)
	}

	var publicKey bytes.Buffer
	if err := signer.Serialize(&publicKey); err != nil {
		t.Fatalf("signer.Serialize() error = %v", err)
	}
	storeURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(storeURL, storeURL)
	if err := store.PutSignature(context.Background(), repo.Reference().String(), desc.Digest, signedData); err != nil {
		t.Fatalf("PutSignature() error = %v", err)
	}
	pol, err := policy.NewEvaluator(&policy.Policy{
		Default: policy.PolicyRequirements{&policy.PRSignedBy{
			KeyType: "GPGKeys",
			KeyData: base64.StdEncoding.EncodeToString(publicKey.Bytes()),
		}},
	}, policy.WithSignedByVerifier(signature.NewSignedByVerifier(store)))
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	repo.Registry.Policy = pol

	got, err := repo.Resolve(context.Background(), "signed")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Digest != desc.Digest {
		t.Errorf("Resolve() = %v, want %v", got, desc)
	}
	rc, err := repo.Fetch(context.Background(), desc)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Fetch().Close() error = %v", err)
	}
}

func TestRepository_Resolve_SignedByPolicy_Denied(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo, _ := newSignedByTestServer(t, verifier)

	_, err := repo.Resolve(context.Background(), "signed")
	assertPolicyDenied(t, err, "Resolve()")
}

func TestRepository_Resolve_SignedByPolicy_DigestPreflight(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo, desc, requests := newSignedByTestServerWithRequests(t, verifier)
	reference := "@" + desc.Digest.String()

	_, err := repo.Resolve(context.Background(), reference)
	assertPolicyDenied(t, err, "Resolve()")
	if *requests != 0 {
		t.Errorf("registry requests = %d, want 0", *requests)
	}
	wantReference := repo.Reference().String() + reference
	if len(verifier.seen) != 1 || verifier.seen[0].Digest != desc.Digest {
		t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, desc.Digest)
	} else if verifier.seen[0].Reference != wantReference {
		t.Errorf("verifier got reference %q, want %q", verifier.seen[0].Reference, wantReference)
	}
}

func TestRepository_Resolve_SignedByPolicy_DigestChecksOnce(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: true}
	repo, desc, requests := newSignedByTestServerWithRequests(t, verifier)
	reference := "@" + desc.Digest.String()

	got, err := repo.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Digest != desc.Digest {
		t.Errorf("Resolve() = %v, want %v", got, desc)
	}
	if *requests != 1 {
		t.Errorf("registry requests = %d, want 1", *requests)
	}
	if len(verifier.seen) != 1 || verifier.seen[0].Digest != desc.Digest {
		t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, desc.Digest)
	}
}

func TestRepository_SignedByPolicy_MirrorPaths(t *testing.T) {
	tests := []struct {
		name      string
		operation func(context.Context, *Repository) (ocispec.Descriptor, io.ReadCloser, error)
	}{
		{
			name: "Resolve",
			operation: func(ctx context.Context, repo *Repository) (ocispec.Descriptor, io.ReadCloser, error) {
				desc, err := repo.Resolve(ctx, "signed")
				return desc, nil, err
			},
		},
		{
			name: "FetchReference",
			operation: func(ctx context.Context, repo *Repository) (ocispec.Descriptor, io.ReadCloser, error) {
				return repo.FetchReference(ctx, "signed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := &recordingSignedByVerifier{allow: true}
			primary, want, primaryRequests := newSignedByTestServerWithRequests(t, verifier)
			mirror, _, mirrorRequests := newSignedByTestServerWithRequests(t, verifier)
			primary.mirrors = []mirrorRepository{{Repository: mirror, pullFromMirror: PullFromMirrorAll}}

			got, rc, err := tt.operation(context.Background(), primary)
			if err != nil {
				t.Fatalf("%s() error = %v", tt.name, err)
			}
			if rc != nil {
				defer rc.Close()
			}
			if got.Digest != want.Digest {
				t.Errorf("%s() = %v, want %v", tt.name, got, want)
			}
			if *primaryRequests != 0 || *mirrorRequests != 1 {
				t.Errorf("registry requests = primary %d, mirror %d; want primary 0, mirror 1", *primaryRequests, *mirrorRequests)
			}
			if len(verifier.seen) != 1 || verifier.seen[0].Digest != want.Digest {
				t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, want.Digest)
			}
		})
	}
}

func TestRepository_FetchReference_SignedByPolicy_Tag(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: true}
	repo, indexDesc := newSignedByTestServer(t, verifier)

	got, rc, err := repo.FetchReference(context.Background(), "signed")
	if err != nil {
		t.Fatalf("Repository.FetchReference() error = %v", err)
	}
	defer rc.Close()
	if got.Digest != indexDesc.Digest {
		t.Errorf("Repository.FetchReference() = %v, want %v", got, indexDesc)
	}
	if len(verifier.seen) != 1 || verifier.seen[0].Digest != indexDesc.Digest {
		t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, indexDesc.Digest)
	}
}

func TestRepository_FetchReference_SignedByPolicy_Denied(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo, _ := newSignedByTestServer(t, verifier)

	_, _, err := repo.FetchReference(context.Background(), "signed")
	assertPolicyDenied(t, err, "FetchReference()")
}

func TestRepository_FetchReference_SignedByPolicy_DeniedClosesBody(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo := newSignedByPolicyRepo(t, verifier)
	manifest := []byte(`{"manifests":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	body := &mockReadCloser{Reader: strings.NewReader(string(manifest))}
	repo.Registry.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {desc.MediaType}, "Docker-Content-Digest": {desc.Digest.String()}},
			Body:          body,
			ContentLength: desc.Size,
			Request:       req,
		}, nil
	})}

	_, _, err := repo.FetchReference(context.Background(), "signed")
	assertPolicyDenied(t, err, "FetchReference()")
	if !body.closed {
		t.Error("response body was not closed after policy denial")
	}
}

func TestRepository_ManifestStore_FetchReference_UnknownContentLengthChecksPolicyOnce(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: true}
	repo := newSignedByPolicyRepo(t, verifier)
	manifest := []byte(`{"manifests":[]}`)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	repo.Registry.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(""))
		contentLength := desc.Size
		if req.Method == http.MethodGet {
			body = io.NopCloser(strings.NewReader(string(manifest)))
			contentLength = -1
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {desc.MediaType}, "Docker-Content-Digest": {desc.Digest.String()}},
			Body:          body,
			ContentLength: contentLength,
			Request:       req,
		}, nil
	})}

	got, rc, err := repo.Manifests().FetchReference(context.Background(), "signed")
	if err != nil {
		t.Fatalf("Manifests().FetchReference() error = %v", err)
	}
	defer rc.Close()
	if got.Digest != desc.Digest {
		t.Errorf("Manifests().FetchReference() = %v, want %v", got, desc)
	}
	if len(verifier.seen) != 1 || verifier.seen[0].Digest != desc.Digest {
		t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, desc.Digest)
	}
}

func TestRepository_ManifestStore_Resolve_SignedByPolicy_Denied(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo, _ := newSignedByTestServer(t, verifier)

	_, err := repo.Manifests().Resolve(context.Background(), "signed")
	assertPolicyDenied(t, err, "Manifests().Resolve()")
}

func TestRepository_Tag_SignedByPolicy_Denied(t *testing.T) {
	// The descriptor carries the digest, so signature requirements are
	// evaluated before any network access.
	verifier := &recordingSignedByVerifier{allow: false}
	repo := newSignedByPolicyRepo(t, verifier)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.Tag(context.Background(), desc, "v1.0")
	assertPolicyDenied(t, err, "Tag()")
	if len(verifier.seen) != 1 || verifier.seen[0].Digest != desc.Digest {
		t.Errorf("verifier calls = %v, want one call with digest %q", verifier.seen, desc.Digest)
	}
}

func TestRepository_PushReference_SignedByPolicy_Denied(t *testing.T) {
	verifier := &recordingSignedByVerifier{allow: false}
	repo := newSignedByPolicyRepo(t, verifier)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:      1234,
	}

	err := repo.PushReference(context.Background(), desc, strings.NewReader("test content"), "v1.0")
	assertPolicyDenied(t, err, "PushReference()")
}

func TestRepository_PolicyScope_TaggedEntryApplies(t *testing.T) {
	repoScope := testReference.Registry + "/" + testReference.Repository
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				repoScope + ":blocked": policy.PolicyRequirements{&policy.Reject{}},
			},
		},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	repo := &Repository{
		Registry: &Registry{
			Reference: registry.Reference{Registry: testReference.Registry},
			Policy:    evaluator,
		},
		RepositoryName: testReference.Repository,
	}

	if err := repo.checkPolicy(context.Background(), "blocked"); err == nil {
		t.Error("checkPolicy() on the tag named by the policy should be denied, got nil")
	}
	if err := repo.checkPolicy(context.Background(), "allowed"); err != nil {
		t.Errorf("checkPolicy() on another tag should be allowed, got: %v", err)
	}
}

func TestRepository_PolicyScope_DigestEntryApplies(t *testing.T) {
	const blocked = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	const other = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	repoScope := testReference.Registry + "/" + testReference.Repository
	pol := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportNameDocker: {
				repoScope + "@" + blocked: policy.PolicyRequirements{&policy.Reject{}},
			},
		},
	}
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}
	repo := &Repository{
		Registry: &Registry{
			Reference: registry.Reference{Registry: testReference.Registry},
			Policy:    evaluator,
		},
		RepositoryName: testReference.Repository,
	}

	if err := repo.checkPolicy(context.Background(), blocked); err == nil {
		t.Error("checkPolicy() on the digest named by the policy should be denied, got nil")
	}
	if err := repo.checkPolicy(context.Background(), other); err != nil {
		t.Errorf("checkPolicy() on another digest should be allowed, got: %v", err)
	}
}
