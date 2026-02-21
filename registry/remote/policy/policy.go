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

// Package policy implements support for containers-policy.json format
// for OCI image signature verification policies.
//
// Reference: https://man.archlinux.org/man/containers-policy.json.5.en
package policy

import "github.com/oras-project/oras-go/v3/registry/remote/config"

// Path constants for backward compatibility.
const (
	// PolicyConfUserDir is the user-level configuration directory for policy.json
	PolicyConfUserDir = config.PolicyConfUserDir
	// PolicyConfFileName is the name of the policy configuration file
	PolicyConfFileName = config.PolicyConfFileName
	// PolicyConfSystemPath is the system-wide policy.json path
	PolicyConfSystemPath = config.PolicyConfSystemPath
)

// Type aliases for backward compatibility.

// Policy represents a containers-policy.json configuration.
// Deprecated: Use config.Policy instead.
type Policy = config.Policy

// TransportScopes represents scopes within a transport.
// Deprecated: Use config.TransportScopes instead.
type TransportScopes = config.TransportScopes

// PolicyRequirements is a list of policy requirements.
// Deprecated: Use config.PolicyRequirements instead.
type PolicyRequirements = config.PolicyRequirements

// PolicyRequirement represents a single policy requirement.
// Deprecated: Use config.PolicyRequirement instead.
type PolicyRequirement = config.PolicyRequirement

// Function aliases for backward compatibility.
var (
	// NewPolicy creates a new empty Policy.
	// Deprecated: Use config.NewPolicy instead.
	NewPolicy = config.NewPolicy
	// NewInsecureAcceptAnythingPolicy creates a policy that accepts all images.
	// Deprecated: Use config.NewInsecureAcceptAnythingPolicy instead.
	NewInsecureAcceptAnythingPolicy = config.NewInsecureAcceptAnythingPolicy
	// NewRejectAllPolicy creates a policy that rejects all images.
	// Deprecated: Use config.NewRejectAllPolicy instead.
	NewRejectAllPolicy = config.NewRejectAllPolicy
	// GetDefaultPolicyPath returns the default path to policy.json.
	// Deprecated: Use config.GetDefaultPolicyPath instead.
	GetDefaultPolicyPath = config.GetDefaultPolicyPath
	// Load loads a policy from the specified file path.
	// Deprecated: Use config.LoadPolicy instead.
	Load = config.LoadPolicy
	// LoadDefault loads the policy from the default location.
	// Deprecated: Use config.LoadDefaultPolicy instead.
	LoadDefault = config.LoadDefaultPolicy
)
