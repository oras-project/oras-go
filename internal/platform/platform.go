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

package platform

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// MatchPlatform checks whether the current platform matches the target platform.
// MatchPlatform will return true if all of the following conditions are met.
// - Architecture and OS exactly match.
// - Variant and OSVersion exactly match if target platform provided.
// - OSFeatures of the target platform are the subsets of the OSFeatures array
//   of the current platform.
// Note: Variant, OSVersion and OSFeatures are optional fields, will skip the
// comparison if the target platform does not provide specfic value.
func MatchPlatform(curr *ocispec.Platform, target *ocispec.Platform) bool {
	if curr.Architecture != target.Architecture || curr.OS != target.OS {
		return false
	}

	if target.OSVersion != "" && curr.OSVersion != target.OSVersion {
		return false
	}

	if target.Variant != "" && curr.Variant != target.Variant {
		return false
	}

	if len(target.OSFeatures) != 0 && !isSubset(target.OSFeatures, curr.OSFeatures) {
		return false
	}

	return true
}

// isSubset returns true if all items in slice A are present in slice B
func isSubset(a, b []string) bool {
	set := make(map[string]bool)
	for _, v := range b {
		set[v] = true
	}
	for _, v := range a {
		if _, ok := set[v]; !ok {
			return false
		}
	}

	return true
}
