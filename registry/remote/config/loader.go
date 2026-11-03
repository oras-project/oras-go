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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configpaths"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// Configs holds configuration loaded from Docker config.json, containers
// auth.json, registries.conf, policy.json, registries.d, and
// containers-certs.d. Pointer fields are nil if the corresponding file was not
// found.
type Configs struct {
	// DockerConfig is the loaded Docker config.json, or nil if not found.
	DockerConfig *Config

	// ContainersAuthConfig is the first loaded containers auth.json
	// (Podman/Buildah format), or nil if none were found. It is the same
	// pointer as ContainersAuthConfigs[0] when that slice is non-empty.
	// The auth.json format is identical to Docker config.json but uses
	// hierarchical namespace matching via GetAuthConfigHierarchical().
	// CredentialStore ignores it when ContainersAuthConfigs is non-empty.
	ContainersAuthConfig *Config

	// ContainersAuthConfigs is every loaded containers auth.json, in search
	// order (primary then fallbacks). Credential lookup uses the first file
	// that has a match. Nil or empty if none were found.
	ContainersAuthConfigs []*Config

	// RegistriesConfig is the loaded registries.conf, or nil if not found.
	RegistriesConfig *RegistriesConfig

	// PolicyConfig is the loaded containers-policy.json, or nil if not found.
	PolicyConfig *policy.Policy

	// RegistriesDConfig is the loaded registries.d signature storage config,
	// or nil if no configuration was found.
	RegistriesDConfig *RegistriesDConfig

	// CertsDirPaths is the resolved list of base directories for
	// containers-certs.d certificate discovery.
	CertsDirPaths []string
}

// LoadConfigsOptions configures LoadConfigs behavior.
type LoadConfigsOptions struct {
	// Strategy selects the configuration file path resolution approach.
	// Default (zero value) is [StrategyContainersImage].
	// Setting this to [StrategyUAPI] is EXPERIMENTAL and may change as
	// the upstream UAPI specification evolves.
	Strategy Strategy

	// DockerConfigPath overrides the Docker config.json path.
	// When empty, the default path is used ($DOCKER_CONFIG/config.json
	// or $HOME/.docker/config.json).
	DockerConfigPath string

	// ContainersAuthPath overrides the containers auth.json path.
	// When set, only that file is loaded (no fallbacks). When empty, the
	// strategy's primary path and fallbacks are searched.
	ContainersAuthPath string

	// RegistriesConfigPath overrides the registries.conf path.
	// When empty, the system default locations are searched.
	RegistriesConfigPath string

	// PolicyConfigPath overrides the containers-policy.json path.
	// When empty, the default locations are searched based on the Strategy.
	PolicyConfigPath string

	// RegistriesDPath overrides the registries.d directory path.
	// When empty, the system default locations are searched.
	RegistriesDPath string

	// CertsDirPaths overrides the containers-certs.d base directories.
	// When empty, the default paths are used based on the Strategy.
	CertsDirPaths []string
}

// LoadConfigs loads Docker config.json, containers auth.json, registries.conf,
// policy.json, registries.d, and containers-certs.d from their default
// locations. Missing files are silently skipped.
// Returns an error only if a file exists but cannot be parsed.
func LoadConfigs() (*Configs, error) {
	return LoadConfigsWithOptions(LoadConfigsOptions{})
}

// LoadConfigsWithOptions loads configs from specified or default paths.
// Missing files are silently skipped.
// Returns an error only if a file exists but cannot be parsed.
func LoadConfigsWithOptions(opts LoadConfigsOptions) (*Configs, error) {
	result := &Configs{}
	resolver := configpaths.NewResolver(configpaths.Strategy(opts.Strategy))

	// Load Docker config.
	dockerPath := opts.DockerConfigPath
	if dockerPath == "" {
		var err error
		dockerPath, err = resolver.DockerConfigPath()
		if err != nil {
			return nil, fmt.Errorf("failed to determine Docker config path: %w", err)
		}
	}
	if _, err := os.Stat(dockerPath); err == nil {
		cfg, err := Load(dockerPath)
		if err != nil {
			return nil, err
		}
		result.DockerConfig = cfg
	}

	// Load containers auth.json files.
	var authCandidates []string
	if opts.ContainersAuthPath != "" {
		authCandidates = []string{opts.ContainersAuthPath}
	} else {
		if authPath, err := resolver.AuthPrimaryPath(); err == nil {
			authCandidates = append(authCandidates, authPath)
		}
		authCandidates = append(authCandidates, resolver.AuthFallbackPaths()...)
	}
	var skipDocker []string
	if opts.ContainersAuthPath == "" {
		skipDocker = append(skipDocker, dockerPath)
		if p, err := resolver.DockerConfigPath(); err == nil {
			skipDocker = append(skipDocker, p)
		}
	}
	authCfgs, err := loadExistingAuthConfigs(authCandidates, skipDocker)
	if err != nil {
		return nil, err
	}
	if len(authCfgs) > 0 {
		result.ContainersAuthConfigs = authCfgs
		result.ContainersAuthConfig = authCfgs[0]
	}

	// Load registries.conf.
	if opts.RegistriesConfigPath != "" {
		if _, err := os.Stat(opts.RegistriesConfigPath); err == nil {
			cfg, err := LoadRegistriesConfig(opts.RegistriesConfigPath)
			if err != nil {
				return nil, err
			}
			result.RegistriesConfig = cfg
		}
	} else {
		var cfg *RegistriesConfig
		var err error
		if opts.Strategy == StrategyContainersImage {
			cfg, err = LoadSystemRegistriesConfig()
		} else {
			cfg, err = LoadSystemRegistriesConfigWithStrategy(opts.Strategy)
		}
		if err != nil {
			if !errors.Is(err, ErrRegistriesConfigNotFound) {
				return nil, err
			}
		} else {
			result.RegistriesConfig = cfg
		}
	}

	// Load policy config.
	policyPath := opts.PolicyConfigPath
	if policyPath == "" {
		policyPaths := resolver.PolicyPaths()
		for _, p := range policyPaths {
			if _, err := os.Stat(p); err == nil {
				policyPath = p
				break
			}
		}
	}
	if policyPath != "" {
		if _, err := os.Stat(policyPath); err == nil {
			pol, err := policy.LoadPolicy(policyPath)
			if err != nil {
				return nil, err
			}
			result.PolicyConfig = pol
		}
	}

	// Load registries.d signature storage config.
	if opts.RegistriesDPath != "" {
		if _, err := os.Stat(opts.RegistriesDPath); err == nil {
			cfg, err := loadRegistriesDDir(nil, opts.RegistriesDPath)
			if err != nil {
				return nil, err
			}
			result.RegistriesDConfig = cfg
		}
	} else {
		var cfg *RegistriesDConfig
		var err error
		if opts.Strategy == StrategyContainersImage {
			cfg, err = LoadSystemRegistriesDConfig()
		} else {
			cfg, err = LoadSystemRegistriesDConfigWithStrategy(opts.Strategy)
		}
		if err != nil {
			return nil, err
		}
		if cfg.DefaultDocker != nil || len(cfg.Docker) > 0 {
			result.RegistriesDConfig = cfg
		}
	}

	// Populate certs.d paths.
	if len(opts.CertsDirPaths) > 0 {
		result.CertsDirPaths = opts.CertsDirPaths
	} else {
		result.CertsDirPaths = defaultCertsDirPathsWithStrategy(opts.Strategy)
	}

	return result, nil
}

// RegistryProperties creates a [properties.Registry] for the given reference
// string by combining settings from RegistriesConfig and CertsDir.
//
// It performs the following steps:
//  1. Creates base properties from RegistriesConfig (or plain reference parsing
//     if RegistriesConfig is nil).
//  2. Loads and applies TLS certificates from CertsDirPaths for the resolved
//     registry host.
func (c *Configs) RegistryProperties(ref string) (*properties.Registry, error) {
	props, err := NewRegistryProperties(ref, c.RegistriesConfig)
	if err != nil {
		return nil, err
	}

	if len(c.CertsDirPaths) > 0 {
		certs, err := LoadCertsDirFromPaths(props.Reference.Host(), c.CertsDirPaths)
		if err != nil {
			return nil, fmt.Errorf("failed to load certs for %s: %w", props.Reference.Host(), err)
		}
		if certs != nil {
			certs.ApplyToTransport(&props.Transport)
		}
	}

	return props, nil
}

// PolicyEvaluator creates a [policy.Evaluator] from the loaded policy config.
// Returns (nil, nil) if no policy configuration was loaded.
func (c *Configs) PolicyEvaluator(opts ...policy.EvaluatorOption) (*policy.Evaluator, error) {
	if c.PolicyConfig == nil {
		return nil, nil
	}
	return policy.NewEvaluator(c.PolicyConfig, opts...)
}

// CredentialStore creates a [credentials.Store] from loaded containers
// auth.json files and Docker config. Each containers auth.json is a
// hierarchical store, in search order, and is checked before Docker config.
// Get uses the first store with a match. Put writes the first store. Delete
// removes from all stores. Missing auth.json files are not created. An
// auth.json with no credentials configured uses the platform's default
// native store, as [credentials.NewStoreFromConfig] does.
//
// A helper that fails erase, or reports not-found in a non-standard way,
// makes Delete return that error after the other stores are still cleared.
//
// Returns an error if neither a containers auth.json nor DockerConfig is loaded.
func (c *Configs) CredentialStore(opts credentials.StoreOptions) (credentials.Store, error) {
	var stores []credentials.Store

	authCfgs := c.ContainersAuthConfigs
	if len(authCfgs) == 0 && c.ContainersAuthConfig != nil {
		authCfgs = []*Config{c.ContainersAuthConfig}
	}
	authOpts := opts
	authOpts.Hierarchical = true
	for _, cfg := range authCfgs {
		stores = append(stores, credentials.NewStoreFromConfig(cfg, authOpts))
	}
	if c.DockerConfig != nil {
		stores = append(stores, credentials.NewStoreFromConfig(c.DockerConfig, opts))
	}

	if len(stores) == 0 {
		return nil, fmt.Errorf("no credential configurations found")
	}
	if len(stores) == 1 {
		return stores[0], nil
	}
	inner := credentials.NewStoreWithFallbacks(stores[0], stores[1:]...)
	return &credentialStore{inner: inner, stores: stores}, nil
}

// credentialStore wraps a fallback store so Get and Put keep first-store
// semantics while Delete removes the credential from every store.
type credentialStore struct {
	inner  credentials.Store
	stores []credentials.Store
}

var (
	_ credentials.Store            = (*credentialStore)(nil)
	_ credentials.NamespaceMatcher = (*credentialStore)(nil)
)

func (s *credentialStore) Get(ctx context.Context, serverAddress string) (credentials.Credential, error) {
	return s.inner.Get(ctx, serverAddress)
}

func (s *credentialStore) Put(ctx context.Context, serverAddress string, cred credentials.Credential) error {
	return s.inner.Put(ctx, serverAddress, cred)
}

func (s *credentialStore) Delete(ctx context.Context, serverAddress string) error {
	var errs []error
	for _, store := range s.stores {
		if err := store.Delete(ctx, serverAddress); err != nil && !errors.Is(err, credentials.ErrCredentialsNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// MatchesNamespace reports whether Get performs most-specific-first namespace
// matching for serverAddress. It delegates to the inner fallback store so
// [NewCredentialFunc] keeps the same type-assertion behavior.
func (s *credentialStore) MatchesNamespace(serverAddress string) bool {
	matcher, ok := s.inner.(credentials.NamespaceMatcher)
	return ok && matcher.MatchesNamespace(serverAddress)
}

// loadExistingAuthConfigs Stat+Loads each candidate that exists. Paths are
// deduped with filepath.Clean. skipDocker paths (the effective Docker config
// and the resolver default) are compared with filepath.Clean and omitted so
// Docker credentials stay on DockerConfig. Missing files are skipped; a file
// that exists but cannot be parsed is an error. Missing paths are never
// passed to Load.
func loadExistingAuthConfigs(candidates []string, skipDocker []string) ([]*Config, error) {
	skip := make(map[string]struct{}, len(skipDocker))
	for _, p := range skipDocker {
		if p != "" {
			skip[filepath.Clean(p)] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	var cfgs []*Config
	for _, p := range candidates {
		if p == "" {
			continue
		}
		cleaned := filepath.Clean(p)
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		if _, skipDockerPath := skip[cleaned]; skipDockerPath {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cfg, err := Load(p)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, cfg)
	}
	return cfgs, nil
}
