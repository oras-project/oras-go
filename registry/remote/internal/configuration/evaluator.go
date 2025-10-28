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
	"context"
	"fmt"
)

// ImageReference represents a reference to an image
type ImageReference struct {
	// Transport is the transport type (e.g., "docker")
	Transport TransportName
	// Scope is the scope within the transport (e.g., "docker.io/library/nginx")
	Scope string
	// Reference is the full reference (e.g., "docker.io/library/nginx:latest")
	Reference string
}

// Evaluator evaluates policy requirements against image references
type Evaluator struct {
	policy *Policy
}

// NewEvaluator creates a new policy evaluator
func NewEvaluator(policy *Policy) (*Evaluator, error) {
	if policy == nil {
		return nil, fmt.Errorf("policy cannot be nil")
	}

	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid policy: %w", err)
	}

	return &Evaluator{
		policy: policy,
	}, nil
}

// IsImageAllowed determines if an image is allowed by the policy
func (e *Evaluator) IsImageAllowed(ctx context.Context, image ImageReference) (bool, error) {
	reqs := e.policy.GetRequirementsForImage(image.Transport, image.Scope)

	if len(reqs) == 0 {
		// No requirements means reject by default for safety
		return false, fmt.Errorf("no policy requirements found for %s:%s", image.Transport, image.Scope)
	}

	// All requirements must be satisfied
	for _, req := range reqs {
		allowed, err := e.evaluateRequirement(ctx, req, image)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate requirement %s: %w", req.Type(), err)
		}
		if !allowed {
			return false, nil
		}
	}

	return true, nil
}

// evaluateRequirement evaluates a single policy requirement
func (e *Evaluator) evaluateRequirement(ctx context.Context, req PolicyRequirement, image ImageReference) (bool, error) {
	switch r := req.(type) {
	case *InsecureAcceptAnything:
		return e.evaluateInsecureAcceptAnything(ctx, r, image)
	case *Reject:
		return e.evaluateReject(ctx, r, image)
	case *PRSignedBy:
		return e.evaluateSignedBy(ctx, r, image)
	case *PRSigstoreSigned:
		return e.evaluateSigstoreSigned(ctx, r, image)
	default:
		return false, fmt.Errorf("unknown requirement type: %T", req)
	}
}

// evaluateInsecureAcceptAnything always accepts the image
func (e *Evaluator) evaluateInsecureAcceptAnything(ctx context.Context, req *InsecureAcceptAnything, image ImageReference) (bool, error) {
	return true, nil
}

// evaluateReject always rejects the image
func (e *Evaluator) evaluateReject(ctx context.Context, req *Reject, image ImageReference) (bool, error) {
	return false, nil
}

// evaluateSignedBy evaluates a signedBy requirement
// Note: This is a placeholder implementation. Full signature verification
// would require integration with GPG/signing libraries.
func (e *Evaluator) evaluateSignedBy(ctx context.Context, req *PRSignedBy, image ImageReference) (bool, error) {
	// TODO: Implement actual signature verification https://github.com/oras-project/oras-go/issues/1029
	return false, fmt.Errorf("signedBy verification not yet implemented")
}

// evaluateSigstoreSigned evaluates a sigstoreSigned requirement
// Note: This is a placeholder implementation. Full signature verification
// would require integration with sigstore libraries.
func (e *Evaluator) evaluateSigstoreSigned(ctx context.Context, req *PRSigstoreSigned, image ImageReference) (bool, error) {
	// TODO: Implement actual sigstore verification https://github.com/oras-project/oras-go/issues/1029
	return false, fmt.Errorf("sigstoreSigned verification not yet implemented")
}

// ShouldAcceptImage is a convenience function that returns true if the image is allowed
func ShouldAcceptImage(ctx context.Context, policy *Policy, image ImageReference) (bool, error) {
	evaluator, err := NewEvaluator(policy)
	if err != nil {
		return false, err
	}

	return evaluator.IsImageAllowed(ctx, image)
}
