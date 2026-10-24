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
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/errdef"
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
	req := &policy.PRSignedBy{
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
	req := &policy.PRSignedBy{
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
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if result {
		t.Error("Verify() = true, want false (wrong key)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
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
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if result {
		t.Error("Verify() = true, want false (digest mismatch)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
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
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
		SignedIdentity: &policy.SignedIdentity{
			Type: policy.IdentityMatchExact,
		},
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if result {
		t.Error("Verify() = true, want false (identity mismatch)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
	}
}

func TestDefaultSignedByVerifier_Verify_NoDigestInRef(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)
	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	store := newMockStore()
	verifier := NewSignedByVerifier(store)
	req := &policy.PRSignedBy{
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
		{
			name:    "Malformed digest",
			ref:     "registry.example.com/repo@sha256:notahexdigest",
			wantErr: true,
		},
		{
			name:    "Unknown digest algorithm",
			ref:     "registry.example.com/repo@bogus:abc123",
			wantErr: true,
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

func TestNewSignedByVerifierFromConfig(t *testing.T) {
	cfg := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{
			"registry.example.com": {
				Lookaside: "https://sigstore.example.com/sigs",
			},
		},
	}

	verifier := NewSignedByVerifierFromConfig(cfg)
	if verifier == nil {
		t.Fatal("NewSignedByVerifierFromConfig() returned nil")
	}
	if verifier.store == nil {
		t.Error("NewSignedByVerifierFromConfig() verifier has no store")
	}
}

func TestNewSignedByVerifierFromConfig_NoMatch(t *testing.T) {
	cfg := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{},
	}

	verifier := NewSignedByVerifierFromConfig(cfg)
	if verifier == nil {
		t.Fatal("NewSignedByVerifierFromConfig() should never return nil")
	}

	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	if _, err := createKeyFile(t, entity, keyFile); err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}

	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "unknown.example.com/repo",
		Reference: "unknown.example.com/repo@" + digest.FromString("test").String(),
	}

	allowed, err := verifier.Verify(context.Background(), req, image)
	if allowed {
		t.Error("Verify() = true, want false")
	}
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Errorf("Verify() error = %v, want wrapping errdef.ErrUnsupported", err)
	}
}

func TestNewSignedByVerifierFromConfig_Nil(t *testing.T) {
	verifier := NewSignedByVerifierFromConfig(nil)
	if verifier == nil {
		t.Fatal("NewSignedByVerifierFromConfig(nil) should never return nil")
	}

	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	if _, err := createKeyFile(t, entity, keyFile); err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}

	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "any.example.com/repo",
		Reference: "any.example.com/repo@" + digest.FromString("test").String(),
	}

	allowed, err := verifier.Verify(context.Background(), req, image)
	if allowed {
		t.Error("Verify() = true, want false")
	}
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Errorf("Verify() error = %v, want wrapping errdef.ErrUnsupported", err)
	}
}

func TestSignedByVerifierFromConfig_MultiScope(t *testing.T) {
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	if _, err := createKeyFile(t, entity, keyFile); err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}

	scope1 := "registry1.example.com/repo"
	dgst1 := digest.FromString("scope1 content")
	ref1 := scope1 + ":v1@" + dgst1.String()
	payload1 := NewSimpleSigningPayload(dgst1, scope1+":v1")
	payload1Bytes, _ := payload1.Marshal()
	sig1, _ := CreateOpenPGPSignature(payload1Bytes, entity)

	scope2 := "registry2.example.com/repo"
	dgst2 := digest.FromString("scope2 content")
	ref2 := scope2 + ":v2@" + dgst2.String()
	payload2 := NewSimpleSigningPayload(dgst2, scope2+":v2")
	payload2Bytes, _ := payload2.Marshal()
	sig2, _ := CreateOpenPGPSignature(payload2Bytes, entity)

	scopeDefault := "fallback.example.com/repo"
	dgstDefault := digest.FromString("default content")
	refDefault := scopeDefault + ":v3@" + dgstDefault.String()
	payloadDefault := NewSimpleSigningPayload(dgstDefault, scopeDefault+":v3")
	payloadDefaultBytes, _ := payloadDefault.Marshal()
	sigDefault, _ := CreateOpenPGPSignature(payloadDefaultBytes, entity)

	sigPath1 := fmt.Sprintf("/scope1/%s@%s=%s/signature-1", scope1, dgst1.Algorithm(), dgst1.Hex())
	sigPath2 := fmt.Sprintf("/scope2/%s@%s=%s/signature-1", scope2, dgst2.Algorithm(), dgst2.Hex())
	sigPathDefault := fmt.Sprintf("/default/%s@%s=%s/signature-1", scopeDefault, dgstDefault.Algorithm(), dgstDefault.Hex())

	sigs := map[string][]byte{
		sigPath1:       sig1,
		sigPath2:       sig2,
		sigPathDefault: sigDefault,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, ok := sigs[r.URL.Path]; ok {
			w.Write(data)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	cfg := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{
			"registry1.example.com": {
				Lookaside: ts.URL + "/scope1",
			},
			"registry2.example.com": {
				Lookaside: ts.URL + "/scope2",
			},
		},
		DefaultDocker: &config.RegistriesDDockerConfig{
			Lookaside: ts.URL + "/default",
		},
	}

	verifier := NewSignedByVerifierFromConfig(cfg)
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}

	ctx := context.Background()

	// Scope 1 verifies against its own lookaside URL
	allowed, err := verifier.Verify(ctx, req, policy.ImageReference{
		Transport: "docker",
		Scope:     scope1,
		Reference: ref1,
		Digest:    dgst1,
	})
	if err != nil {
		t.Fatalf("Verify() scope 1 error: %v", err)
	}
	if !allowed {
		t.Error("Verify() scope 1 = false, want true")
	}

	// Scope 2 verifies against its own lookaside URL
	allowed, err = verifier.Verify(ctx, req, policy.ImageReference{
		Transport: "docker",
		Scope:     scope2,
		Reference: ref2,
		Digest:    dgst2,
	})
	if err != nil {
		t.Fatalf("Verify() scope 2 error: %v", err)
	}
	if !allowed {
		t.Error("Verify() scope 2 = false, want true")
	}

	// Scope not in Docker map falls back to default-docker
	allowed, err = verifier.Verify(ctx, req, policy.ImageReference{
		Transport: "docker",
		Scope:     scopeDefault,
		Reference: refDefault,
		Digest:    dgstDefault,
	})
	if err != nil {
		t.Fatalf("Verify() default-docker error: %v", err)
	}
	if !allowed {
		t.Error("Verify() default-docker = false, want true")
	}

	// When default-docker is not set, unconfigured scope returns errdef.ErrUnsupported
	cfgNoDefault := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{
			"registry1.example.com": {
				Lookaside: ts.URL + "/scope1",
			},
		},
	}
	verifierNoDefault := NewSignedByVerifierFromConfig(cfgNoDefault)
	allowed, err = verifierNoDefault.Verify(ctx, req, policy.ImageReference{
		Transport: "docker",
		Scope:     "unconfigured.example.com/repo",
		Reference: "unconfigured.example.com/repo@" + digest.FromString("unconfigured").String(),
	})
	if allowed {
		t.Error("Verify() unconfigured scope = true, want false")
	}
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Errorf("Verify() unconfigured scope error = %v, want wrapping errdef.ErrUnsupported", err)
	}
}

func TestDefaultSignedByVerifier_Verify_KeyRingError(t *testing.T) {
	verifier := NewSignedByVerifier(newMockStore())
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: filepath.Join(t.TempDir(), "missing.gpg"),
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo@" + digest.FromString("test").String(),
	}

	if _, err := verifier.Verify(context.Background(), req, image); err == nil {
		t.Fatal("Verify() should return error for an unreadable keyring")
	}
}

func TestDefaultSignedByVerifier_Verify_StoreError(t *testing.T) {
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	if _, err := createKeyFile(t, entity, keyFile); err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}

	verifier := NewSignedByVerifier(&errSignatureStore{err: errors.New("store unavailable")})
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo@" + digest.FromString("test").String(),
	}

	if _, err := verifier.Verify(context.Background(), req, image); err == nil {
		t.Fatal("Verify() should return error when the store fails")
	}
}

func TestDefaultSignedByVerifier_Verify_UnparseablePayload(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	imgDigest := digest.FromString("test image content")
	scope := "registry.example.com/repo"
	signedData, err := CreateOpenPGPSignature([]byte("not json"), entity)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error: %v", err)
	}

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(store)
	result, err := verifier.Verify(context.Background(), &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}, policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: scope + "@" + imgDigest.String(),
	})
	if result {
		t.Error("Verify() = true, want false (payload is not a signing payload)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
	}
}

func TestDefaultSignedByVerifier_Verify_InvalidPayload(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	imgDigest := digest.FromString("test image content")
	scope := "registry.example.com/repo"

	// A well-formed JSON payload that fails Validate() (wrong critical type).
	payload := NewSimpleSigningPayload(imgDigest, scope+":latest")
	payload.Critical.Type = "bogus type"
	payloadBytes, _ := payload.Marshal()
	signedData, _ := CreateOpenPGPSignature(payloadBytes, entity)

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(store)
	result, err := verifier.Verify(context.Background(), &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}, policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: scope + "@" + imgDigest.String(),
	})
	if result {
		t.Error("Verify() = true, want false (invalid payload)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
	}
}

func TestDefaultSignedByVerifier_Verify_IdentityMatchError(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	imgDigest := digest.FromString("test image content")
	scope := "registry.example.com/repo"

	payload := NewSimpleSigningPayload(imgDigest, scope+":latest")
	payloadBytes, _ := payload.Marshal()
	signedData, _ := CreateOpenPGPSignature(payloadBytes, entity)

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := filepath.Join(t.TempDir(), "test.gpg")
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(store)
	result, err := verifier.Verify(context.Background(), &policy.PRSignedBy{
		KeyType:        "GPGKeys",
		KeyPath:        keyFile,
		SignedIdentity: &policy.SignedIdentity{Type: "unknownType"},
	}, policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: scope + "@" + imgDigest.String(),
	})
	if result {
		t.Error("Verify() = true, want false (identity matching failed)")
	}
	// The signature was present but rejected; Verify reports why so that a
	// misconfiguration is distinguishable from genuinely unsigned content.
	if err == nil {
		t.Error("Verify() error = nil, want a rejection reason")
	}
}

func TestDefaultSignedByVerifier_LoadKeyRing_KeyData(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)

	var buf bytes.Buffer
	if err := entity.SerializePrivate(&buf, nil); err != nil {
		t.Fatalf("SerializePrivate() error: %v", err)
	}

	verifier := NewSignedByVerifier(newMockStore())

	t.Run("base64", func(t *testing.T) {
		req := &policy.PRSignedBy{
			KeyType: "GPGKeys",
			KeyData: base64.StdEncoding.EncodeToString(buf.Bytes()),
		}
		keyring, err := verifier.loadKeyRing(req)
		if err != nil {
			t.Fatalf("loadKeyRing() error: %v", err)
		}
		if len(keyring.entities) != 1 {
			t.Errorf("loadKeyRing() loaded %d entities, want 1", len(keyring.entities))
		}
	})

	t.Run("raw armored", func(t *testing.T) {
		var armored bytes.Buffer
		w, err := armor.Encode(&armored, openpgp.PublicKeyType, nil)
		if err != nil {
			t.Fatalf("armor.Encode() error: %v", err)
		}
		if err := entity.Serialize(w); err != nil {
			t.Fatalf("Serialize() error: %v", err)
		}
		w.Close()

		req := &policy.PRSignedBy{
			KeyType: "GPGKeys",
			KeyData: armored.String(),
		}
		keyring, err := verifier.loadKeyRing(req)
		if err != nil {
			t.Fatalf("loadKeyRing() error: %v", err)
		}
		if len(keyring.entities) != 1 {
			t.Errorf("loadKeyRing() loaded %d entities, want 1", len(keyring.entities))
		}
	})

	t.Run("key paths", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "extra.gpg")
		createKeyFile(t, entity, keyFile)

		req := &policy.PRSignedBy{
			KeyType:  "GPGKeys",
			KeyPaths: []string{keyFile},
		}
		keyring, err := verifier.loadKeyRing(req)
		if err != nil {
			t.Fatalf("loadKeyRing() error: %v", err)
		}
		if len(keyring.entities) != 1 {
			t.Errorf("loadKeyRing() loaded %d entities, want 1", len(keyring.entities))
		}
	})
}

// errSignatureStore is a SignatureStore whose operations always fail.
type errSignatureStore struct {
	err error
}

func (s *errSignatureStore) GetSignatures(ctx context.Context, ref string, dgst digest.Digest) ([][]byte, error) {
	return nil, s.err
}

func (s *errSignatureStore) PutSignature(ctx context.Context, ref string, dgst digest.Digest, sig []byte) error {
	return s.err
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

func TestDefaultSignedByVerifier_Verify_ResolvedDigest(t *testing.T) {
	// A tag reference paired with a resolved digest in ImageReference.Digest
	// must verify without a digest embedded in the reference itself.
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	imgDigest := digest.FromString("test image content")
	imgRef := "registry.example.com/repo:latest" // No digest in the reference.
	scope := "registry.example.com/repo"

	payload := NewSimpleSigningPayload(imgDigest, "registry.example.com/repo:latest")
	payloadBytes, err := payload.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	signedData, err := CreateOpenPGPSignature(payloadBytes, entity)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error: %v", err)
	}

	store := newMockStore()
	store.addSignature(scope, imgDigest, signedData)

	keyFile := t.TempDir() + "/test.gpg"
	if _, err := createKeyFile(t, entity, keyFile); err != nil {
		t.Fatalf("createKeyFile() error: %v", err)
	}

	verifier := NewSignedByVerifier(store)
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     scope,
		Reference: imgRef,
		Digest:    imgDigest,
	}

	result, err := verifier.Verify(context.Background(), req, image)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !result {
		t.Error("Verify() = false, want true")
	}
}

func TestDefaultSignedByVerifier_Verify_InvalidResolvedDigest(t *testing.T) {
	entity, _ := openpgp.NewEntity("Test", "", "test@example.com", nil)
	keyFile := t.TempDir() + "/test.gpg"
	createKeyFile(t, entity, keyFile)

	verifier := NewSignedByVerifier(newMockStore())
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
		KeyPath: keyFile,
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo:latest",
		Digest:    "not-a-digest",
	}

	_, err := verifier.Verify(context.Background(), req, image)
	if err == nil {
		t.Fatal("Verify() should return error for invalid resolved digest")
	}
}

func TestDefaultSignedByVerifier_Verify_NilReceiver(t *testing.T) {
	var v *DefaultSignedByVerifier
	req := &policy.PRSignedBy{
		KeyType: "GPGKeys",
	}
	image := policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo:latest",
	}

	allowed, err := v.Verify(context.Background(), req, image)
	if allowed {
		t.Error("Verify() = true, want false")
	}
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Errorf("Verify() error = %v, want wrapping errdef.ErrUnsupported", err)
	}
}

func TestDefaultSignedByVerifier_Verify_NilStore(t *testing.T) {
	tests := []struct {
		name     string
		verifier *DefaultSignedByVerifier
	}{
		{
			name:     "nil interface store",
			verifier: NewSignedByVerifier(nil),
		},
		{
			name:     "typed nil lookaside store",
			verifier: NewSignedByVerifier((*LookasideStore)(nil)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &policy.PRSignedBy{
				KeyType: "GPGKeys",
			}
			image := policy.ImageReference{
				Transport: "docker",
				Scope:     "registry.example.com/repo",
				Reference: "registry.example.com/repo:latest",
			}

			allowed, err := tt.verifier.Verify(context.Background(), req, image)
			if allowed {
				t.Error("Verify() = true, want false")
			}
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Errorf("Verify() error = %v, want wrapping errdef.ErrUnsupported", err)
			}
		})
	}
}

func TestTypedNilVerifierRepro(t *testing.T) {
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}
	var buf bytes.Buffer
	if err := entity.Serialize(&buf); err != nil {
		t.Fatalf("Serialize() error: %v", err)
	}
	keyData := base64.StdEncoding.EncodeToString(buf.Bytes())
	polPath := filepath.Join(t.TempDir(), "policy.json")
	pol := `{"default":[{"type":"signedBy","keyType":"GPGKeys","keyData":"` + keyData + `"}]}`
	if err := os.WriteFile(polPath, []byte(pol), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	p, err := policy.LoadPolicy(polPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}

	// registries.d with no lookaside for this scope -> verifier fails closed
	v := NewSignedByVerifierFromConfig(&config.RegistriesDConfig{})
	eval, err := policy.NewEvaluator(p, policy.WithSignedByVerifier(v))
	if err != nil {
		t.Fatalf("NewEvaluator() error: %v", err)
	}

	d := digest.FromString("x")
	allowed, err := eval.IsImageAllowed(context.Background(), policy.ImageReference{
		Transport: "docker",
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo@" + d.String(),
		Digest:    d,
	})
	if allowed {
		t.Error("IsImageAllowed() = true, want false")
	}
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Errorf("IsImageAllowed() error = %v, want wrapping errdef.ErrUnsupported", err)
	}
}
