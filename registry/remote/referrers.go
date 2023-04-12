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
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
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
	// support Referrers API.
	referrersStateSupported
	// referrersStateUnsupported represents that the repository is known to
	// not support Referrers API.
	referrersStateUnsupported
)

// referrerOperation represents an operation on a referrer.
type referrerOperation = int32

const (
	// referrerOperationAdd represents an addition operation on a referrer.
	referrerOperationAdd referrerOperation = iota
	// referrerOperationRemove represents a removal operation on a referrer.
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

// DanglingReferrersIndexError is returned when failed to delete old referrer index
// after newly updated referrer index being pushed.
// Only returned if referrer API is unavailable.

// ErrDanglingReferrersIndex is returned from an attempt to delete an old
// referrers index fails after a newly updated referrers index has been pushed.
// This error is only returned when the referrers API is unavailable.
type DanglingReferrersIndexError struct {
	InnerError   error
	IndexDigest  digest.Digest
	ReferrersTag string
	Subject      ocispec.Descriptor
}

// Error returns error msg of DanglingReferrerIndexError.
func (d *DanglingReferrersIndexError) Error() string {
	return fmt.Sprintf("failed to delete dangling referrers index %s for referrers tag %s: %s", d.IndexDigest.String(), d.ReferrersTag, d.InnerError.Error())
}

// Unwrap returns the inner error of DanglingReferrerIndexErr.
func (d *DanglingReferrersIndexError) Unwrap() error {
	return d.InnerError
}

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
	referrersMap := make(map[descriptor.Descriptor]int, len(referrers)+len(referrerChanges))
	updatedReferrers := make([]ocispec.Descriptor, 0, len(referrers)+len(referrerChanges))
	var updateRequired bool
	for _, r := range referrers {
		if content.Equal(r, ocispec.Descriptor{}) {
			// skip bad entry
			updateRequired = true
			continue
		}
		key := descriptor.FromOCI(r)
		if _, ok := referrersMap[key]; ok {
			// skip duplicates
			updateRequired = true
			continue
		}
		updatedReferrers = append(updatedReferrers, r)
		referrersMap[key] = len(updatedReferrers) - 1
	}

	// apply changes
	for _, change := range referrerChanges {
		key := descriptor.FromOCI(change.referrer)
		switch change.operation {
		case referrerOperationAdd:
			if _, ok := referrersMap[key]; !ok {
				// add distinct referrers
				updatedReferrers = append(updatedReferrers, change.referrer)
				referrersMap[key] = len(updatedReferrers) - 1
			}
		case referrerOperationRemove:
			if pos, ok := referrersMap[key]; ok {
				// remove referrers that are already in the map
				updatedReferrers[pos] = ocispec.Descriptor{}
				delete(referrersMap, key)
			}
		}
	}

	// skip unnecessary update
	if !updateRequired && len(referrersMap) == len(referrers) {
		// if the result referrer map contains the same content as the
		// original referrers, consider that there is no update on the
		// referrers.
		for _, r := range referrers {
			key := descriptor.FromOCI(r)
			if _, ok := referrersMap[key]; !ok {
				updateRequired = true
			}
		}
		if !updateRequired {
			return nil, errNoReferrerUpdate
		}
	}

	return removeEmptyDescriptors(updatedReferrers, len(referrersMap)), nil
}

// removeEmptyDescriptors in-place removes empty items from descs, given a hint
// of the number of non-empty descriptors.
func removeEmptyDescriptors(descs []ocispec.Descriptor, hint int) []ocispec.Descriptor {
	j := 0
	for i, r := range descs {
		if !content.Equal(r, ocispec.Descriptor{}) {
			if i > j {
				descs[j] = r
			}
			j++
		}
		if j == hint {
			break
		}
	}
	return descs[:j]
}
