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

package policy_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"oras.land/oras-go/v2/registry/remote/policy"
)

// ExamplePolicy_basic demonstrates creating a basic policy
func ExamplePolicy_basic() {
	// Create a policy that rejects everything by default
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
	}

	// Add a transport-specific policy for docker that accepts anything
	p.Transports = map[policy.TransportName]policy.TransportScopes{
		policy.TransportDocker: {
			"": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
		},
	}

	fmt.Println("Policy created with default reject and docker accept")
	// Output: Policy created with default reject and docker accept
}

// ExamplePolicy_signedBy demonstrates creating a policy with signature verification
func ExamplePolicy_signedBy() {
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportDocker: {
				"docker.io/myorg": policy.PolicyRequirements{
					&policy.PRSignedBy{
						KeyType: "GPGKeys",
						KeyPath: "/path/to/trusted-key.gpg",
						SignedIdentity: &policy.SignedIdentity{
							Type: policy.MatchRepository,
						},
					},
				},
			},
		},
	}
	_ = p

	fmt.Println("Policy requires GPG signatures for docker.io/myorg")
	// Output: Policy requires GPG signatures for docker.io/myorg
}

// ExampleLoadPolicy demonstrates loading a policy from a file
func ExampleLoadPolicy() {
	// Create a temporary policy file
	tmpDir := os.TempDir()
	policyPath := filepath.Join(tmpDir, "example-policy.json")

	// Create and save a policy
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportDocker: {
				"": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
			},
		},
	}

	if err := policy.SavePolicy(p, policyPath); err != nil {
		log.Fatalf("Failed to save policy: %v", err)
	}
	defer os.Remove(policyPath)

	// Load the policy
	loaded, err := policy.LoadPolicy(policyPath)
	if err != nil {
		log.Fatalf("Failed to load policy: %v", err)
	}

	fmt.Printf("Loaded policy with %d default requirements\n", len(loaded.Default))
	// Output: Loaded policy with 1 default requirements
}

// ExampleEvaluator_IsImageAllowed demonstrates evaluating a policy
func ExampleEvaluator_IsImageAllowed() {
	// Create a permissive policy for testing
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
	}

	// Create an evaluator
	evaluator, err := policy.NewEvaluator(p)
	if err != nil {
		log.Fatalf("Failed to create evaluator: %v", err)
	}

	// Check if an image is allowed
	image := policy.ImageReference{
		Transport: policy.TransportDocker,
		Scope:     "docker.io/library/nginx",
		Reference: "docker.io/library/nginx:latest",
	}

	allowed, err := evaluator.IsImageAllowed(context.Background(), image)
	if err != nil {
		log.Fatalf("Failed to evaluate policy: %v", err)
	}

	fmt.Printf("Image allowed: %v\n", allowed)
	// Output: Image allowed: true
}

// ExamplePolicy_GetRequirementsForImage demonstrates getting requirements for a specific image
func ExamplePolicy_GetRequirementsForImage() {
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportDocker: {
				"":                         policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
				"docker.io/library/nginx": policy.PolicyRequirements{&policy.Reject{}},
			},
		},
	}

	// Get requirements for nginx specifically
	nginxReqs := p.GetRequirementsForImage(policy.TransportDocker, "docker.io/library/nginx")
	fmt.Printf("Nginx requirements: %s\n", nginxReqs[0].Type())

	// Get requirements for other docker images
	otherReqs := p.GetRequirementsForImage(policy.TransportDocker, "docker.io/library/alpine")
	fmt.Printf("Other docker requirements: %s\n", otherReqs[0].Type())

	// Output:
	// Nginx requirements: reject
	// Other docker requirements: insecureAcceptAnything
}

// ExamplePolicy_sigstore demonstrates creating a sigstore-based policy
func ExamplePolicy_sigstore() {
	p := &policy.Policy{
		Default: policy.PolicyRequirements{&policy.Reject{}},
		Transports: map[policy.TransportName]policy.TransportScopes{
			policy.TransportDocker: {
				"docker.io/myorg": policy.PolicyRequirements{
					&policy.PRSigstoreSigned{
						KeyPath: "/path/to/cosign.pub",
						Fulcio: &policy.FulcioConfig{
							CAPath:       "/path/to/fulcio-ca.pem",
							OIDCIssuer:   "https://oauth2.sigstore.dev/auth",
							SubjectEmail: "user@example.com",
						},
						RekorPublicKeyPath: "/path/to/rekor.pub",
						SignedIdentity: &policy.SignedIdentity{
							Type: policy.MatchRepository,
						},
					},
				},
			},
		},
	}
	_ = p

	fmt.Println("Policy requires sigstore signatures for docker.io/myorg")
	// Output: Policy requires sigstore signatures for docker.io/myorg
}
