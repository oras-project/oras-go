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

import "strings"

// RegistriesDConfig represents a registries.d YAML configuration file.
// These files specify where signatures are stored for each registry.
// Reference: https://github.com/containers/image/blob/main/docs/containers-registries.d.5.md
type RegistriesDConfig struct {
	// DefaultDocker is the default configuration for all docker transport images.
	DefaultDocker *RegistriesDDockerConfig `yaml:"default-docker"`
	// Docker is a map of registry-specific configurations, keyed by namespace.
	Docker map[string]RegistriesDDockerConfig `yaml:"docker"`
}

// RegistriesDDockerConfig represents the docker-specific signature storage
// configuration for a registry namespace.
type RegistriesDDockerConfig struct {
	// Lookaside is the URL of the lookaside storage for reading signatures.
	Lookaside string `yaml:"lookaside"`
	// LookasideStaging is the URL of the lookaside storage for writing signatures.
	// If empty, Lookaside is used for both reading and writing.
	LookasideStaging string `yaml:"lookaside-staging"`
	// UseSigstoreAttachments indicates whether signatures should be stored
	// as OCI image attachments (sigstore format) instead of using lookaside storage.
	UseSigstoreAttachments bool `yaml:"use-sigstore-attachments"`

	// Legacy field names (sigstore was renamed to lookaside).
	// These are supported for backward compatibility.
	SigStore        string `yaml:"sigstore"`
	SigStoreStaging string `yaml:"sigstore-staging"`
}

// GetLookasideURLs returns the effective read and write lookaside URLs for the
// given image scope. It uses longest-prefix matching against the Docker
// namespace keys, falling back to the default-docker configuration.
//
// Returns empty strings if no configuration matches.
func (c *RegistriesDConfig) GetLookasideURLs(scope string) (readURL, writeURL string) {
	if c == nil {
		return "", ""
	}

	// Find the best matching Docker namespace entry.
	var bestMatch string
	var bestConfig RegistriesDDockerConfig

	for ns, cfg := range c.Docker {
		if !strings.HasPrefix(scope, ns) {
			continue
		}
		// Ensure prefix match is at a path boundary or exact match.
		if len(scope) > len(ns) && scope[len(ns)] != '/' {
			continue
		}
		if len(ns) > len(bestMatch) {
			bestMatch = ns
			bestConfig = cfg
		}
	}

	if bestMatch != "" {
		readURL = effectiveLookaside(bestConfig)
		writeURL = effectiveLookasideStaging(bestConfig)
		return readURL, writeURL
	}

	// Fall back to default-docker.
	if c.DefaultDocker != nil {
		readURL = effectiveLookaside(*c.DefaultDocker)
		writeURL = effectiveLookasideStaging(*c.DefaultDocker)
	}

	return readURL, writeURL
}

// effectiveLookaside returns the effective read URL, preferring the new
// "lookaside" field over the legacy "sigstore" field.
func effectiveLookaside(cfg RegistriesDDockerConfig) string {
	if cfg.Lookaside != "" {
		return cfg.Lookaside
	}
	return cfg.SigStore
}

// effectiveLookasideStaging returns the effective write URL. If no staging URL
// is configured, the read URL is used.
func effectiveLookasideStaging(cfg RegistriesDDockerConfig) string {
	if cfg.LookasideStaging != "" {
		return cfg.LookasideStaging
	}
	if cfg.SigStoreStaging != "" {
		return cfg.SigStoreStaging
	}
	// Fall back to the read URL.
	return effectiveLookaside(cfg)
}
