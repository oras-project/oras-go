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

package signature

import (
	"context"
	"os"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

// mockSignatureStore implements SignatureStore for testing.
type mockSignatureStore struct {
	sigs map[string]map[string][][]byte // ref -> digest_string -> signatures
}

func newMockStore() *mockSignatureStore {
	return &mockSignatureStore{
		sigs: make(map[string]map[string][][]byte),
	}
}

func (m *mockSignatureStore) addSignature(ref string, dgst digest.Digest, sig []byte) {
	if m.sigs[ref] == nil {
		m.sigs[ref] = make(map[string][][]byte)
	}
	m.sigs[ref][dgst.String()] = append(m.sigs[ref][dgst.String()], sig)
}

func (m *mockSignatureStore) GetSignatures(ctx context.Context, ref string, dgst digest.Digest) ([][]byte, error) {
	if d, ok := m.sigs[ref]; ok {
		return d[dgst.String()], nil
	}
	return nil, nil
}

func (m *mockSignatureStore) PutSignature(ctx context.Context, ref string, dgst digest.Digest, sig []byte) error {
	m.addSignature(ref, dgst, sig)
	return nil
}

func TestDefaultSignedByVerifier_Verify_Success(t *testing.T) {
	// Generate a test key.
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	// Create image reference and digest.
	imgDigest := digest.FromString("test image content")
	imgRef := "registry.example.com/repo:latest@" + imgDigest.String()
	scope := "registry.example.com/repo"

	// Create and sign payload.
	payload := NewSimpleSigningPayload(imgDigest, "registry.example.com/repo:latest")
	payloadBytes, err := payload.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	signedData, err := CreateOpenPGPSignature(payloadBytes, entity)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error: %v", err)
	}

	// Set up mock store with the signature.
	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	// Serialize the key for the policy requirement.
	keyFile := t.TempDir() + "/test.gpg"
	f, err := createKeyFile(t, entity, keyFile)
	if err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}
	_ = f

	// Create verifier and verify.
	verifier := NewSignedByVerifier(store)
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !result {
		t.Error("Verify() = false, want true")
	}
}

func TestDefaultSignedByVerifier_Verify_NoSignatures(t *testing.T) {
	store := newMockStore()
	verifier := NewSignedByVerifier(store)

	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)
	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	imgDigest := digest.FromString("test")
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo:latest@" + imgDigest.String(),
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if result {
		t.Error("Verify() = true, want false (no signatures)")
	}
}

func TestDefaultSignedByVerifier_Verify_WrongKey(t *testing.T) {
	// Sign with one key, verify with another.
	signer, _ := openpgp.NewEntity("Signer", "", "signer@example.com", nil)
	verifierKey, _ := openpgp.NewEntity("Verifier", "", "verifier@example.com", nil)

	imgDigest := digest.FromString("test image content")
	scope := "registry.example.com/repo"
	imgRef := scope + ":latest@" + imgDigest.String()

	payload := NewSimpleSigningPayload(imgDigest, scope+":latest")
	payloadBytes, _ := payload.Marshal()
	signedData, _ := CreateOpenPGPSignature(payloadBytes, signer)

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := t.TempDir() + "/wrong.gpg"
	createKeyFile(t, verifierKey, keyFile)

	verifier := NewSignedByVerifier(store)
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if result {
		t.Error("Verify() = true, want false (wrong key)")
	}
}

func TestDefaultSignedByVerifier_Verify_DigestMismatch(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	imgDigest := digest.FromString("real image")
	wrongDigest := digest.FromString("wrong image")
	scope := "registry.example.com/repo"
	imgRef := scope + ":latest@" + imgDigest.String()

	// Create payload with the wrong digest.
	payload := NewSimpleSigningPayload(wrongDigest, scope+":latest")
	payloadBytes, _ := payload.Marshal()
	signedData, _ := CreateOpenPGPSignature(payloadBytes, entity)

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(store)
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if result {
		t.Error("Verify() = true, want false (digest mismatch)")
	}
}

func TestDefaultSignedByVerifier_Verify_IdentityMismatch(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	imgDigest := digest.FromString("test image content")
	scope := "registry.example.com/repo"
	imgRef := scope + ":latest@" + imgDigest.String()

	// Create payload with a different reference.
	payload := NewSimpleSigningPayload(imgDigest, "registry.example.com/other:latest")
	payloadBytes, _ := payload.Marshal()
	signedData, _ := CreateOpenPGPSignature(payloadBytes, entity)

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(store)
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
		SignedIdentity: &config.SignedIdentity{
			Type: config.IdentityMatchExact,
		},
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if result {
		t.Error("Verify() = true, want false (identity mismatch)")
	}
}

func TestDefaultSignedByVerifier_Verify_NoDigestInRef(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)
	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	store := newMockStore()
	verifier := NewSignedByVerifier(store)
	req := &config.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo:latest", // No digest.
	}

	_, err := verifier.Verify(context.Background(), req, image)
	if err == nil {
		t.Fatal("Verify() should return error for reference without digest")
	}
}

func TestParseImageDigest(t *testing.T) {
	realDigest := digest.FromString("test content")
	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{
			name: "Valid digest reference",
			ref:  "registry.example.com/repo@" + realDigest.String(),
			want: realDigest.String(),
		},
		{
			name:    "Tag only reference",
			ref:     "registry.example.com/repo:latest",
			wantErr: true,
		},
		{
			name: "Tag and digest",
			ref:  "registry.example.com/repo:latest@" + realDigest.String(),
			want: realDigest.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImageDigest(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseImageDigest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("parseImageDigest() = %v, want %v", got, tt.want)
			}
		})
	}
}

// createKeyFile serializes a GPG entity to a file and returns the path.
func createKeyFile(t *testing.T, entity *openpgp.Entity, path string) (string, error) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := entity.SerializePrivate(f, nil); err != nil {
		return "", err
	}
	return path, nil
}
