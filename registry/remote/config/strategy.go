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

package config

// Strategy selects the configuration file path resolution approach.
type Strategy int

const (
	// StrategyContainersImage uses the current containers/image two-tier path
	// resolution (system + user). This is the default.
	StrategyContainersImage Strategy = iota

	// StrategyUAPI uses the Podman 6 UAPI-based three-tier path resolution
	// (vendor + system + user) with rootful/rootless drop-in directories.
	// EXPERIMENTAL: behavior may change as the upstream UAPI specification evolves.
	// Reference: https://uapi-group.org/specifications/specs/configuration_files_specification/
	StrategyUAPI
)
