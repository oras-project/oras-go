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
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// ErrSignatureVerification is returned when an OpenPGP signature cannot be verified.
var ErrSignatureVerification = errors.New("signature verification failed")

// KeyRing wraps an OpenPGP entity list for signature verification.
type KeyRing struct {
	entities openpgp.EntityList
}

// LoadKeyRing loads an OpenPGP keyring from the given key file paths and/or
// raw key data. Both armored and binary formats are supported.
func LoadKeyRing(keyPaths []string, keyDatas [][]byte) (*KeyRing, error) {
	var entities openpgp.EntityList

	for _, path := range keyPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read key file %s: %w", path, err)
		}
		keyDatas = append(keyDatas, data)
	}

	for _, data := range keyDatas {
		// Try armored format first, then binary.
		el, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
		if err != nil {
			el, err = openpgp.ReadKeyRing(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("failed to parse key data: %w", err)
			}
		}
		entities = append(entities, el...)
	}

	if len(entities) == 0 {
		return nil, fmt.Errorf("no keys found")
	}

	return &KeyRing{entities: entities}, nil
}

// VerifyOpenPGPSignature verifies an OpenPGP signed message and returns the
// embedded simple signing payload.
// The signedData should be an OpenPGP signed message (binary or clearsigned).
func VerifyOpenPGPSignature(signedData []byte, keyring *KeyRing) (*SimpleSigningPayload, error) {
	if keyring == nil || len(keyring.entities) == 0 {
		return nil, fmt.Errorf("%w: no keyring provided", ErrSignatureVerification)
	}

	md, err := openpgp.ReadMessage(bytes.NewReader(signedData), keyring.entities, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignatureVerification, err)
	}

	// Read the message body (the signed payload).
	body, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read message body: %v", ErrSignatureVerification, err)
	}

	// Check signature verification status.
	if md.SignatureError != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignatureVerification, md.SignatureError)
	}
	if md.SignedBy == nil {
		return nil, fmt.Errorf("%w: message was not signed", ErrSignatureVerification)
	}

	// Parse the payload.
	payload, err := ParseSimpleSigningPayload(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse signed payload: %w", err)
	}

	return payload, nil
}

// CreateOpenPGPSignature creates an OpenPGP signed message containing the
// given payload, signed with the first key in the entity list.
func CreateOpenPGPSignature(payload []byte, signer *openpgp.Entity) ([]byte, error) {
	if signer == nil {
		return nil, fmt.Errorf("no signer provided")
	}

	var buf bytes.Buffer
	w, err := openpgp.Sign(&buf, signer, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}

	if _, err := w.Write(payload); err != nil {
		return nil, fmt.Errorf("failed to write payload: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to close signer: %w", err)
	}

	return buf.Bytes(), nil
}
