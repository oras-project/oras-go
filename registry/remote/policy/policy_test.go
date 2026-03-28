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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPolicy_GetRequirementsForImage(t *testing.T) {
	tests := []struct {
		name      string
		policy    *Policy
		transport TransportName
		scope     string
		wantType  string
	}{
		{
			name: "global default",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/library/nginx",
			wantType:  TypeReject,
		},
		{
			name: "transport default",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/library/nginx",
			wantType:  TypeInsecureAcceptAnything,
		},
		{
			name: "specific scope",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"":                        PolicyRequirements{&InsecureAcceptAnything{}},
						"docker.io/library/nginx": PolicyRequirements{&Reject{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/library/nginx",
			wantType:  TypeReject,
		},
		{
			name: "prefix match at path boundary",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"docker.io/myorg": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/myorg/myrepo",
			wantType:  TypeInsecureAcceptAnything,
		},
		{
			name: "longest prefix wins",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"docker.io":              PolicyRequirements{&InsecureAcceptAnything{}},
						"docker.io/myorg":        PolicyRequirements{&Reject{}},
						"docker.io/myorg/myrepo": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/myorg/myrepo",
			wantType:  TypeInsecureAcceptAnything,
		},
		{
			name: "prefix does not match partial path segment",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"docker.io/my": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "docker.io/myorg/myrepo",
			wantType:  TypeReject,
		},
		{
			name: "wildcard subdomain match",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"*.example.com": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "sub.example.com/myrepo",
			wantType:  TypeInsecureAcceptAnything,
		},
		{
			name: "wildcard does not match non-subdomain",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"*.example.com": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			transport: TransportNameDocker,
			scope:     "notexample.com/myrepo",
			wantType:  TypeReject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs := tt.policy.GetRequirementsForImage(tt.transport, tt.scope)
			if len(reqs) == 0 {
				t.Fatal("expected requirements, got none")
			}
			if reqs[0].Type() != tt.wantType {
				t.Errorf("got type %s, want %s", reqs[0].Type(), tt.wantType)
			}
		})
	}
}

func TestPolicy_JSONMarshalUnmarshal(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{&Reject{}},
		Transports: map[TransportName]TransportScopes{
			TransportNameDocker: {
				"": PolicyRequirements{&InsecureAcceptAnything{}},
				"docker.io/library/nginx": PolicyRequirements{
					&PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/path/to/key.gpg",
						SignedIdentity: &SignedIdentity{
							Type: IdentityMatchExact,
						},
					},
				},
			},
		},
	}

	// Marshal
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal policy: %v", err)
	}

	// Unmarshal
	var unmarshaled Policy
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal policy: %v", err)
	}

	// Verify
	if len(unmarshaled.Default) != 1 || unmarshaled.Default[0].Type() != TypeReject {
		t.Error("default requirement not preserved")
	}

	dockerScopes := unmarshaled.Transports[TransportNameDocker]
	if len(dockerScopes[""]) != 1 || dockerScopes[""][0].Type() != TypeInsecureAcceptAnything {
		t.Error("docker default requirement not preserved")
	}

	nginxReqs := dockerScopes["docker.io/library/nginx"]
	if len(nginxReqs) != 1 || nginxReqs[0].Type() != TypeSignedBy {
		t.Error("nginx-specific requirement not preserved")
	}
}

func TestPolicy_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.json")

	original := &Policy{
		Default: PolicyRequirements{&Reject{}},
		Transports: map[TransportName]TransportScopes{
			TransportNameDocker: {
				"": PolicyRequirements{&InsecureAcceptAnything{}},
			},
		},
	}

	// Save
	if err := original.Save(policyPath); err != nil {
		t.Fatalf("failed to save policy: %v", err)
	}

	// Load
	loaded, err := LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("failed to load policy: %v", err)
	}

	// Verify
	if len(loaded.Default) != 1 || loaded.Default[0].Type() != TypeReject {
		t.Error("loaded policy default not correct")
	}

	if len(loaded.Transports[TransportNameDocker][""]) != 1 {
		t.Error("loaded policy docker transport not correct")
	}
}

func TestPolicy_Validate(t *testing.T) {
	tests := []struct {
		name    string
		policy  *Policy
		wantErr bool
	}{
		{
			name: "valid policy",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
			},
			wantErr: false,
		},
		{
			name: "empty default requirements",
			policy: &Policy{
				Default: PolicyRequirements{},
			},
			wantErr: true,
		},
		{
			name: "invalid signedBy requirement",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSignedBy{
						// Missing KeyType
						KeyPath: "/path/to/key.gpg",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid signed identity",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/path/to/key.gpg",
						SignedIdentity: &SignedIdentity{
							Type: IdentityMatchExactReference,
							// Missing DockerReference
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEvaluator_IsImageAllowed(t *testing.T) {
	tests := []struct {
		name       string
		policy     *Policy
		image      ImageReference
		wantResult bool
		wantErr    bool
	}{
		{
			name: "insecure accept anything",
			policy: &Policy{
				Default: PolicyRequirements{&InsecureAcceptAnything{}},
			},
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: true,
			wantErr:    false,
		},
		{
			name: "reject",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
			},
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: false,
			wantErr:    false,
		},
		{
			name: "signedBy not implemented",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/path/to/key.gpg",
					},
				},
			},
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, err := NewEvaluator(tt.policy)
			if err != nil {
				t.Fatalf("failed to create evaluator: %v", err)
			}

			result, err := evaluator.IsImageAllowed(context.Background(), tt.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsImageAllowed() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.wantResult {
				t.Errorf("IsImageAllowed() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

func TestRequirement_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     PolicyRequirement
		wantErr bool
	}{
		{
			name:    "insecure accept anything valid",
			req:     &InsecureAcceptAnything{},
			wantErr: false,
		},
		{
			name:    "reject valid",
			req:     &Reject{},
			wantErr: false,
		},
		{
			name: "signedBy valid",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
			},
			wantErr: false,
		},
		{
			name: "signedBy missing keyType",
			req: &PRSignedBy{
				KeyPath: "/path/to/key.gpg",
			},
			wantErr: true,
		},
		{
			name: "signedBy missing key source",
			req: &PRSignedBy{
				KeyType: "GPGKeys",
			},
			wantErr: true,
		},
		{
			name: "sigstoreSigned valid",
			req: &PRSigstoreSigned{
				KeyPath: "/path/to/key.pub",
			},
			wantErr: false,
		},
		{
			name:    "sigstoreSigned missing verification method",
			req:     &PRSigstoreSigned{},
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

func TestGetDefaultPolicyPath(t *testing.T) {
	path, err := GetDefaultPolicyPath()

	homeDir, _ := os.UserHomeDir()
	userPath := filepath.Join(homeDir, policyConfUserDir, policyConfFileName)

	if _, statErr := os.Stat(userPath); statErr == nil {
		// User policy file exists — should succeed with that path
		if err != nil {
			t.Fatalf("GetDefaultPolicyPath() error = %v", err)
		}
		if path != userPath {
			t.Errorf("GetDefaultPolicyPath() = %v, want %v", path, userPath)
		}
	} else if systemPolicyPath != "" {
		// No user policy but system path available (Linux)
		if err != nil {
			t.Fatalf("GetDefaultPolicyPath() error = %v", err)
		}
		if path != systemPolicyPath {
			t.Errorf("GetDefaultPolicyPath() = %v, want %v", path, systemPolicyPath)
		}
	} else {
		// No user policy and no system path (non-Linux) — should error
		if err == nil {
			t.Error("GetDefaultPolicyPath() should error on non-Linux without user policy")
		}
	}
}

// Test LoadDefault
func TestLoadDefault(t *testing.T) {
	// Create a temporary policy file in the home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	userPolicyDir := filepath.Join(homeDir, policyConfUserDir)
	userPolicyPath := filepath.Join(userPolicyDir, policyConfFileName)

	// Clean up any existing test policy
	defer os.Remove(userPolicyPath)

	// Create policy directory
	if err := os.MkdirAll(userPolicyDir, 0755); err != nil {
		t.Fatalf("failed to create policy directory: %v", err)
	}

	// Create a test policy
	testPolicy := &Policy{
		Default: PolicyRequirements{&InsecureAcceptAnything{}},
	}
	if err := testPolicy.Save(userPolicyPath); err != nil {
		t.Fatalf("failed to save test policy: %v", err)
	}

	// Test LoadDefault
	loaded, err := LoadDefault()
	if err != nil {
		t.Errorf("LoadDefault() error = %v", err)
	}
	if loaded == nil {
		t.Error("LoadDefault() returned nil policy")
	}
}

// Test ShouldAcceptImage convenience function
func TestShouldAcceptImage(t *testing.T) {
	tests := []struct {
		name       string
		policy     *Policy
		image      ImageReference
		wantResult bool
		wantErr    bool
	}{
		{
			name: "accept image",
			policy: &Policy{
				Default: PolicyRequirements{&InsecureAcceptAnything{}},
			},
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: true,
			wantErr:    false,
		},
		{
			name: "reject image",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
			},
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: false,
			wantErr:    false,
		},
		{
			name:   "nil policy",
			policy: nil,
			image: ImageReference{
				Transport: TransportNameDocker,
				Scope:     "docker.io/library/nginx",
				Reference: "docker.io/library/nginx:latest",
			},
			wantResult: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ShouldAcceptImage(context.Background(), tt.policy, tt.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("ShouldAcceptImage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.wantResult {
				t.Errorf("ShouldAcceptImage() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

// Test NewEvaluator with invalid policy
func TestNewEvaluator_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		policy  *Policy
		wantErr bool
	}{
		{
			name:    "nil policy",
			policy:  nil,
			wantErr: true,
		},
		{
			name: "invalid policy - missing keyType",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSignedBy{
						KeyPath: "/path/to/key",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid policy - empty default",
			policy: &Policy{
				Default: PolicyRequirements{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEvaluator(tt.policy)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEvaluator() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test evaluateSigstoreSigned
func TestEvaluator_SigstoreSigned(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&PRSigstoreSigned{
				KeyPath: "/path/to/key.pub",
			},
		},
	}

	evaluator, err := NewEvaluator(policy)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	// Should return error as sigstore verification is not implemented
	allowed, err := evaluator.IsImageAllowed(context.Background(), image)
	if err == nil {
		t.Error("expected error for unimplemented sigstore verification")
	}
	if allowed {
		t.Error("should not allow image when verification fails")
	}
}

// Test Policy validation with transport-specific requirements
func TestPolicy_ValidateTransportScopes(t *testing.T) {
	tests := []struct {
		name    string
		policy  *Policy
		wantErr bool
	}{
		{
			name: "valid transport scopes",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"docker.io": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid transport requirement",
			policy: &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					TransportNameDocker: {
						"docker.io": PolicyRequirements{
							&PRSignedBy{
								// Missing KeyType
								KeyPath: "/path/to/key",
							},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test LoadPolicy with non-existent file
func TestLoadPolicy_NonExistent(t *testing.T) {
	_, err := LoadPolicy("/nonexistent/path/policy.json")
	if err == nil {
		t.Error("LoadPolicy() should fail for non-existent file")
	}
}

// Test LoadPolicy with invalid JSON
func TestLoadPolicy_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.json")

	// Write invalid JSON
	if err := os.WriteFile(policyPath, []byte("invalid json {"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadPolicy(policyPath)
	if err == nil {
		t.Error("LoadPolicy() should fail for invalid JSON")
	}
}

// Test Policy.Save with invalid path
func TestPolicy_Save_ErrorCases(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with read-only directory
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatalf("failed to create read-only directory: %v", err)
	}
	defer os.Chmod(readOnlyDir, 0755) // Restore permissions for cleanup

	policy := &Policy{
		Default: PolicyRequirements{&Reject{}},
	}

	policyPath := filepath.Join(readOnlyDir, "policy.json")
	err := policy.Save(policyPath)
	if err == nil {
		t.Error("Save() should fail for read-only directory")
	}
}

// Test GetDefaultPolicyPath when user policy doesn't exist
func TestGetDefaultPolicyPath_NoUserPolicy(t *testing.T) {
	path, err := GetDefaultPolicyPath()

	if systemPolicyPath != "" {
		// On Linux, should fall back to system path
		if err != nil {
			t.Errorf("GetDefaultPolicyPath() error = %v", err)
		}
		if path == "" {
			t.Error("GetDefaultPolicyPath() returned empty path")
		}
		if path != systemPolicyPath {
			if !filepath.IsAbs(path) {
				t.Errorf("GetDefaultPolicyPath() returned non-absolute path: %s", path)
			}
		}
	} else {
		// On non-Linux without user policy, should return an error
		homeDir, _ := os.UserHomeDir()
		userPath := filepath.Join(homeDir, policyConfUserDir, policyConfFileName)
		if _, statErr := os.Stat(userPath); statErr != nil {
			// User policy doesn't exist either — expect error
			if err == nil {
				t.Error("GetDefaultPolicyPath() should error on non-Linux without user policy")
			}
		}
	}
}

// Test multiple requirements in a single policy
func TestPolicy_MultipleRequirements(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&InsecureAcceptAnything{},
			&InsecureAcceptAnything{}, // Multiple requirements - all must pass
		},
	}

	evaluator, err := NewEvaluator(policy)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	allowed, err := evaluator.IsImageAllowed(context.Background(), image)
	if err != nil {
		t.Errorf("IsImageAllowed() error = %v", err)
	}
	if !allowed {
		t.Error("IsImageAllowed() should allow image when all requirements pass")
	}
}

// Test no requirements found for scope after matching
func TestEvaluator_NoRequirementsForScope(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{&Reject{}},
		Transports: map[TransportName]TransportScopes{
			TransportNameDocker: {
				"docker.io/library/nginx": PolicyRequirements{},
			},
		},
	}

	evaluator, err := NewEvaluator(policy)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	_, err = evaluator.IsImageAllowed(context.Background(), image)
	if err == nil {
		t.Error("IsImageAllowed() should fail when no requirements are defined for the scope")
	}
}

// Test all transport types
func TestPolicy_AllTransports(t *testing.T) {
	transports := []TransportName{
		TransportNameDocker,
		TransportNameAtomic,
		TransportNameContainersStorage,
		TransportNameDir,
		TransportNameDockerArchive,
		TransportNameDockerDaemon,
		TransportNameOCI,
		TransportNameOCIArchive,
		TransportNameSIF,
		TransportNameTarball,
	}

	for _, transport := range transports {
		t.Run(string(transport), func(t *testing.T) {
			policy := &Policy{
				Default: PolicyRequirements{&Reject{}},
				Transports: map[TransportName]TransportScopes{
					transport: {
						"": PolicyRequirements{&InsecureAcceptAnything{}},
					},
				},
			}

			if err := policy.Validate(); err != nil {
				t.Errorf("policy validation failed for transport %s: %v", transport, err)
			}

			reqs := policy.GetRequirementsForImage(transport, "test/scope")
			if len(reqs) == 0 {
				t.Errorf("no requirements found for transport %s", transport)
			}
			if reqs[0].Type() != TypeInsecureAcceptAnything {
				t.Errorf("wrong requirement type for transport %s", transport)
			}
		})
	}
}

// Test JSON round-trip with all requirement types
func TestPolicyRequirements_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		reqs PolicyRequirements
	}{
		{
			name: "insecure accept anything",
			reqs: PolicyRequirements{&InsecureAcceptAnything{}},
		},
		{
			name: "reject",
			reqs: PolicyRequirements{&Reject{}},
		},
		{
			name: "signedBy with keyPath",
			reqs: PolicyRequirements{
				&PRSignedBy{
					KeyType: "GPGKeys",
					KeyPath: "/path/to/key.gpg",
					SignedIdentity: &SignedIdentity{
						Type: IdentityMatchExact,
					},
				},
			},
		},
		{
			name: "sigstoreSigned with fulcio",
			reqs: PolicyRequirements{
				&PRSigstoreSigned{
					KeyPath: "/path/to/key.pub",
					Fulcio: &FulcioConfig{
						CAPath:       "/path/ca.pem",
						CAData:       []byte("ca data"),
						OIDCIssuer:   "https://oauth.example.com",
						SubjectEmail: "user@example.com",
					},
					RekorPublicKeyPath: "/path/rekor.pub",
					RekorPublicKeyData: []byte("rekor key"),
					SignedIdentity: &SignedIdentity{
						Type: IdentityMatchRepository,
					},
				},
			},
		},
		{
			name: "mixed requirements",
			reqs: PolicyRequirements{
				&InsecureAcceptAnything{},
				&Reject{},
				&PRSignedBy{
					KeyType: "GPGKeys",
					KeyPath: "/key.gpg",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal
			data, err := json.Marshal(tt.reqs)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			// Unmarshal
			var unmarshaled PolicyRequirements
			if err := json.Unmarshal(data, &unmarshaled); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			// Verify count
			if len(unmarshaled) != len(tt.reqs) {
				t.Errorf("requirement count mismatch: got %d, want %d", len(unmarshaled), len(tt.reqs))
			}

			// Verify types
			for i := range tt.reqs {
				if unmarshaled[i].Type() != tt.reqs[i].Type() {
					t.Errorf("requirement[%d] type mismatch: got %s, want %s",
						i, unmarshaled[i].Type(), tt.reqs[i].Type())
				}
			}
		})
	}
}

// Test UnmarshalJSON with invalid data
func TestPolicyRequirements_UnmarshalJSON_Errors(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{
			name:    "not an array",
			data:    `{"type": "reject"}`,
			wantErr: true,
		},
		{
			name:    "missing type field",
			data:    `[{"keyType": "GPGKeys"}]`,
			wantErr: true,
		},
		{
			name:    "unknown type",
			data:    `[{"type": "unknownType"}]`,
			wantErr: true,
		},
		{
			name:    "invalid signedBy",
			data:    `[{"type": "signedBy", "keyType": 123}]`,
			wantErr: true,
		},
		{
			name:    "invalid sigstoreSigned",
			data:    `[{"type": "sigstoreSigned", "keyPath": 123}]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqs PolicyRequirements
			err := json.Unmarshal([]byte(tt.data), &reqs)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsPathPrefix(t *testing.T) {
	tests := []struct {
		s      string
		prefix string
		want   bool
	}{
		{"docker.io/myorg/myrepo", "docker.io/myorg", true},
		{"docker.io/myorg/myrepo", "docker.io/myorg/myrepo", true},
		{"docker.io/myorg/myrepo", "docker.io/my", false},
		{"docker.io/myorg", "docker.io/myorg/myrepo", false},
		{"docker.io/myorg/myrepo", "", true},
		{"docker.io/myorg", "docker.io/myorg", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.prefix, func(t *testing.T) {
			got := isPathPrefix(tt.s, tt.prefix)
			if got != tt.want {
				t.Errorf("isPathPrefix(%q, %q) = %v, want %v", tt.s, tt.prefix, got, tt.want)
			}
		})
	}
}
