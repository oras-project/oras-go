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

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// PolicyConfUserDir is the user-level configuration directory for policy.json
	PolicyConfUserDir = ".config/containers"
	// PolicyConfFileName is the name of the policy configuration file
	PolicyConfFileName = "policy.json"
	// PolicyConfSystemPath is the system-wide policy.json path
	PolicyConfSystemPath = "/etc/containers/policy.json"
)

// Policy represents a containers-policy.json configuration
type Policy struct {
	// Default is the default policy requirement
	Default PolicyRequirements `json:"default"`
	// Transports contains transport-specific policy scopes
	Transports map[TransportName]TransportScopes `json:"transports,omitempty"`
}

// TransportScopes represents scopes within a transport
type TransportScopes map[string]PolicyRequirements

// PolicyRequirements is a list of policy requirements
type PolicyRequirements []PolicyRequirement

// PolicyRequirement represents a single policy requirement
type PolicyRequirement interface {
	// Type returns the type of requirement
	Type() string
	// Validate validates the requirement configuration
	Validate() error
}

// NewPolicy creates a new empty Policy.
// Use this for programmatic policy construction without a file.
func NewPolicy() *Policy {
	return &Policy{
		Default:    make(PolicyRequirements, 0),
		Transports: make(map[TransportName]TransportScopes),
	}
}

// NewInsecureAcceptAnythingPolicy creates a policy that accepts all images.
// This is useful for testing or development environments.
func NewInsecureAcceptAnythingPolicy() *Policy {
	return &Policy{
		Default: PolicyRequirements{&InsecureAcceptAnything{}},
	}
}

// NewRejectAllPolicy creates a policy that rejects all images.
// This is a safe default that requires explicit configuration to allow images.
func NewRejectAllPolicy() *Policy {
	return &Policy{
		Default: PolicyRequirements{&Reject{}},
	}
}

// SetDefault sets the default policy requirements.
func (p *Policy) SetDefault(reqs ...PolicyRequirement) *Policy {
	p.Default = reqs
	return p
}

// SetTransportScope sets the policy requirements for a specific transport and scope.
func (p *Policy) SetTransportScope(transport TransportName, scope string, reqs ...PolicyRequirement) *Policy {
	if p.Transports == nil {
		p.Transports = make(map[TransportName]TransportScopes)
	}
	if p.Transports[transport] == nil {
		p.Transports[transport] = make(TransportScopes)
	}
	p.Transports[transport][scope] = reqs
	return p
}

// GetDefaultPolicyPath returns the default path to policy.json.
// It checks $HOME/.config/containers/policy.json first, then falls back to
// /etc/containers/policy.json.
func GetDefaultPolicyPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Try user-specific path first
	userPath := filepath.Join(homeDir, PolicyConfUserDir, PolicyConfFileName)
	if _, err := os.Stat(userPath); err == nil {
		return userPath, nil
	}

	// Fall back to system-wide path
	return PolicyConfSystemPath, nil
}

// Load loads a policy from the specified file path.
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %s: %w", path, err)
	}

	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("failed to parse policy file %s: %w", path, err)
	}

	return &policy, nil
}

// LoadDefault loads the policy from the default location.
func LoadDefault() (*Policy, error) {
	path, err := GetDefaultPolicyPath()
	if err != nil {
		return nil, err
	}

	return Load(path)
}

// Save saves a policy to the specified file path.
func (p *Policy) Save(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal policy: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write policy file %s: %w", path, err)
	}

	return nil
}

// Validate validates the policy configuration.
func (p *Policy) Validate() error {
	// Validate default requirements
	for _, req := range p.Default {
		if err := req.Validate(); err != nil {
			return fmt.Errorf("invalid default requirement: %w", err)
		}
	}

	// Validate transport-specific requirements
	for transport, scopes := range p.Transports {
		for scope, reqs := range scopes {
			for _, req := range reqs {
				if err := req.Validate(); err != nil {
					return fmt.Errorf("invalid requirement for transport %s scope %s: %w", transport, scope, err)
				}
			}
		}
	}

	return nil
}

// GetRequirementsForImage returns the policy requirements for a given transport and scope.
// It follows the precedence rules: specific scope > transport default > global default.
func (p *Policy) GetRequirementsForImage(transport TransportName, scope string) PolicyRequirements {
	// Check for transport-specific scope
	if transportScopes, ok := p.Transports[transport]; ok {
		// Try exact scope match first
		if reqs, ok := transportScopes[scope]; ok {
			return reqs
		}

		// Try transport default (empty scope)
		if reqs, ok := transportScopes[""]; ok {
			return reqs
		}
	}

	// Fall back to global default
	return p.Default
}
