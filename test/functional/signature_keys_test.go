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
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// publicKeyBytes returns the binary serialization of the public keys of
// entities, concatenated into one keyring.
func publicKeyBytes(t *testing.T, entities ...*openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entities {
		if err := e.Serialize(&buf); err != nil {
			t.Fatalf("failed to serialize GPG public key: %v", err)
		}
	}
	return buf.Bytes()
}

// armoredPublicKey returns the ASCII-armored public key of entity.
func armoredPublicKey(t *testing.T, entity *openpgp.Entity) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("failed to start armor encoder: %v", err)
	}
	if err := entity.Serialize(w); err != nil {
		t.Fatalf("failed to serialize GPG public key: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to finish armor encoding: %v", err)
	}
	return buf.String()
}

// newKeyRepository returns a repository for repoName whose policy is req,
// with signatures looked up in store.
func newKeyRepository(t *testing.T, repoName string, store signature.SignatureStore, req *policy.PRSignedBy) *remote.Repository {
	t.Helper()
	req.KeyType = "GPGKeys"
	evaluator, err := policy.NewEvaluator(policy.NewPolicy().SetDefault(req), policy.WithSignedByVerifier(signature.NewSignedByVerifier(store)))
	if err != nil {
		t.Fatalf("failed to create policy evaluator: %v", err)
	}
	repo := newRepository(t, repoName)
	repo.Registry.Policy = evaluator
	return repo
}

// TestSignatureKeys verifies the ways a signedBy requirement can name its
// trusted keys, each against an image signed by one key.
func TestSignatureKeys(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, _ := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	signer := newGPGEntity(t, "Key Signer")
	other := newGPGEntity(t, "Other Key")
	another := newGPGEntity(t, "Another Key")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, desc, tag, signer)

	writeKeyring := func(t *testing.T, entities ...*openpgp.Entity) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "keyring.gpg")
		if err := os.WriteFile(path, publicKeyBytes(t, entities...), 0644); err != nil {
			t.Fatalf("failed to write keyring: %v", err)
		}
		return path
	}

	tests := []struct {
		name    string
		req     func(t *testing.T) *policy.PRSignedBy
		allowed bool
	}{
		{
			name: "keyData base64 binary key",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyData: base64.StdEncoding.EncodeToString(publicKeyBytes(t, signer))}
			},
			allowed: true,
		},
		{
			name: "keyData base64 armored key",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyData: base64.StdEncoding.EncodeToString([]byte(armoredPublicKey(t, signer)))}
			},
			allowed: true,
		},
		{
			name: "keyData raw armored key",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyData: armoredPublicKey(t, signer)}
			},
			allowed: true,
		},
		{
			name: "keyData for another key",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyData: base64.StdEncoding.EncodeToString(publicKeyBytes(t, other))}
			},
		},
		{
			name: "keyPath keyring with several keys including the signer",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyPath: writeKeyring(t, other, signer, another)}
			},
			allowed: true,
		},
		{
			name: "keyPaths including the signer",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyPaths: []string{writeGPGKeyFile(t, other), writeGPGKeyFile(t, signer)}}
			},
			allowed: true,
		},
		{
			name: "keyPaths without the signer",
			req: func(t *testing.T) *policy.PRSignedBy {
				return &policy.PRSignedBy{KeyPaths: []string{writeGPGKeyFile(t, other), writeGPGKeyFile(t, another)}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newKeyRepository(t, repoName, store, tt.req(t))
			_, err := repo.Resolve(ctx, tag)
			if tt.allowed {
				if err != nil {
					t.Fatalf("Resolve(%q) error = %v, want allowed", tag, err)
				}
				return
			}
			requireSignatureRejected(t, err, scope+":"+tag)
		})
	}
}

// TestSignatureKeys_UntrustedSignatureBeforeTrusted stores a signature from an
// untrusted key ahead of one from the trusted key and verifies the image is
// allowed: a signature that fails verification must not stop the search.
func TestSignatureKeys_UntrustedSignatureBeforeTrusted(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "v1"

	desc, _ := pushManifest(t, ctx, newRepository(t, repoName), tag, nil)

	trusted := newGPGEntity(t, "Trusted Signer")
	untrusted := newGPGEntity(t, "Untrusted Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, desc, tag, untrusted) // signature-1
	signImage(t, ctx, store, scope, desc, tag, trusted)   // signature-2

	sigs, err := store.GetSignatures(ctx, scope, desc.Digest)
	if err != nil || len(sigs) != 2 {
		t.Fatalf("test setup: GetSignatures = %d signatures, err %v; want 2", len(sigs), err)
	}

	repo := newKeyRepository(t, repoName, store, &policy.PRSignedBy{KeyPath: writeGPGKeyFile(t, trusted)})
	if _, err := repo.Resolve(ctx, tag); err != nil {
		t.Fatalf("Resolve(%q) error = %v, want the second, trusted signature to be accepted", tag, err)
	}
}
