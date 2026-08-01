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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/opencontainers/go-digest"
)

func TestCreateAndVerifyOpenPGPSignature(t *testing.T) {
	// Generate a test key pair.
	entity, err := openpgp.NewEntity("Test User", "test", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	// Create a payload.
	dgst := digest.FromString("test content")
	ref := "registry.example.com/repo:latest"
	payload := NewSimpleSigningPayload(dgst, ref)
	payloadBytes, err := payload.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	// Sign it.
	signedData, err := CreateOpenPGPSignature(payloadBytes, entity)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error: %v", err)
	}

	if len(signedData) == 0 {
		t.Fatal("CreateOpenPGPSignature() returned empty data")
	}

	// Verify it.
	keyring := &KeyRing{entities: openpgp.EntityList{entity}}
	verified, err := VerifyOpenPGPSignature(signedData, keyring)
	if err != nil {
		t.Fatalf("VerifyOpenPGPSignature() error: %v", err)
	}

	if verified.Critical.Image.DockerManifestDigest != dgst.String() {
		t.Errorf("digest = %v, want %v", verified.Critical.Image.DockerManifestDigest, dgst.String())
	}
	if verified.Critical.Identity.DockerReference != ref {
		t.Errorf("reference = %v, want %v", verified.Critical.Identity.DockerReference, ref)
	}
}

func TestVerifyOpenPGPSignature_WrongKey(t *testing.T) {
	// Generate two different key pairs.
	signer, err := openpgp.NewEntity("Signer", "", "signer@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}
	other, err := openpgp.NewEntity("Other", "", "other@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	// Sign with the signer.
	payload := []byte(`{"critical":{"type":"atomic container signature","image":{"docker-manifest-digest":"sha256:abc"},"identity":{"docker-reference":"ref"}}}`)
	signedData, err := CreateOpenPGPSignature(payload, signer)
	if err != nil {
		t.Fatalf("CreateOpenPGPSignature() error: %v", err)
	}

	// Try to verify with the wrong key.
	keyring := &KeyRing{entities: openpgp.EntityList{other}}
	_, err = VerifyOpenPGPSignature(signedData, keyring)
	if err == nil {
		t.Fatal("VerifyOpenPGPSignature() should return error for wrong key")
	}
	if !errors.Is(err, ErrSignatureVerification) {
		t.Errorf("error should wrap ErrSignatureVerification, got: %v", err)
	}
}

func TestVerifyOpenPGPSignature_NilKeyring(t *testing.T) {
	_, err := VerifyOpenPGPSignature([]byte("data"), nil)
	if err == nil {
		t.Fatal("VerifyOpenPGPSignature() should return error for nil keyring")
	}
}

func TestVerifyOpenPGPSignature_InvalidData(t *testing.T) {
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	keyring := &KeyRing{entities: openpgp.EntityList{entity}}
	_, err = VerifyOpenPGPSignature([]byte("not a pgp message"), keyring)
	if err == nil {
		t.Fatal("VerifyOpenPGPSignature() should return error for invalid data")
	}
}

func TestCreateOpenPGPSignature_NilSigner(t *testing.T) {
	_, err := CreateOpenPGPSignature([]byte("payload"), nil)
	if err == nil {
		t.Fatal("CreateOpenPGPSignature() should return error for nil signer")
	}
}

func TestLoadKeyRing_FromData(t *testing.T) {
	// Generate a key and serialize it.
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	var buf []byte
	w, err := os.CreateTemp(t.TempDir(), "key-*.gpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		t.Fatalf("SerializePrivate() error: %v", err)
	}
	w.Close()

	buf, err = os.ReadFile(w.Name())
	if err != nil {
		t.Fatal(err)
	}

	kr, err := LoadKeyRing(nil, [][]byte{buf})
	if err != nil {
		t.Fatalf("LoadKeyRing() error: %v", err)
	}
	if len(kr.entities) == 0 {
		t.Error("LoadKeyRing() returned empty keyring")
	}
}

func TestLoadKeyRing_FromFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate and save a key.
	entity, err := openpgp.NewEntity("Test", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("NewEntity() error: %v", err)
	}

	keyPath := filepath.Join(tmpDir, "test.gpg")
	f, err := os.Create(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.SerializePrivate(f, nil); err != nil {
		t.Fatalf("SerializePrivate() error: %v", err)
	}
	f.Close()

	kr, err := LoadKeyRing([]string{keyPath}, nil)
	if err != nil {
		t.Fatalf("LoadKeyRing() error: %v", err)
	}
	if len(kr.entities) == 0 {
		t.Error("LoadKeyRing() returned empty keyring")
	}
}

func TestLoadKeyRing_MissingFile(t *testing.T) {
	_, err := LoadKeyRing([]string{"/nonexistent/key.gpg"}, nil)
	if err == nil {
		t.Fatal("LoadKeyRing() should return error for missing file")
	}
}

func TestLoadKeyRing_InvalidData(t *testing.T) {
	_, err := LoadKeyRing(nil, [][]byte{[]byte("not a key")})
	if err == nil {
		t.Fatal("LoadKeyRing() should return error for invalid key data")
	}
}

func TestLoadKeyRing_Empty(t *testing.T) {
	_, err := LoadKeyRing(nil, nil)
	if err == nil {
		t.Fatal("LoadKeyRing() should return error for no keys")
	}
}
