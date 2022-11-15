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

package remote

import (
	"errors"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/container/set"
	"oras.land/oras-go/v2/internal/descriptor"
)

// zeroDigest represents a digest that consists of zeros. zeroDigest is used
// for pinging Referrers API.
const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// referrersState represents the state of Referrers API.
type referrersState = int32

const (
	// referrersStateUnknown represents an unknown state of Referrers API.
	referrersStateUnknown referrersState = iota
	// referrersStateSupported represents that the repository is known to
	// support Referrers API
	referrersStateSupported
	// referrersStateUnsupported represents that the repository is known to
	// not support Referrers API
	referrersStateUnsupported
)

// referrerOperation represents an operation on a referrer.
type referrerOperation = int32

const (
	// referrersStateUnknown represents an addition operation on a referrer.
	referrerOperationAdd referrerOperation = iota
	// referrersStateUnknown represents a removal operation on a referrer.
	referrerOperationRemove
)

// referrerChange represents a change on a referrer.
type referrerChange struct {
	referrer  ocispec.Descriptor
	operation referrerOperation
}

var (
	// ErrReferrersCapabilityAlreadySet is returned by SetReferrersCapability()
	// when the Referrers API capability has been already set.
	ErrReferrersCapabilityAlreadySet = errors.New("referrers capability cannot be changed once set")

	// errNoReferrerUpdate is returned by applyReferrerChanges() when there
	// is no any referrer update.
	errNoReferrerUpdate = errors.New("no referrer update")
)

// buildReferrersTag builds the referrers tag for the given manifest descriptor.
// Format: <algorithm>-<digest>
// Reference: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc1/spec.md#unavailable-referrers-api
func buildReferrersTag(desc ocispec.Descriptor) string {
	alg := desc.Digest.Algorithm().String()
	encoded := desc.Digest.Encoded()
	return alg + "-" + encoded
}

// isReferrersFilterApplied checks annotations to see if requested is in the
// applied filter list.
func isReferrersFilterApplied(annotations map[string]string, requested string) bool {
	applied := annotations[ocispec.AnnotationReferrersFiltersApplied]
	if applied == "" || requested == "" {
		return false
	}
	filters := strings.Split(applied, ",")
	for _, f := range filters {
		if f == requested {
			return true
		}
	}
	return false
}

// filterReferrers filters a slice of referrers by artifactType in place.
// The returned slice contains matching referrers.
func filterReferrers(refs []ocispec.Descriptor, artifactType string) []ocispec.Descriptor {
	if artifactType == "" {
		return refs
	}
	var j int
	for i, ref := range refs {
		if ref.ArtifactType == artifactType {
			if i != j {
				refs[j] = ref
			}
			j++
		}
	}
	return refs[:j]
}

// applyReferrerChanges applies referrerChanges on referrers and returns the
// updated referrers.
func applyReferrerChanges(referrers []ocispec.Descriptor, referrerChanges []referrerChange) ([]ocispec.Descriptor, error) {
	referrersSet := make(set.Set[descriptor.Descriptor], len(referrers))
	for _, r := range referrers {
		key := descriptor.FromOCI(r)
		referrersSet.Add(key)
	}

	var referrersToAdd []ocispec.Descriptor
	referrersToRemove := make(set.Set[descriptor.Descriptor], len(referrers))
	for _, change := range referrerChanges {
		key := descriptor.FromOCI(change.referrer)
		switch change.operation {
		case referrerOperationAdd:
			if !referrersSet.Contains(key) {
				// add distinct referrers
				referrersSet.Add(key)
				referrersToAdd = append(referrersToAdd, change.referrer)
			}
		case referrerOperationRemove:
			// log distinct referrers to remove
			referrersToRemove.Add(key)
		}
	}
	if len(referrersToAdd) == len(referrersToRemove) {
		// the changes can be offset when the items in referrersToAdd and
		// referrersToRemove are the same
		canOffset := true
		for _, r := range referrersToAdd {
			key := descriptor.FromOCI(r)
			if !referrersToRemove.Contains(key) {
				canOffset = false
				break
			}
		}
		if canOffset {
			return nil, errNoReferrerUpdate
		}
	}

	var referrersUpdated bool
	var updatedReferrers []ocispec.Descriptor
	if len(referrersToAdd) > 0 {
		referrers = append(referrers, referrersToAdd...)
		referrersUpdated = true
	}
	for _, r := range referrers {
		key := descriptor.FromOCI(r)
		if referrersToRemove.Contains(key) {
			// exclude the referrers that are in the removal set
			referrersUpdated = true
		} else {
			updatedReferrers = append(updatedReferrers, r)
		}
	}
	if !referrersUpdated {
		return nil, errNoReferrerUpdate
	}

	return updatedReferrers, nil
}
