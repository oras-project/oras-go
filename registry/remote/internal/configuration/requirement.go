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

package configuration

import (
	"encoding/json"
	"fmt"
)

const (
	// TypeInsecureAcceptAnything accepts any image without verification
	TypeInsecureAcceptAnything = "insecureAcceptAnything"
	// TypeReject rejects all images
	TypeReject = "reject"
	// TypeSignedBy requires simple signing verification
	TypeSignedBy = "signedBy"
	// TypeSigstoreSigned requires sigstore signature verification
	TypeSigstoreSigned = "sigstoreSigned"
)

// InsecureAcceptAnything accepts any image without verification
type InsecureAcceptAnything struct{}

// Type returns the requirement type
func (r *InsecureAcceptAnything) Type() string {
	return TypeInsecureAcceptAnything
}

// Validate validates the requirement
func (r *InsecureAcceptAnything) Validate() error {
	return nil
}

// Reject rejects all images
type Reject struct{}

// Type returns the requirement type
func (r *Reject) Type() string {
	return TypeReject
}

// Validate validates the requirement
func (r *Reject) Validate() error {
	return nil
}

// IdentityMatchType represents the type of identity matching
type IdentityMatchType string

const (
	// MatchExact matches the exact identity
	MatchExact IdentityMatchType = "matchExact"
	// MatchRepoDigestOrExact matches repository digest or exact
	MatchRepoDigestOrExact IdentityMatchType = "matchRepoDigestOrExact"
	// MatchRepository matches the repository
	MatchRepository IdentityMatchType = "matchRepository"
	// ExactReference matches exact reference
	ExactReference IdentityMatchType = "exactReference"
	// ExactRepository matches exact repository
	ExactRepository IdentityMatchType = "exactRepository"
	// RemapIdentity remaps identity
	RemapIdentity IdentityMatchType = "remapIdentity"
)

// SignedByKeyData represents GPG key data for signature verification
type SignedByKeyData struct {
	// KeyPath is the path to the GPG key file
	KeyPath string `json:"keyPath,omitempty"`
	// KeyData is the inline GPG key data
	KeyData string `json:"keyData,omitempty"`
}

// PRSignedBy represents a simple signing policy requirement
type PRSignedBy struct {
	// KeyType specifies the type of key (e.g., "GPGKeys")
	KeyType string `json:"keyType"`
	// KeyPath is the path to the key file
	KeyPath string `json:"keyPath,omitempty"`
	// KeyData is inline key data
	KeyData string `json:"keyData,omitempty"`
	// KeyPaths is a list of key paths (alternative to KeyPath)
	KeyPaths []string `json:"keyPaths,omitempty"`
	// KeyDatas is a list of inline key data (alternative to KeyData)
	KeyDatas []SignedByKeyData `json:"keyDatas,omitempty"`
	// SignedIdentity specifies the identity matching rules
	SignedIdentity *SignedIdentity `json:"signedIdentity,omitempty"`
}

// Type returns the requirement type
func (r *PRSignedBy) Type() string {
	return TypeSignedBy
}

// Validate validates the requirement
func (r *PRSignedBy) Validate() error {
	if r.KeyType == "" {
		return fmt.Errorf("keyType is required")
	}

	// Validate that at least one key source is provided
	hasKey := r.KeyPath != "" || r.KeyData != "" || len(r.KeyPaths) > 0 || len(r.KeyDatas) > 0
	if !hasKey {
		return fmt.Errorf("at least one key source (keyPath, keyData, keyPaths, or keyDatas) must be specified")
	}

	return validateSignedIdentity(r.SignedIdentity)
}

// SignedIdentity represents identity matching rules
type SignedIdentity struct {
	// Type is the identity match type
	Type IdentityMatchType `json:"type"`
	// DockerReference is used for certain match types
	DockerReference string `json:"dockerReference,omitempty"`
	// DockerRepository is used for certain match types
	DockerRepository string `json:"dockerRepository,omitempty"`
	// Prefix is used for remapIdentity
	Prefix string `json:"prefix,omitempty"`
	// SignedPrefix is used for remapIdentity
	SignedPrefix string `json:"signedPrefix,omitempty"`
}

// Validate validates the signed identity configuration
func (si *SignedIdentity) Validate() error {
	switch si.Type {
	case MatchExact, MatchRepoDigestOrExact:
		// No additional fields required
		return nil
	case MatchRepository:
		// No additional fields required
		return nil
	case ExactReference:
		if si.DockerReference == "" {
			return fmt.Errorf("dockerReference is required for exactReference type")
		}
		return nil
	case ExactRepository:
		if si.DockerRepository == "" {
			return fmt.Errorf("dockerRepository is required for exactRepository type")
		}
		return nil
	case RemapIdentity:
		if si.Prefix == "" || si.SignedPrefix == "" {
			return fmt.Errorf("both prefix and signedPrefix are required for remapIdentity type")
		}
		return nil
	default:
		return fmt.Errorf("unknown identity match type: %s", si.Type)
	}
}

// validateSignedIdentity validates a SignedIdentity if it's not nil
func validateSignedIdentity(si *SignedIdentity) error {
	if si != nil {
		if err := si.Validate(); err != nil {
			return fmt.Errorf("invalid signedIdentity: %w", err)
		}
	}
	return nil
}

// SigstoreKeyData represents a sigstore public key
type SigstoreKeyData struct {
	// PublicKeyFile is the path to the public key file
	PublicKeyFile string `json:"publicKeyFile,omitempty"`
	// PublicKeyData is inline public key data
	PublicKeyData []byte `json:"publicKeyData,omitempty"`
}

// PRSigstoreSigned represents a sigstore signature policy requirement
type PRSigstoreSigned struct {
	// KeyPath is the path to the public key
	KeyPath string `json:"keyPath,omitempty"`
	// KeyData is inline public key data
	KeyData []byte `json:"keyData,omitempty"`
	// KeyDatas is a list of key data
	KeyDatas []SigstoreKeyData `json:"keyDatas,omitempty"`
	// Fulcio specifies Fulcio certificate verification
	Fulcio *FulcioConfig `json:"fulcio,omitempty"`
	// RekorPublicKeyPath is the path to the Rekor public key
	RekorPublicKeyPath string `json:"rekorPublicKeyPath,omitempty"`
	// RekorPublicKeyData is inline Rekor public key data
	RekorPublicKeyData []byte `json:"rekorPublicKeyData,omitempty"`
	// SignedIdentity specifies the identity matching rules
	SignedIdentity *SignedIdentity `json:"signedIdentity,omitempty"`
}

// Type returns the requirement type
func (r *PRSigstoreSigned) Type() string {
	return TypeSigstoreSigned
}

// Validate validates the requirement
func (r *PRSigstoreSigned) Validate() error {
	// Validate that at least one verification method is provided
	hasKey := r.KeyPath != "" || len(r.KeyData) > 0 || len(r.KeyDatas) > 0 || r.Fulcio != nil
	if !hasKey {
		return fmt.Errorf("at least one verification method must be specified")
	}

	if r.Fulcio != nil {
		if err := r.Fulcio.Validate(); err != nil {
			return fmt.Errorf("invalid fulcio config: %w", err)
		}
	}

	return validateSignedIdentity(r.SignedIdentity)
}

// FulcioConfig represents Fulcio certificate verification configuration
type FulcioConfig struct {
	// CAPath is the path to the Fulcio CA certificate
	CAPath string `json:"caPath,omitempty"`
	// CAData is inline CA certificate data
	CAData []byte `json:"caData,omitempty"`
	// OIDCIssuer is the OIDC issuer URL
	OIDCIssuer string `json:"oidcIssuer,omitempty"`
	// SubjectEmail is the subject email to verify
	SubjectEmail string `json:"subjectEmail,omitempty"`
}

// Validate validates the Fulcio configuration
func (fc *FulcioConfig) Validate() error {
	// At least CA path or data should be provided
	if fc.CAPath == "" && len(fc.CAData) == 0 {
		return fmt.Errorf("either caPath or caData must be specified")
	}

	return nil
}

// UnmarshalJSON implements custom JSON unmarshaling for PolicyRequirements
func (pr *PolicyRequirements) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*pr = make([]PolicyRequirement, 0, len(raw))

	for i, rawReq := range raw {
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawReq, &typeCheck); err != nil {
			return fmt.Errorf("requirement %d: failed to determine type: %w", i, err)
		}

		var req PolicyRequirement
		switch typeCheck.Type {
		case TypeInsecureAcceptAnything:
			req = &InsecureAcceptAnything{}
		case TypeReject:
			req = &Reject{}
		case TypeSignedBy:
			var signedBy PRSignedBy
			if err := json.Unmarshal(rawReq, &signedBy); err != nil {
				return fmt.Errorf("requirement %d: failed to unmarshal signedBy: %w", i, err)
			}
			req = &signedBy
		case TypeSigstoreSigned:
			var sigstoreSigned PRSigstoreSigned
			if err := json.Unmarshal(rawReq, &sigstoreSigned); err != nil {
				return fmt.Errorf("requirement %d: failed to unmarshal sigstoreSigned: %w", i, err)
			}
			req = &sigstoreSigned
		default:
			return fmt.Errorf("requirement %d: unknown type %q", i, typeCheck.Type)
		}

		*pr = append(*pr, req)
	}

	return nil
}

// marshalWithType marshals a value and adds a "type" field to the resulting JSON
func marshalWithType(typeName string, v interface{}) ([]byte, error) {
	// First marshal the value
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Unmarshal to map to add type field
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	// Add type field
	m["type"] = typeName

	// Marshal back with type field included
	return json.Marshal(m)
}

// MarshalJSON implements custom JSON marshaling for PolicyRequirements
func (pr PolicyRequirements) MarshalJSON() ([]byte, error) {
	raw := make([]json.RawMessage, 0, len(pr))

	for _, req := range pr {
		var data []byte
		var err error

		switch r := req.(type) {
		case *InsecureAcceptAnything:
			data, err = json.Marshal(map[string]string{"type": TypeInsecureAcceptAnything})
		case *Reject:
			data, err = json.Marshal(map[string]string{"type": TypeReject})
		case *PRSignedBy:
			data, err = marshalWithType(TypeSignedBy, r)
		case *PRSigstoreSigned:
			data, err = marshalWithType(TypeSigstoreSigned, r)
		default:
			return nil, fmt.Errorf("unknown requirement type: %T", req)
		}

		if err != nil {
			return nil, err
		}
		raw = append(raw, data)
	}

	return json.Marshal(raw)
}
