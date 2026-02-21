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

import "github.com/oras-project/oras-go/v3/registry/remote/config"

// Type constants for backward compatibility.
const (
	// TypeInsecureAcceptAnything accepts any image without verification
	TypeInsecureAcceptAnything = config.TypeInsecureAcceptAnything
	// TypeReject rejects all images
	TypeReject = config.TypeReject
	// TypeSignedBy requires simple signing verification
	TypeSignedBy = config.TypeSignedBy
	// TypeSigstoreSigned requires sigstore signature verification
	TypeSigstoreSigned = config.TypeSigstoreSigned
)

// Type aliases for backward compatibility.

// InsecureAcceptAnything accepts any image without verification.
// Deprecated: Use config.InsecureAcceptAnything instead.
type InsecureAcceptAnything = config.InsecureAcceptAnything

// Reject rejects all images.
// Deprecated: Use config.Reject instead.
type Reject = config.Reject

// IdentityMatch represents the type of identity matching.
// Deprecated: Use config.IdentityMatch instead.
type IdentityMatch = config.IdentityMatch

// Identity match constants for backward compatibility.
const (
	// IdentityMatchExact matches the exact identity
	IdentityMatchExact = config.IdentityMatchExact
	// IdentityMatchRepoDigestOrExact matches repository digest or exact
	IdentityMatchRepoDigestOrExact = config.IdentityMatchRepoDigestOrExact
	// IdentityMatchRepository matches the repository
	IdentityMatchRepository = config.IdentityMatchRepository
	// IdentityMatchExactReference matches exact reference
	IdentityMatchExactReference = config.IdentityMatchExactReference
	// IdentityMatchExactRepository matches exact repository
	IdentityMatchExactRepository = config.IdentityMatchExactRepository
	// IdentityMatchRemap remaps identity
	IdentityMatchRemap = config.IdentityMatchRemap
)

// SignedByKeyData represents GPG key data for signature verification.
// Deprecated: Use config.SignedByKeyData instead.
type SignedByKeyData = config.SignedByKeyData

// PRSignedBy represents a simple signing policy requirement.
// Deprecated: Use config.PRSignedBy instead.
type PRSignedBy = config.PRSignedBy

// SignedIdentity represents identity matching rules.
// Deprecated: Use config.SignedIdentity instead.
type SignedIdentity = config.SignedIdentity

// SigstoreKeyData represents a sigstore public key.
// Deprecated: Use config.SigstoreKeyData instead.
type SigstoreKeyData = config.SigstoreKeyData

// PRSigstoreSigned represents a sigstore signature policy requirement.
// Deprecated: Use config.PRSigstoreSigned instead.
type PRSigstoreSigned = config.PRSigstoreSigned

// FulcioConfig represents Fulcio certificate verification configuration.
// Deprecated: Use config.FulcioConfig instead.
type FulcioConfig = config.FulcioConfig
