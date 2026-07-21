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

// Strategy specifies the configuration file resolution strategy.
type Strategy int

const (
	// StrategyContainersImage uses the current containers/image path
	// resolution with two tiers (system + user) and merge-all semantics.
	// This is the default.
	StrategyContainersImage Strategy = iota

	// StrategyUAPI uses the Podman 6 UAPI-based path resolution with
	// three tiers (vendor + system + user), first-found-wins for main
	// config files, and rootful/rootless drop-in directories.
	//
	// EXPERIMENTAL: This strategy is based on the Podman 6 design proposal
	// and the UAPI configuration files specification. The behavior may
	// change as the upstream specification evolves.
	//
	// Reference: https://uapi-group.org/specifications/specs/configuration_files_specification/
	StrategyUAPI
)
