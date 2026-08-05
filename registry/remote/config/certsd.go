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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/internal/configpaths"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// defaultCertsDirPaths returns the default search paths for
// containers-certs.d directories using the current containers/image strategy.
func defaultCertsDirPaths() []string {
	return defaultCertsDirPathsWithStrategy(StrategyContainersImage)
}

// defaultCertsDirPathsWithStrategy returns the search paths for
// containers-certs.d directories using the specified strategy.
func defaultCertsDirPathsWithStrategy(strategy Strategy) []string {
	resolver := configpaths.NewResolver(configpaths.Strategy(strategy))
	return resolver.CertsDirPaths()
}

// CertsDir holds TLS certificate paths discovered from a
// containers-certs.d directory for a specific registry host.
type CertsDir struct {
	// CACertPaths contains paths to .crt files (CA certificates).
	CACertPaths []string

	// ClientCert is the path to a .cert file (client certificate).
	ClientCert string

	// ClientKey is the path to the matching .key file (client key).
	ClientKey string
}

// LoadCertsDir discovers TLS certificate files for the given registry
// host from the default containers-certs.d directories:
//  1. /etc/containers/certs.d/<host>/
//  2. $HOME/.config/containers/certs.d/<host>/
//
// Files from later directories are appended (not overridden).
// Returns nil if no certificate files are found.
func LoadCertsDir(host string) (*CertsDir, error) {
	return LoadCertsDirFromPaths(host, defaultCertsDirPaths())
}

// LoadCertsDirFromPaths discovers TLS certificate files for the given
// registry host from the specified base directories.
// Returns nil if no certificate files are found.
func LoadCertsDirFromPaths(host string, baseDirs []string) (*CertsDir, error) {
	var result *CertsDir

	for _, baseDir := range baseDirs {
		hostDir := filepath.Join(baseDir, host)
		info, err := os.Stat(hostDir)
		if err != nil || !info.IsDir() {
			continue
		}

		// Discover .crt files (CA certificates).
		crtFiles, err := filepath.Glob(filepath.Join(hostDir, "*.crt"))
		if err != nil {
			return nil, err
		}
		sort.Strings(crtFiles)

		// Discover .cert files (client certificates).
		certFiles, err := filepath.Glob(filepath.Join(hostDir, "*.cert"))
		if err != nil {
			return nil, err
		}
		sort.Strings(certFiles)

		if len(crtFiles) == 0 && len(certFiles) == 0 {
			continue
		}

		if result == nil {
			result = &CertsDir{}
		}

		result.CACertPaths = append(result.CACertPaths, crtFiles...)

		// Use the first .cert file found (across all dirs) that hasn't
		// been set yet.
		if result.ClientCert == "" && len(certFiles) > 0 {
			result.ClientCert = certFiles[0]
			// Look for a matching .key file with the same basename.
			base := strings.TrimSuffix(certFiles[0], ".cert")
			keyPath := base + ".key"
			if _, err := os.Stat(keyPath); err == nil {
				result.ClientKey = keyPath
			}
		}
	}

	return result, nil
}

// ApplyToTransport populates the Transport CACerts, Cert, and Key fields
// from the discovered certificate paths. Existing values are not overwritten.
func (cd *CertsDir) ApplyToTransport(t *properties.Transport) {
	if cd == nil {
		return
	}
	if len(cd.CACertPaths) > 0 && len(t.CACerts) == 0 && t.CACert == "" {
		t.CACerts = cd.CACertPaths
	}
	if cd.ClientCert != "" && t.Cert == "" {
		t.Cert = cd.ClientCert
	}
	if cd.ClientKey != "" && t.Key == "" {
		t.Key = cd.ClientKey
	}
}
