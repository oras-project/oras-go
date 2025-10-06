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

// unknownRequirement is a custom requirement type for testing
type unknownRequirement struct{}

func (u *unknownRequirement) Type() string {
	return "unknown"
}

func (u *unknownRequirement) Validate() error {
	return nil
}

// Test evaluateRequirement with unknown requirement type
func TestEvaluator_UnknownRequirementType(t *testing.T) {
	evaluator := &Evaluator{
		policy: &Policy{
			Default: PolicyRequirements{&unknownRequirement{}},
		},
	}

	image := ImageReference{
		Transport: TransportDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	_, err := evaluator.evaluateRequirement(context.Background(), &unknownRequirement{}, image)
	if err == nil {
		t.Error("evaluateRequirement() should fail for unknown requirement type")
	}
}

// Test LoadDefaultPolicy when both user and system paths don't exist
func TestLoadDefaultPolicy_NoFiles(t *testing.T) {
	// This test depends on system state, so we'll just verify it returns an error
	// when files don't exist (most common case)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	userPath := filepath.Join(homeDir, PolicyConfUserDir, PolicyConfFileName)
	systemPath := PolicyConfSystemPath

	// Check if files exist
	userExists := false
	systemExists := false

	if _, err := os.Stat(userPath); err == nil {
		userExists = true
	}
	if _, err := os.Stat(systemPath); err == nil {
		systemExists = true
	}

	if !userExists && !systemExists {
		// No policy files exist, LoadDefaultPolicy should fail
		_, err := LoadDefaultPolicy()
		if err == nil {
			t.Error("LoadDefaultPolicy() should fail when no policy files exist")
		}
	}
}

// Test SavePolicy with directory creation failure
func TestSavePolicy_CreateDirError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("Test requires non-root user")
	}

	// Try to create a policy in a path that requires root permissions
	policy := &Policy{
		Default: PolicyRequirements{&Reject{}},
	}

	// This should fail on most systems
	policyPath := "/root/test-policy/policy.json"
	err := SavePolicy(policy, policyPath)
	if err == nil {
		// If it somehow succeeded, clean up
		os.RemoveAll("/root/test-policy")
		t.Skip("Expected permission denied, but operation succeeded")
	}
}

// customRequirement is a custom requirement type for testing
type customRequirement struct{}

func (c *customRequirement) Type() string {
	return "custom"
}

func (c *customRequirement) Validate() error {
	return nil
}

// Test MarshalJSON with unknown requirement type in slice
func TestPolicyRequirements_MarshalJSON_UnknownType(t *testing.T) {
	reqs := PolicyRequirements{&customRequirement{}}

	// This should fail because customRequirement is not a known type
	_, err := json.Marshal(reqs)
	if err == nil {
		t.Error("MarshalJSON() should fail for unknown requirement type")
	}
}

// Test UnmarshalJSON with malformed type determination
func TestPolicyRequirements_UnmarshalJSON_MalformedType(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "type is not a string",
			data: `[{"type": 123}]`,
		},
		{
			name: "type is null",
			data: `[{"type": null}]`,
		},
		{
			name: "type is object",
			data: `[{"type": {}}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqs PolicyRequirements
			err := json.Unmarshal([]byte(tt.data), &reqs)
			// Should either fail to unmarshal or fail validation
			if err == nil {
				t.Errorf("UnmarshalJSON() should fail for %s", tt.name)
			}
		})
	}
}

// Test GetDefaultPolicyPath with stat errors
func TestGetDefaultPolicyPath_EdgeCases(t *testing.T) {
	// This mostly tests that the function doesn't panic with weird paths
	path, err := GetDefaultPolicyPath()
	if err != nil {
		t.Errorf("GetDefaultPolicyPath() should not fail: %v", err)
	}
	if path == "" {
		t.Error("GetDefaultPolicyPath() returned empty path")
	}

	// Verify the path is one of the expected values
	homeDir, _ := os.UserHomeDir()
	userPath := filepath.Join(homeDir, PolicyConfUserDir, PolicyConfFileName)

	if path != userPath && path != PolicyConfSystemPath {
		t.Errorf("GetDefaultPolicyPath() returned unexpected path: %s", path)
	}
}

// Test complex policy with nested errors
func TestPolicy_ValidateNestedErrors(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{&Reject{}},
		Transports: map[TransportName]TransportScopes{
			TransportDocker: {
				"scope1": PolicyRequirements{
					&PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/key.gpg",
						SignedIdentity: &SignedIdentity{
							Type: ExactReference,
							// Missing DockerReference - will cause validation error
						},
					},
				},
				"scope2": PolicyRequirements{&InsecureAcceptAnything{}},
			},
			TransportOCI: {
				"": PolicyRequirements{&Reject{}},
			},
		},
	}

	err := policy.Validate()
	if err == nil {
		t.Error("Validate() should fail for policy with invalid nested requirements")
	}

	// Error should mention the specific scope and transport
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Error message should not be empty")
	}
}

// Test IsImageAllowed with first requirement failing
func TestEvaluator_IsImageAllowed_FirstRequirementFails(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&Reject{}, // This will fail
			&InsecureAcceptAnything{},
		},
	}

	evaluator, err := NewEvaluator(policy)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	image := ImageReference{
		Transport: TransportDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	allowed, err := evaluator.IsImageAllowed(context.Background(), image)
	if err != nil {
		t.Errorf("IsImageAllowed() error = %v", err)
	}
	if allowed {
		t.Error("IsImageAllowed() should return false when first requirement fails")
	}
}

// Test complete policy lifecycle
func TestPolicy_CompleteLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "complete-policy.json")

	// 1. Create a complex policy
	originalPolicy := &Policy{
		Default: PolicyRequirements{&Reject{}},
		Transports: map[TransportName]TransportScopes{
			TransportDocker: {
				"": PolicyRequirements{&InsecureAcceptAnything{}},
				"docker.io/trusted": PolicyRequirements{
					&PRSignedBy{
						KeyType:  "GPGKeys",
						KeyPath:  "/path/key1.gpg",
						KeyPaths: []string{"/path/key2.gpg"},
						KeyDatas: []SignedByKeyData{
							{KeyPath: "/path/key3.gpg"},
							{KeyData: "inline-key-data"},
						},
						SignedIdentity: &SignedIdentity{
							Type:            ExactReference,
							DockerReference: "docker.io/trusted/image:v1.0",
						},
					},
				},
			},
			TransportOCI: {
				"": PolicyRequirements{
					&PRSigstoreSigned{
						KeyPath: "/path/cosign.pub",
						KeyData: []byte("cosign-key-data"),
						KeyDatas: []SigstoreKeyData{
							{PublicKeyFile: "/path/key.pub"},
							{PublicKeyData: []byte("inline-key")},
						},
						Fulcio: &FulcioConfig{
							CAPath:       "/path/fulcio-ca.pem",
							CAData:       []byte("ca-cert-data"),
							OIDCIssuer:   "https://oauth.example.com",
							SubjectEmail: "user@example.com",
						},
						RekorPublicKeyPath: "/path/rekor.pub",
						RekorPublicKeyData: []byte("rekor-key"),
						SignedIdentity: &SignedIdentity{
							Type:         RemapIdentity,
							Prefix:       "ghcr.io/",
							SignedPrefix: "docker.io/",
						},
					},
				},
			},
		},
	}

	// 2. Validate
	if err := originalPolicy.Validate(); err != nil {
		t.Fatalf("original policy validation failed: %v", err)
	}

	// 3. Save
	if err := SavePolicy(originalPolicy, policyPath); err != nil {
		t.Fatalf("failed to save policy: %v", err)
	}

	// 4. Load
	loadedPolicy, err := LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("failed to load policy: %v", err)
	}

	// 5. Validate loaded policy
	if err := loadedPolicy.Validate(); err != nil {
		t.Fatalf("loaded policy validation failed: %v", err)
	}

	// 6. Create evaluator
	evaluator, err := NewEvaluator(loadedPolicy)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	// 7. Test different scopes
	testCases := []struct {
		transport TransportName
		scope     string
		wantType  string
	}{
		{TransportDocker, "docker.io/library/nginx", TypeInsecureAcceptAnything},
		{TransportDocker, "docker.io/trusted", TypeSignedBy},
		{TransportOCI, "ghcr.io/package", TypeSigstoreSigned},
		{TransportAtomic, "atomic.io/image", TypeReject}, // Falls back to default
	}

	for _, tc := range testCases {
		t.Run(string(tc.transport)+":"+tc.scope, func(t *testing.T) {
			reqs := loadedPolicy.GetRequirementsForImage(tc.transport, tc.scope)
			if len(reqs) == 0 {
				t.Error("no requirements found")
				return
			}
			if reqs[0].Type() != tc.wantType {
				t.Errorf("got type %s, want %s", reqs[0].Type(), tc.wantType)
			}
		})
	}

	// 8. Test evaluation
	dockerImage := ImageReference{
		Transport: TransportDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	allowed, err := evaluator.IsImageAllowed(context.Background(), dockerImage)
	if err != nil {
		t.Errorf("evaluation failed: %v", err)
	}
	if !allowed {
		t.Error("docker image should be allowed by policy")
	}
}

// Test zero-value structs
func TestZeroValueStructs(t *testing.T) {
	t.Run("empty SignedIdentity", func(t *testing.T) {
		si := &SignedIdentity{}
		err := si.Validate()
		if err == nil {
			t.Error("empty SignedIdentity should fail validation")
		}
	})

	t.Run("empty FulcioConfig", func(t *testing.T) {
		fc := &FulcioConfig{}
		err := fc.Validate()
		if err == nil {
			t.Error("empty FulcioConfig should fail validation")
		}
	})

	t.Run("empty PRSignedBy", func(t *testing.T) {
		req := &PRSignedBy{}
		err := req.Validate()
		if err == nil {
			t.Error("empty PRSignedBy should fail validation")
		}
	})

	t.Run("empty PRSigstoreSigned", func(t *testing.T) {
		req := &PRSigstoreSigned{}
		err := req.Validate()
		if err == nil {
			t.Error("empty PRSigstoreSigned should fail validation")
		}
	})
}
