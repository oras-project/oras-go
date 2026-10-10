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
	"context"
	"fmt"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// signImageAs signs desc with a payload claiming dockerRef and stores the
// signature in store under scope. Unlike signImage, the claimed identity is
// independent of where the image lives, which is what the signedIdentity
// rules are about: an image pulled from a mirror or a staging repository
// still carries the identity it was signed under.
func signImageAs(t *testing.T, ctx context.Context, store *signature.LookasideStore, scope string, desc ocispec.Descriptor, dockerRef string, entity *openpgp.Entity) {
	t.Helper()
	payloadBytes, err := signature.NewSimpleSigningPayload(desc.Digest, dockerRef).Marshal()
	if err != nil {
		t.Fatalf("failed to marshal signing payload: %v", err)
	}
	sigData, err := signature.CreateOpenPGPSignature(payloadBytes, entity)
	if err != nil {
		t.Fatalf("failed to create OpenPGP signature: %v", err)
	}
	if err := store.PutSignature(ctx, scope, desc.Digest, sigData); err != nil {
		t.Fatalf("failed to store signature: %v", err)
	}
}

// newIdentityRepository returns a repository for repoName whose policy
// requires a signature from the key at keyPath matching identity.
func newIdentityRepository(t *testing.T, repoName string, store signature.SignatureStore, keyPath string, identity *policy.SignedIdentity) *remote.Repository {
	t.Helper()
	pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
		KeyType:        "GPGKeys",
		KeyPath:        keyPath,
		SignedIdentity: identity,
	})
	evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(signature.NewSignedByVerifier(store)))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}
	repo := newRepository(t, repoName)
	repo.Registry.Policy = evaluator
	return repo
}

// identityFixture is a pushed manifest with a fresh key and an empty
// file-based lookaside store.
type identityFixture struct {
	repoName string
	scope    string
	desc     ocispec.Descriptor
	entity   *openpgp.Entity
	keyPath  string
	store    *signature.LookasideStore
}

// newIdentityFixture pushes a manifest under each of tags and prepares a key
// and lookaside store for it.
func newIdentityFixture(t *testing.T, ctx context.Context, tags ...string) identityFixture {
	t.Helper()
	repoName := newRepoName(t)
	plainRepo := newRepository(t, repoName)
	desc, _ := pushManifest(t, ctx, plainRepo, tags[0], nil)
	for _, tag := range tags[1:] {
		if err := plainRepo.Tag(ctx, desc, tag); err != nil {
			t.Fatalf("failed to tag %s: %v", tag, err)
		}
	}
	entity := newGPGEntity(t, "Identity Signer")
	lookasideURL := "file://" + t.TempDir()
	return identityFixture{
		repoName: repoName,
		scope:    fmt.Sprintf("%s/%s", registryHost, repoName),
		desc:     desc,
		entity:   entity,
		keyPath:  writeGPGKeyFile(t, entity),
		store:    signature.NewLookasideStore(lookasideURL, lookasideURL),
	}
}

// TestSignatureIdentity_MatchExact verifies that matchExact accepts only the
// exact reference that was signed: the signed tag passes, while the same
// manifest by digest -- which the default rule accepts -- is rejected.
func TestSignatureIdentity_MatchExact(t *testing.T) {
	ctx := context.Background()
	f := newIdentityFixture(t, ctx, "v1")
	signImageAs(t, ctx, f.store, f.scope, f.desc, f.scope+":v1", f.entity)

	repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, &policy.SignedIdentity{Type: policy.IdentityMatchExact})
	if _, err := repo.Resolve(ctx, "v1"); err != nil {
		t.Fatalf("Resolve(v1) error = %v, want the exactly signed tag to be allowed", err)
	}

	digestRef := f.scope + "@" + f.desc.Digest.String()
	_, err := repo.Resolve(ctx, f.desc.Digest.String())
	requireSignatureRejected(t, err, digestRef)

	// The default rule accepts the digest reference for the same signature.
	defaultRepo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, nil)
	if _, err := defaultRepo.Resolve(ctx, f.desc.Digest.String()); err != nil {
		t.Errorf("Resolve(digest) with the default rule error = %v, want it allowed", err)
	}
}

// TestSignatureIdentity_MatchRepository verifies that matchRepository ignores
// the tag but not the repository.
func TestSignatureIdentity_MatchRepository(t *testing.T) {
	ctx := context.Background()
	f := newIdentityFixture(t, ctx, "v1", "other")
	identity := &policy.SignedIdentity{Type: policy.IdentityMatchRepository}

	signImageAs(t, ctx, f.store, f.scope, f.desc, f.scope+":v1", f.entity)
	repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, identity)
	if _, err := repo.Resolve(ctx, "other"); err != nil {
		t.Fatalf("Resolve(other) error = %v, want a signature for another tag in the same repository to be allowed", err)
	}

	// A signature naming a different repository is rejected even though it
	// covers this digest.
	g := newIdentityFixture(t, ctx, "v1")
	signImageAs(t, ctx, g.store, g.scope, g.desc, g.scope+"-elsewhere:v1", g.entity)
	repo = newIdentityRepository(t, g.repoName, g.store, g.keyPath, identity)
	_, err := repo.Resolve(ctx, "v1")
	requireSignatureRejected(t, err, g.scope+":v1")
}

// TestSignatureIdentity_ExactReference verifies that exactReference compares
// the signed identity to the configured reference, not to where the image is
// being pulled from -- the policy for consuming a signed release out of a
// mirror or staging repository.
func TestSignatureIdentity_ExactReference(t *testing.T) {
	ctx := context.Background()
	const released = "release.example.com/product/app:1.0"
	identity := &policy.SignedIdentity{Type: policy.IdentityMatchExactReference, DockerReference: released}

	f := newIdentityFixture(t, ctx, "staged")
	signImageAs(t, ctx, f.store, f.scope, f.desc, released, f.entity)
	repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, identity)
	if _, err := repo.Resolve(ctx, "staged"); err != nil {
		t.Fatalf("Resolve(staged) error = %v, want a signature for the configured reference to be allowed", err)
	}

	g := newIdentityFixture(t, ctx, "staged")
	signImageAs(t, ctx, g.store, g.scope, g.desc, "release.example.com/product/app:1.1", g.entity)
	repo = newIdentityRepository(t, g.repoName, g.store, g.keyPath, identity)
	_, err := repo.Resolve(ctx, "staged")
	requireSignatureRejected(t, err, g.scope+":staged")
}

// TestSignatureIdentity_ExactRepository verifies that exactRepository accepts
// any tag signed in the configured repository and nothing outside it.
func TestSignatureIdentity_ExactRepository(t *testing.T) {
	ctx := context.Background()
	const releasedRepo = "release.example.com/product/app"
	identity := &policy.SignedIdentity{Type: policy.IdentityMatchExactRepository, DockerRepository: releasedRepo}

	f := newIdentityFixture(t, ctx, "staged")
	signImageAs(t, ctx, f.store, f.scope, f.desc, releasedRepo+":2.3.4", f.entity)
	repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, identity)
	if _, err := repo.Resolve(ctx, "staged"); err != nil {
		t.Fatalf("Resolve(staged) error = %v, want a signature in the configured repository to be allowed", err)
	}

	g := newIdentityFixture(t, ctx, "staged")
	signImageAs(t, ctx, g.store, g.scope, g.desc, "release.example.com/product/other:2.3.4", g.entity)
	repo = newIdentityRepository(t, g.repoName, g.store, g.keyPath, identity)
	_, err := repo.Resolve(ctx, "staged")
	requireSignatureRejected(t, err, g.scope+":staged")
}

// TestSignatureIdentity_RemapIdentity verifies that remapIdentity rewrites the
// pulled reference's prefix into the signer's namespace before an exact
// comparison, and that the prefix only matches at a path boundary.
func TestSignatureIdentity_RemapIdentity(t *testing.T) {
	ctx := context.Background()
	const signedPrefix = "upstream.example.com/team"

	t.Run("remapped reference allowed", func(t *testing.T) {
		f := newIdentityFixture(t, ctx, "v1")
		// Remap this repository onto the signer's namespace: the pulled
		// "<scope>:v1" becomes "<signedPrefix>/app:v1".
		identity := &policy.SignedIdentity{Type: policy.IdentityMatchRemap, Prefix: f.scope, SignedPrefix: signedPrefix + "/app"}
		signImageAs(t, ctx, f.store, f.scope, f.desc, signedPrefix+"/app:v1", f.entity)
		repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, identity)
		if _, err := repo.Resolve(ctx, "v1"); err != nil {
			t.Fatalf("Resolve(v1) error = %v, want the remapped reference to be allowed", err)
		}
	})

	t.Run("remapped tag must match", func(t *testing.T) {
		f := newIdentityFixture(t, ctx, "v2")
		identity := &policy.SignedIdentity{Type: policy.IdentityMatchRemap, Prefix: f.scope, SignedPrefix: signedPrefix + "/app"}
		signImageAs(t, ctx, f.store, f.scope, f.desc, signedPrefix+"/app:v1", f.entity)
		repo := newIdentityRepository(t, f.repoName, f.store, f.keyPath, identity)
		_, err := repo.Resolve(ctx, "v2")
		requireSignatureRejected(t, err, f.scope+":v2")
	})

	t.Run("prefix stops at a path boundary", func(t *testing.T) {
		// The image lives in "<scope>-evil", a sibling of the configured
		// prefix "<scope>". A plain string prefix would remap it to
		// "<signedPrefix>-evil:v1" and accept a signature for that.
		f := newIdentityFixture(t, ctx, "v1")
		prefix := f.scope
		siblingName := f.repoName + "-evil"
		sibling := prefix + "-evil"
		desc, _ := pushManifest(t, ctx, newRepository(t, siblingName), "v1", nil)

		identity := &policy.SignedIdentity{Type: policy.IdentityMatchRemap, Prefix: prefix, SignedPrefix: signedPrefix}
		signImageAs(t, ctx, f.store, sibling, desc, signedPrefix+"-evil:v1", f.entity)
		repo := newIdentityRepository(t, siblingName, f.store, f.keyPath, identity)
		_, err := repo.Resolve(ctx, "v1")
		if err == nil {
			t.Fatalf("Resolve(v1) on %s succeeded, want the sibling repository not to match prefix %s", sibling, prefix)
		}
		requireSignatureRejected(t, err, sibling+":v1")
	})
}
