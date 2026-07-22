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

import (
	"errors"
	"fmt"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// ErrRegistryBlocked is returned when a registry is blocked by the
// registries configuration.
var ErrRegistryBlocked = errors.New("registry is blocked")

// RegistryProperties creates a [properties.Registry] for the given reference
// string using transport settings from the registries configuration.
//
// It performs the following steps:
//  1. Resolves aliases (short names to fully qualified references).
//  2. Checks if the registry is blocked (returns [ErrRegistryBlocked]).
//  3. Rewrites the reference using Location rules.
//  4. Applies transport settings (Insecure) from the matching registry entry.
func (rc *RegistriesConfig) RegistryProperties(ref string) (*properties.Registry, error) {
	return NewRegistryProperties(ref, rc)
}

// NewRegistryProperties creates a [properties.Registry] from a reference
// string with optional [RegistriesConfig] for transport settings.
// If regConf is nil, only reference parsing is performed.
func NewRegistryProperties(ref string, regConf *RegistriesConfig) (*properties.Registry, error) {
	var origReg *Registry
	if regConf != nil {
		// Step 1: Resolve aliases.
		if resolved, ok := regConf.ResolveAlias(ref); ok {
			ref = resolved
		}

		// Step 2: Check if the registry is blocked.
		if regConf.IsBlocked(ref) {
			return nil, fmt.Errorf("%w: %s", ErrRegistryBlocked, ref)
		}

		// Find the matching registry entry before rewriting, since mirrors
		// and transport settings are associated with the original prefix.
		origReg = regConf.FindRegistry(ref)

		// Step 3: Rewrite reference using Location rules.
		ref = regConf.RewriteReference(ref)
	}

	// Step 4: Parse the reference into properties.
	props, err := properties.NewRegistry(ref)
	if err != nil {
		return nil, err
	}

	// Step 5: Apply transport settings, attributes, and mirrors from the
	// original (pre-rewrite) registry entry.
	if origReg != nil {
		if origReg.Insecure {
			props.Transport.Insecure = true
		}

		if origReg.ForceBasicAuth {
			props.Attributes.ForceBasicAuth = true
		}

		switch origReg.ReferrersAPI {
		case "supported":
			props.Attributes.ReferrersAPI = properties.ReferrersAPISupported
		case "unsupported":
			props.Attributes.ReferrersAPI = properties.ReferrersAPIUnsupported
		}

		props.Attributes.RepositoryListPageSize = origReg.RepositoryListPageSize
		props.Attributes.TagListPageSize = origReg.TagListPageSize
		props.Attributes.ReferrerListPageSize = origReg.ReferrerListPageSize

		// Step 6: Populate mirrors.
		if len(origReg.Mirrors) > 0 {
			props.Mirrors = make([]properties.Mirror, len(origReg.Mirrors))
			for i, m := range origReg.Mirrors {
				pullFrom := m.PullFromMirror
				if pullFrom == "" && origReg.MirrorByDigestOnly {
					pullFrom = "digest-only"
				}
				props.Mirrors[i] = properties.Mirror{
					Location:       m.Location,
					PullFromMirror: pullFrom,
					Transport: properties.Transport{
						Insecure:    m.Insecure,
						HeaderFlags: make(map[string]string),
					},
				}
			}
		}
	}

	return props, nil
}

// SearchRegistryProperties returns properties for each unqualified search
// registry combined with the given image name. This supports the
// UnqualifiedSearchRegistries feature from registries.conf.
//
// For example, if UnqualifiedSearchRegistries contains ["docker.io", "quay.io"]
// and imageName is "library/alpine:latest", this returns properties for
// "docker.io/library/alpine:latest" and "quay.io/library/alpine:latest".
func (rc *RegistriesConfig) SearchRegistryProperties(imageName string) ([]*properties.Registry, error) {
	if rc == nil || len(rc.UnqualifiedSearchRegistries) == 0 {
		return nil, nil
	}

	result := make([]*properties.Registry, 0, len(rc.UnqualifiedSearchRegistries))
	for _, registry := range rc.UnqualifiedSearchRegistries {
		ref := registry + "/" + imageName
		props, err := rc.RegistryProperties(ref)
		if err != nil {
			// Skip blocked registries in search results.
			if errors.Is(err, ErrRegistryBlocked) {
				continue
			}
			return nil, fmt.Errorf("failed to create properties for %s: %w", ref, err)
		}
		result = append(result, props)
	}

	return result, nil
}
