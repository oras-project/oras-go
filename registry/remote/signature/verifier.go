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
	"encoding/base64"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

// DefaultSignedByVerifier implements policy.SignedByVerifier by fetching
// signatures from a SignatureStore, verifying OpenPGP signatures, validating
// the payload digest, and applying identity matching rules.
type DefaultSignedByVerifier struct {
	store SignatureStore
}

// NewSignedByVerifier creates a new DefaultSignedByVerifier with the given
// signature store.
func NewSignedByVerifier(store SignatureStore) *DefaultSignedByVerifier {
	return &DefaultSignedByVerifier{store: store}
}

// NewSignedByVerifierFromConfig creates a DefaultSignedByVerifier using
// lookaside storage configured in registries.d for the given image scope.
// Returns nil if no lookaside URL is configured for the scope.
func NewSignedByVerifierFromConfig(cfg *config.RegistriesDConfig, scope string) *DefaultSignedByVerifier {
	store := NewLookasideStoreFromConfig(cfg, scope)
	if store == nil {
		return nil
	}
	return NewSignedByVerifier(store)
}

// Verify implements policy.SignedByVerifier.
// It:
//  1. Loads the keyring from the PRSignedBy requirement.
//  2. Fetches all signatures for the image digest from the signature store.
//  3. For each signature: verifies the OpenPGP signature, parses the payload,
//     validates the digest matches, and applies identity matching.
//  4. Returns true if at least one valid signature is found.
func (v *DefaultSignedByVerifier) Verify(ctx context.Context, req *policy.PRSignedBy, image policy.ImageReference) (bool, error) {
	// Load keyring.
	keyring, err := v.loadKeyRing(req)
	if err != nil {
		return false, fmt.Errorf("failed to load keyring: %w", err)
	}

	// Parse the image reference to get the digest.
	imgDigest, err := parseImageDigest(image.Reference)
	if err != nil {
		return false, fmt.Errorf("failed to parse image digest from reference %s: %w", image.Reference, err)
	}

	// Fetch signatures.
	sigs, err := v.store.GetSignatures(ctx, image.Scope, imgDigest)
	if err != nil {
		return false, fmt.Errorf("failed to fetch signatures: %w", err)
	}

	if len(sigs) == 0 {
		return false, nil
	}

	// Check each signature.
	for _, sigData := range sigs {
		payload, err := VerifyOpenPGPSignature(sigData, keyring)
		if err != nil {
			// Signature didn't verify — try next.
			continue
		}

		// Validate the payload.
		if err := payload.Validate(); err != nil {
			continue
		}

		// Check that the digest matches.
		payloadDigest, err := payload.ImageDigest()
		if err != nil {
			continue
		}
		if payloadDigest != imgDigest {
			continue
		}

		// Apply identity matching.
		matched, err := MatchSignedIdentity(req.SignedIdentity, image.Reference, payload.DockerReference())
		if err != nil {
			continue
		}
		if matched {
			return true, nil
		}
	}

	return false, nil
}

// loadKeyRing loads an OpenPGP keyring from the PRSignedBy requirement's
// key configuration.
func (v *DefaultSignedByVerifier) loadKeyRing(req *policy.PRSignedBy) (*KeyRing, error) {
	var keyPaths []string
	var keyDatas [][]byte

	if req.KeyPath != "" {
		keyPaths = append(keyPaths, req.KeyPath)
	}
	keyPaths = append(keyPaths, req.KeyPaths...)

	if req.KeyData != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.KeyData)
		if err != nil {
			// Try as raw data.
			keyDatas = append(keyDatas, []byte(req.KeyData))
		} else {
			keyDatas = append(keyDatas, decoded)
		}
	}

	return LoadKeyRing(keyPaths, keyDatas)
}

// parseImageDigest extracts the digest from an image reference.
// Supports formats like "registry.example.com/repo@sha256:abc123" and
// "registry.example.com/repo:tag" (but tag-only references don't have digests).
func parseImageDigest(ref string) (digest.Digest, error) {
	// Look for @digest format.
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '@' {
			dgst, err := digest.Parse(ref[i+1:])
			if err != nil {
				return "", fmt.Errorf("invalid digest in reference %s: %w", ref, err)
			}
			return dgst, nil
		}
	}
	return "", fmt.Errorf("reference %s does not contain a digest", ref)
}
