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

package signature

import (
	"fmt"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

// MatchSignedIdentity checks whether the signed docker reference matches the
// image reference according to the identity matching rules.
// If signedIdentity is nil, the default (matchRepoDigestOrExact) is used.
func MatchSignedIdentity(signedIdentity *policy.SignedIdentity, imageRef, signedDockerRef string) (bool, error) {
	if signedIdentity == nil {
		// Default: matchRepoDigestOrExact
		return matchRepoDigestOrExact(imageRef, signedDockerRef)
	}

	switch signedIdentity.Type {
	case policy.IdentityMatchExact:
		return matchExact(imageRef, signedDockerRef), nil
	case policy.IdentityMatchRepoDigestOrExact:
		return matchRepoDigestOrExact(imageRef, signedDockerRef)
	case policy.IdentityMatchRepository:
		return matchRepository(imageRef, signedDockerRef), nil
	case policy.IdentityMatchExactReference:
		return matchExactReference(signedIdentity.DockerReference, signedDockerRef), nil
	case policy.IdentityMatchExactRepository:
		return matchExactRepository(signedIdentity.DockerRepository, signedDockerRef), nil
	case policy.IdentityMatchRemap:
		return remapIdentity(signedIdentity.Prefix, signedIdentity.SignedPrefix, imageRef, signedDockerRef), nil
	default:
		return false, fmt.Errorf("unknown identity match type: %s", signedIdentity.Type)
	}
}

// matchExact requires the signed reference to exactly match the image reference.
func matchExact(imageRef, signedRef string) bool {
	return imageRef == signedRef
}

// matchRepoDigestOrExact matches if the references are exactly equal, or if
// they refer to the same repository when the image reference is by digest.
func matchRepoDigestOrExact(imageRef, signedRef string) (bool, error) {
	if imageRef == signedRef {
		return true, nil
	}
	// If the image ref is by digest, compare repositories.
	if strings.Contains(imageRef, "@") {
		imageRepo := repositoryOf(imageRef)
		signedRepo := repositoryOf(signedRef)
		return imageRepo == signedRepo, nil
	}
	return false, nil
}

// matchRepository matches if both references point to the same repository,
// ignoring the tag/digest.
func matchRepository(imageRef, signedRef string) bool {
	return repositoryOf(imageRef) == repositoryOf(signedRef)
}

// matchExactReference matches if the signed reference exactly equals the
// configured docker reference.
func matchExactReference(configuredRef, signedRef string) bool {
	return configuredRef == signedRef
}

// matchExactRepository matches if the signed reference's repository exactly
// equals the configured docker repository.
func matchExactRepository(configuredRepo, signedRef string) bool {
	return configuredRepo == repositoryOf(signedRef)
}

// remapIdentity matches by replacing a prefix in the image reference with a
// signed prefix, then checking for exact match.
func remapIdentity(prefix, signedPrefix, imageRef, signedRef string) bool {
	if !strings.HasPrefix(imageRef, prefix) {
		return false
	}
	// Remap: replace prefix in the image reference with signedPrefix.
	remapped := signedPrefix + imageRef[len(prefix):]
	return remapped == signedRef
}

// repositoryOf extracts the repository part from a reference.
// "registry.example.com/repo:tag" -> "registry.example.com/repo"
// "registry.example.com/repo@sha256:abc" -> "registry.example.com/repo"
// "registry.example.com/repo:tag@sha256:abc" -> "registry.example.com/repo"
func repositoryOf(ref string) string {
	// Strip digest.
	if idx := strings.Index(ref, "@"); idx != -1 {
		ref = ref[:idx]
	}
	// Strip tag.
	// Be careful with ports: "host:port/repo:tag" — find the last colon after
	// the last slash.
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		// Only strip if the colon comes after a slash (not a port).
		if slashIdx := strings.LastIndex(ref, "/"); slashIdx != -1 && idx > slashIdx {
			return ref[:idx]
		}
		// No slash at all — this is "host:tag" or "host:port",
		// could be either. If no slash, treat as full reference.
		if !strings.Contains(ref, "/") {
			return ref[:idx]
		}
	}
	return ref
}
