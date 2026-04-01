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

package policy

import (
	"testing"
)

// Test SignedIdentity validation for all match types
func TestSignedIdentity_ValidateAllTypes(t *testing.T) {
	tests := []struct {
		name     string
		identity *SignedIdentity
		wantErr  bool
	}{
		{
			name: "matchExact valid",
			identity: &SignedIdentity{
				Type: IdentityMatchExact,
			},
			wantErr: false,
		},
		{
			name: "matchRepoDigestOrExact valid",
			identity: &SignedIdentity{
				Type: IdentityMatchRepoDigestOrExact,
			},
			wantErr: false,
		},
		{
			name: "matchRepository valid",
			identity: &SignedIdentity{
				Type: IdentityMatchRepository,
			},
			wantErr: false,
		},
		{
			name: "exactReference valid",
			identity: &SignedIdentity{
				Type:            IdentityMatchExactReference,
				DockerReference: "docker.io/library/nginx:latest",
			},
			wantErr: false,
		},
		{
			name: "exactReference missing dockerReference",
			identity: &SignedIdentity{
				Type: IdentityMatchExactReference,
			},
			wantErr: true,
		},
		{
			name: "exactRepository valid",
			identity: &SignedIdentity{
				Type:             IdentityMatchExactRepository,
				DockerRepository: "docker.io/library/nginx",
			},
			wantErr: false,
		},
		{
			name: "exactRepository missing dockerRepository",
			identity: &SignedIdentity{
				Type: IdentityMatchExactRepository,
			},
			wantErr: true,
		},
		{
			name: "remapIdentity valid",
			identity: &SignedIdentity{
				Type:         IdentityMatchRemap,
				Prefix:       "docker.io/",
				SignedPrefix: "quay.io/",
			},
			wantErr: false,
		},
		{
			name: "remapIdentity missing prefix",
			identity: &SignedIdentity{
				Type:         IdentityMatchRemap,
				SignedPrefix: "quay.io/",
			},
			wantErr: true,
		},
		{
			name: "remapIdentity missing signedPrefix",
			identity: &SignedIdentity{
				Type:   IdentityMatchRemap,
				Prefix: "docker.io/",
			},
			wantErr: true,
		},
		{
			name: "unknown match type",
			identity: &SignedIdentity{
				Type: "unknownType",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.identity.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test PRSignedBy validation with various key source combinations
func TestPRSignedBy_ValidateKeySources(t *testing.T) {
	tests := []struct {
		name    string
		req     *PRSignedBy
		wantErr bool
	}{
		{
			name: "valid with keyPath",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
			},
			wantErr: false,
		},
		{
			name: "valid with keyData",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyData: "inline key data",
			},
			wantErr: false,
		},
		{
			name: "valid with keyPaths",
			req: &PRSignedBy{
				KeyType:  "GPGKeys",
				KeyPaths: []string{"/path1.gpg", "/path2.gpg"},
			},
			wantErr: false,
		},
		{
			name: "invalid with multiple key sources (keyPath and keyData)",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/key.gpg",
				KeyData: "inline data",
			},
			wantErr: true,
		},
		{
			name: "invalid with multiple key sources (keyPath and keyPaths)",
			req: &PRSignedBy{
				KeyType:  "GPGKeys",
				KeyPath:  "/path/key.gpg",
				KeyPaths: []string{"/another.gpg"},
			},
			wantErr: true,
		},
		{
			name: "missing keyType",
			req: &PRSignedBy{
				KeyPath: "/path/to/key.gpg",
			},
			wantErr: true,
		},
		{
			name: "missing all key sources",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
			},
			wantErr: true,
		},
		{
			name: "valid with signed identity",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
				SignedIdentity: &SignedIdentity{
					Type: IdentityMatchRepository,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid signed identity",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
				SignedIdentity: &SignedIdentity{
					Type: IdentityMatchExactReference,
					// Missing DockerReference
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test PRSigstoreSigned validation
func TestPRSigstoreSigned_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     *PRSigstoreSigned
		wantErr bool
	}{
		{
			name: "valid with keyPath",
			req: &PRSigstoreSigned{
				KeyPath: "/path/to/key.pub",
			},
			wantErr: false,
		},
		{
			name: "valid with keyData",
			req: &PRSigstoreSigned{
				KeyData: []byte("inline key data"),
			},
			wantErr: false,
		},
		{
			name: "valid with keyDatas",
			req: &PRSigstoreSigned{
				KeyDatas: []string{"key1data", "key2data"},
			},
			wantErr: false,
		},
		{
			name: "valid with fulcio",
			req: &PRSigstoreSigned{
				Fulcio: &FulcioConfig{
					CAPath:       "/path/ca.pem",
					OIDCIssuer:   "https://oauth.example.com",
					SubjectEmail: "user@example.com",
				},
			},
			wantErr: false,
		},
		{
			name: "missing verification method",
			req: &PRSigstoreSigned{
				RekorPublicKeyPath: "/path/rekor.pub",
			},
			wantErr: true,
		},
		{
			name: "invalid fulcio config - missing CA",
			req: &PRSigstoreSigned{
				Fulcio: &FulcioConfig{
					OIDCIssuer:   "https://oauth.example.com",
					SubjectEmail: "user@example.com",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid fulcio config - missing oidcIssuer",
			req: &PRSigstoreSigned{
				Fulcio: &FulcioConfig{
					CAPath:       "/path/ca.pem",
					SubjectEmail: "user@example.com",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid fulcio config - missing subjectEmail",
			req: &PRSigstoreSigned{
				Fulcio: &FulcioConfig{
					CAPath:     "/path/ca.pem",
					OIDCIssuer: "https://oauth.example.com",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid signed identity",
			req: &PRSigstoreSigned{
				KeyPath: "/path/key.pub",
				SignedIdentity: &SignedIdentity{
					Type: IdentityMatchExactRepository,
					// Missing DockerRepository
				},
			},
			wantErr: true,
		},
		{
			name: "valid with keyPath and optional fields",
			req: &PRSigstoreSigned{
				KeyPath:            "/path/key.pub",
				RekorPublicKeyPath: "/path/rekor.pub",
				RekorPublicKeyData: []byte("rekor key"),
				SignedIdentity: &SignedIdentity{
					Type: IdentityMatchExact,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test PRSigstoreSigned Type method
func TestPRSigstoreSigned_Type(t *testing.T) {
	req := &PRSigstoreSigned{
		KeyPath: "/path/key.pub",
	}
	if req.Type() != TypeSigstoreSigned {
		t.Errorf("Type() = %v, want %v", req.Type(), TypeSigstoreSigned)
	}
}

// Test FulcioConfig validation
func TestFulcioConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *FulcioConfig
		wantErr bool
	}{
		{
			name: "valid with CAPath and required fields",
			config: &FulcioConfig{
				CAPath:       "/path/ca.pem",
				OIDCIssuer:   "https://oauth.example.com",
				SubjectEmail: "user@example.com",
			},
			wantErr: false,
		},
		{
			name: "valid with CAData and required fields",
			config: &FulcioConfig{
				CAData:       []byte("ca certificate data"),
				OIDCIssuer:   "https://oauth.example.com",
				SubjectEmail: "user@example.com",
			},
			wantErr: false,
		},
		{
			name: "valid with all fields",
			config: &FulcioConfig{
				CAPath:       "/path/ca.pem",
				CAData:       []byte("ca data"),
				OIDCIssuer:   "https://oauth.example.com",
				SubjectEmail: "user@example.com",
			},
			wantErr: false,
		},
		{
			name:    "missing both CAPath and CAData",
			config:  &FulcioConfig{},
			wantErr: true,
		},
		{
			name: "missing CA with other fields",
			config: &FulcioConfig{
				OIDCIssuer:   "https://oauth.example.com",
				SubjectEmail: "user@example.com",
			},
			wantErr: true,
		},
		{
			name: "missing oidcIssuer",
			config: &FulcioConfig{
				CAPath:       "/path/ca.pem",
				SubjectEmail: "user@example.com",
			},
			wantErr: true,
		},
		{
			name: "missing subjectEmail",
			config: &FulcioConfig{
				CAPath:     "/path/ca.pem",
				OIDCIssuer: "https://oauth.example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test all IdentityMatch constants
func TestIdentityMatch_Constants(t *testing.T) {
	types := []IdentityMatch{
		IdentityMatchExact,
		IdentityMatchRepoDigestOrExact,
		IdentityMatchRepository,
		IdentityMatchExactReference,
		IdentityMatchExactRepository,
		IdentityMatchRemap,
	}

	for _, matchType := range types {
		t.Run(string(matchType), func(t *testing.T) {
			identity := &SignedIdentity{Type: matchType}

			// Add required fields based on type
			switch matchType {
			case IdentityMatchExactReference:
				identity.DockerReference = "docker.io/library/nginx:latest"
			case IdentityMatchExactRepository:
				identity.DockerRepository = "docker.io/library/nginx"
			case IdentityMatchRemap:
				identity.Prefix = "docker.io/"
				identity.SignedPrefix = "quay.io/"
			}

			err := identity.Validate()
			if err != nil {
				t.Errorf("Validate() failed for valid %s: %v", matchType, err)
			}
		})
	}
}

// Test requirement type constants
func TestRequirementType_Constants(t *testing.T) {
	tests := []struct {
		req      PolicyRequirement
		wantType string
	}{
		{&InsecureAcceptAnything{}, TypeInsecureAcceptAnything},
		{&Reject{}, TypeReject},
		{&PRSignedBy{KeyType: "GPGKeys", KeyPath: "/key"}, TypeSignedBy},
		{&PRSigstoreSigned{KeyPath: "/key.pub"}, TypeSigstoreSigned},
	}

	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			if tt.req.Type() != tt.wantType {
				t.Errorf("Type() = %v, want %v", tt.req.Type(), tt.wantType)
			}
		})
	}
}

// Test edge case: empty string fields
func TestSignedIdentity_EmptyFields(t *testing.T) {
	tests := []struct {
		name     string
		identity *SignedIdentity
		wantErr  bool
	}{
		{
			name: "exactReference with empty dockerReference",
			identity: &SignedIdentity{
				Type:            IdentityMatchExactReference,
				DockerReference: "",
			},
			wantErr: true,
		},
		{
			name: "exactRepository with empty dockerRepository",
			identity: &SignedIdentity{
				Type:             IdentityMatchExactRepository,
				DockerRepository: "",
			},
			wantErr: true,
		},
		{
			name: "remapIdentity with empty prefix",
			identity: &SignedIdentity{
				Type:         IdentityMatchRemap,
				Prefix:       "",
				SignedPrefix: "quay.io/",
			},
			wantErr: true,
		},
		{
			name: "remapIdentity with empty signedPrefix",
			identity: &SignedIdentity{
				Type:         IdentityMatchRemap,
				Prefix:       "docker.io/",
				SignedPrefix: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.identity.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
