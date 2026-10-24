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

// Package config loads container client configuration and converts it into
// registry client settings.
//
// [LoadConfigs] reads Docker config.json, containers auth.json,
// registries.conf, policy.json, registries.d, and containers-certs.d from
// their default locations. Missing files are skipped; an error is returned
// only when a file exists but cannot be parsed. [LoadConfigsWithOptions] can
// override individual paths.
//
// The default [StrategyContainersImage] follows containers/image path
// resolution and merge semantics. [StrategyUAPI] uses the experimental Podman
// 6 UAPI path resolution strategy.
//
// The resulting [Configs] bridges file configuration to remote clients.
// [Configs.RegistryProperties], [Configs.CredentialStore], and
// [Configs.PolicyEvaluator] provide the registry properties, credential store,
// and policy evaluator used to configure remote.ClientBuilder.
package config
