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

package properties

// ReferrersAPI represents the Referrers API capability of a registry.
type ReferrersAPI int

const (
	// ReferrersAPIUnknown indicates that the Referrers API capability is unknown.
	ReferrersAPIUnknown ReferrersAPI = iota
	// ReferrersAPIYes indicates that the registry supports the Referrers API.
	ReferrersAPIYes
	// ReferrersAPINo indicates that the registry does not support the Referrers API.
	ReferrersAPINo
)

// String returns the string representation of ReferrersAPI.
func (r ReferrersAPI) String() string {
	switch r {
	case ReferrersAPIYes:
		return "yes"
	case ReferrersAPINo:
		return "no"
	default:
		return "unknown"
	}
}

// Attributes contains properties specific to the registry itself.
type Attributes struct {
	// ReferrersAPI indicates the Referrers API capability of the registry.
	// - ReferrersAPIYes: the registry supports the Referrers API
	// - ReferrersAPINo: the registry does not support the Referrers API
	// - ReferrersAPIUnknown: the capability is unknown and will be auto-detected
	ReferrersAPI ReferrersAPI
}
