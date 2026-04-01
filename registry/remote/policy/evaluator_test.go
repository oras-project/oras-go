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
	"errors"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
)

// mockSignedByVerifier is a mock implementation of SignedByVerifier for testing
type mockSignedByVerifier struct {
	result bool
	err    error
}

func (m *mockSignedByVerifier) Verify(ctx context.Context, req *PRSignedBy, image ImageReference) (bool, error) {
	return m.result, m.err
}

// mockSigstoreVerifier is a mock implementation of SigstoreVerifier for testing
type mockSigstoreVerifier struct {
	result bool
	err    error
}

func (m *mockSigstoreVerifier) Verify(ctx context.Context, req *PRSigstoreSigned, image ImageReference) (bool, error) {
	return m.result, m.err
}

func TestEvaluator_WithSignedByVerifier(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
			},
		},
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	tests := []struct {
		name       string
		verifier   *mockSignedByVerifier
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "verifier returns true",
			verifier:   &mockSignedByVerifier{result: true, err: nil},
			wantResult: true,
			wantErr:    false,
		},
		{
			name:       "verifier returns false",
			verifier:   &mockSignedByVerifier{result: false, err: nil},
			wantResult: false,
			wantErr:    false,
		},
		{
			name:       "verifier returns error",
			verifier:   &mockSignedByVerifier{result: false, err: errors.New("verification failed")},
			wantResult: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, err := NewEvaluator(policy, WithSignedByVerifier(tt.verifier))
			if err != nil {
				t.Fatalf("NewEvaluator() error = %v", err)
			}

			result, err := evaluator.IsImageAllowed(context.Background(), image)
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

func TestEvaluator_WithSigstoreVerifier(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&PRSigstoreSigned{
				KeyPath: "/path/to/key.pub",
			},
		},
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	tests := []struct {
		name       string
		verifier   *mockSigstoreVerifier
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "verifier returns true",
			verifier:   &mockSigstoreVerifier{result: true, err: nil},
			wantResult: true,
			wantErr:    false,
		},
		{
			name:       "verifier returns false",
			verifier:   &mockSigstoreVerifier{result: false, err: nil},
			wantResult: false,
			wantErr:    false,
		},
		{
			name:       "verifier returns error",
			verifier:   &mockSigstoreVerifier{result: false, err: errors.New("verification failed")},
			wantResult: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, err := NewEvaluator(policy, WithSigstoreVerifier(tt.verifier))
			if err != nil {
				t.Fatalf("NewEvaluator() error = %v", err)
			}

			result, err := evaluator.IsImageAllowed(context.Background(), image)
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

func TestEvaluator_NoVerifier_ReturnsUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		policy *Policy
	}{
		{
			name: "signedBy without verifier",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/path/to/key.gpg",
					},
				},
			},
		},
		{
			name: "sigstoreSigned without verifier",
			policy: &Policy{
				Default: PolicyRequirements{
					&PRSigstoreSigned{
						KeyPath: "/path/to/key.pub",
					},
				},
			},
		},
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, err := NewEvaluator(tt.policy)
			if err != nil {
				t.Fatalf("NewEvaluator() error = %v", err)
			}

			_, err = evaluator.IsImageAllowed(context.Background(), image)
			if err == nil {
				t.Error("IsImageAllowed() should return error when no verifier is set")
			}
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Errorf("IsImageAllowed() error should wrap ErrUnsupported, got: %v", err)
			}
		})
	}
}

func TestEvaluator_WithBothVerifiers(t *testing.T) {
	policy := &Policy{
		Default: PolicyRequirements{
			&PRSignedBy{
				KeyType: "GPGKeys",
				KeyPath: "/path/to/key.gpg",
			},
			&PRSigstoreSigned{
				KeyPath: "/path/to/key.pub",
			},
		},
	}

	image := ImageReference{
		Transport: TransportNameDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	signedByVerifier := &mockSignedByVerifier{result: true, err: nil}
	sigstoreVerifier := &mockSigstoreVerifier{result: true, err: nil}

	evaluator, err := NewEvaluator(policy,
		WithSignedByVerifier(signedByVerifier),
		WithSigstoreVerifier(sigstoreVerifier),
	)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	result, err := evaluator.IsImageAllowed(context.Background(), image)
	if err != nil {
		t.Errorf("IsImageAllowed() error = %v", err)
	}
	if !result {
		t.Error("IsImageAllowed() should return true when all verifiers pass")
	}
}
