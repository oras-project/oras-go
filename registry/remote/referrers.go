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
	"oras.land/oras-go/v2/content"
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
// Returns errNoReferrerUpdate if there is no any referrers updates.
func applyReferrerChanges(referrers []ocispec.Descriptor, referrerChanges []referrerChange) ([]ocispec.Descriptor, error) {
	referrerIndexMap := make(map[descriptor.Descriptor]int, len(referrers))
	for i, r := range referrers {
		key := descriptor.FromOCI(r)
		referrerIndexMap[key] = i
	}

	// apply changes
	updatedReferrers := make([]ocispec.Descriptor, len(referrers))
	copy(updatedReferrers, referrers)
	for _, change := range referrerChanges {
		key := descriptor.FromOCI(change.referrer)
		switch change.operation {
		case referrerOperationAdd:
			if _, ok := referrerIndexMap[key]; !ok {
				// add distinct referrers
				updatedReferrers = append(updatedReferrers, change.referrer)
				referrerIndexMap[key] = len(updatedReferrers) - 1
			}
		case referrerOperationRemove:
			if i, ok := referrerIndexMap[key]; ok {
				// remove referrers that are already in the map
				updatedReferrers[i] = ocispec.Descriptor{}
				delete(referrerIndexMap, key)
			}
		}
	}

	if len(referrerIndexMap) == len(referrers) {
		// if the result referrer map contains the same content as the
		// original referrers, consider that there is no update on the
		// referrers.
		var referrersUpdated bool
		for _, r := range referrers {
			key := descriptor.FromOCI(r)
			if _, ok := referrerIndexMap[key]; !ok {
				referrersUpdated = true
			}
		}
		if !referrersUpdated {
			return nil, errNoReferrerUpdate
		}
	}

	// in-place swap
	i, size := 0, len(referrerIndexMap)
	for j := range updatedReferrers {
		for i < size && !content.Equal(updatedReferrers[i], ocispec.Descriptor{}) {
			// for i, skip non-empty slots
			i++
		}
		if j > i && !content.Equal(updatedReferrers[j], ocispec.Descriptor{}) {
			// i: empty slot, j: non-empty slot
			updatedReferrers[i] = updatedReferrers[j]
			updatedReferrers[j] = ocispec.Descriptor{}
		}
	}
	return updatedReferrers[:size], nil
}
