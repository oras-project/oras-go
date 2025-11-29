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

// Registry contains configuration for connecting to a remote registry.
type Registry struct {
	// Registry is the registry hostname (e.g., "docker.io", "ghcr.io").
	Registry string

	// Namespace is the repository namespace within the registry.
	Namespace string

	// Namespace is the repository namespace within the registry.
	Transport Transport

	// Credential used for authentication.
	Credential Credential

	// Attributes contains registry-specific attributes.
	Attributes Attributes
}

// NewRegistry creates a new Registry property.
func NewRegistry(registry, namespace string) *Registry {
	return &Registry{
		Registry:  registry,
		Namespace: namespace,
		Transport: Transport{
			Insecure:    false,
			PlainHTTP:   false,
			HeaderFlags: make(map[string]string),
		},
		Attributes: Attributes{
			ReferrersAPI: ReferrersAPIUnknown,
		},
	}
}
