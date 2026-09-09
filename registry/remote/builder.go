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

package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

// ClientBuilder creates auth.Client instances from registry properties.
// It handles TLS configuration, retry policies, caching, and credential
// resolution for connecting to container registries.
type ClientBuilder struct {
	// BaseTransport is the underlying HTTP transport.
	// If nil, http.DefaultTransport is used.
	BaseTransport http.RoundTripper

	// RetryPolicy returns a retry policy for HTTP requests.
	// If nil, retry.DefaultPolicy is used.
	RetryPolicy func() retry.Policy

	// CacheFactory creates a cache for the given registry.
	// If nil, a new cache is created for each registry.
	CacheFactory func(registry string) auth.Cache

	// CredentialStore is used to resolve credentials when not specified
	// in the registry properties.
	// If nil, no credential store fallback is used.
	CredentialStore credentials.Store

	// UserAgent is the User-Agent header value for HTTP requests.
	// If empty, no User-Agent header is set.
	UserAgent string

	// TokenFetcher is an optional custom token fetcher.
	// If nil, the default token fetching behavior is used.
	TokenFetcher auth.TokenFetcher

	// PolicyEvaluator is an optional policy evaluator for allow/deny decisions.
	// If set, repositories created by NewRepositoryWithProperties will
	// automatically enforce policy on read/write operations.
	PolicyEvaluator *policy.Evaluator

	// Logger enables HTTP request/response debug logging when non-nil.
	// Each retry attempt is logged individually. If nil, no logging transport
	// is added. Logger is also used for registry warnings when WarningLogger is
	// nil. Use slog.Default() to log to the default handler.
	Logger *slog.Logger

	// WarningLogger logs registry warnings at slog.LevelWarn, deduplicated per
	// registry. If nil, Logger is used for backward compatibility. If both are
	// nil, warnings are not logged.
	WarningLogger *slog.Logger
}

// NewClientBuilder creates a new ClientBuilder with default settings.
func NewClientBuilder() *ClientBuilder {
	return &ClientBuilder{
		BaseTransport: http.DefaultTransport,
		RetryPolicy:   func() retry.Policy { return retry.DefaultPolicy },
		CacheFactory:  func(registry string) auth.Cache { return auth.NewCache() },
	}
}

// Build creates an auth.Client for the given registry properties.
func (b *ClientBuilder) Build(props *properties.Registry) (*auth.Client, error) {
	if props == nil {
		return nil, fmt.Errorf("registry properties cannot be nil")
	}

	// Build TLS configuration
	tlsConfig, err := b.buildTLSConfig(props.Transport)
	if err != nil {
		return nil, fmt.Errorf("failed to configure TLS: %w", err)
	}

	// Build transport with TLS
	transport := b.buildTransport(tlsConfig)

	// Build HTTP client with retry
	httpClient := b.buildHTTPClient(transport)

	// Build credential function
	credentialFunc := b.buildCredentialFunc(props)

	// Build headers
	header := b.buildHeader(props.Transport)

	// Build cache
	var cache auth.Cache
	if b.CacheFactory != nil {
		cache = b.CacheFactory(props.Reference.Registry)
	}

	// Create auth client
	client := &auth.Client{
		Client:         httpClient,
		Header:         header,
		CredentialFunc: credentialFunc,
		Cache:          cache,
		TokenFetcher:   b.tokenFetcher(props, httpClient, header),
	}

	return client, nil
}

// tokenFetcher resolves the token acquisition strategy for a registry.
//
// An explicitly configured ClientBuilder.TokenFetcher wins: it is the caller
// speaking directly, whereas the registry properties come from a config file.
// Otherwise a distribution-spec flow requested via registries.conf is honoured
// by handing the client a composite fetcher in legacy mode. A nil result leaves
// the client on its own default, which is OAuth2.
func (b *ClientBuilder) tokenFetcher(props *properties.Registry, client *http.Client, header http.Header) auth.TokenFetcher {
	if b.TokenFetcher != nil {
		return b.TokenFetcher
	}
	if props.Attributes.TokenFlow == properties.TokenFlowDistribution {
		return auth.NewCompositeTokenFetcher(client, header, "", true)
	}
	return nil
}

// buildTLSConfig creates a TLS configuration from transport properties.
func (b *ClientBuilder) buildTLSConfig(transport properties.Transport) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: transport.Insecure,
	}

	// Collect all CA certificate paths.
	var caPaths []string
	if transport.CACert != "" {
		caPaths = append(caPaths, transport.CACert)
	}
	caPaths = append(caPaths, transport.CACerts...)

	if len(caPaths) > 0 {
		caCertPool := x509.NewCertPool()
		for _, p := range caPaths {
			caCert, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate %s: %w", p, err)
			}
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate %s", p)
			}
		}
		tlsConfig.RootCAs = caCertPool
	}

	// Load client certificate if specified
	if transport.Cert != "" && transport.Key != "" {
		cert, err := tls.LoadX509KeyPair(transport.Cert, transport.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// buildTransport creates an HTTP transport with TLS config.
func (b *ClientBuilder) buildTransport(tlsConfig *tls.Config) http.RoundTripper {
	// Clone the base transport or use default
	base := b.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}

	// If we have TLS config and the base is an *http.Transport, configure it
	if tlsConfig != nil {
		if httpTransport, ok := base.(*http.Transport); ok {
			// Clone the transport to avoid modifying the original
			cloned := httpTransport.Clone()
			cloned.TLSClientConfig = tlsConfig
			base = cloned
		}
	}

	// Wrap with retry transport
	var transport http.RoundTripper = &retry.Transport{
		Base:   base,
		Policy: b.RetryPolicy,
	}

	// Wrap with logging transport if a logger is configured.
	// Placed outside retry so each attempt is individually logged.
	if b.Logger != nil {
		transport = NewLoggingTransport(transport, b.Logger)
	}

	return transport
}

func (b *ClientBuilder) warningLogger() *slog.Logger {
	if b.WarningLogger != nil {
		return b.WarningLogger
	}
	return b.Logger
}

// buildHTTPClient creates an HTTP client with the given transport.
func (b *ClientBuilder) buildHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
	}
}

// buildCredentialFunc creates a credential function that resolves credentials
// from properties or falls back to the credential store.
//
// The credential carried in props belongs to one registry, so it is only
// returned for that registry's host. Every other host — a mirror, a redirect
// target, a bearer realm on another origin — resolves through the credential
// store, which is keyed per host. Returning the static credential
// unconditionally would hand it to whichever server the client happened to be
// talking to.
func (b *ClientBuilder) buildCredentialFunc(props *properties.Registry) credentials.CredentialFunc {
	// Match what the transport actually dials: Host() maps the "docker.io"
	// alias onto "registry-1.docker.io", and the credential func is called with
	// the request host.
	host := props.Reference.Host()

	return func(ctx context.Context, reg string) (credentials.Credential, error) {
		// Credential specified in properties, for its own registry only.
		if props.Credential != credentials.EmptyCredential && strings.EqualFold(reg, host) {
			return props.Credential, nil
		}

		// Fall back to credential store if available
		if b.CredentialStore != nil {
			cred, err := b.CredentialStore.Get(ctx, reg)
			if err != nil {
				return credentials.EmptyCredential, err
			}
			return cred, nil
		}

		return credentials.EmptyCredential, nil
	}
}

// buildHeader creates HTTP headers from transport properties.
func (b *ClientBuilder) buildHeader(transport properties.Transport) http.Header {
	header := http.Header{}

	// Set User-Agent if specified
	if b.UserAgent != "" {
		header.Set("User-Agent", b.UserAgent)
	}

	// Add custom headers from properties
	for key, value := range transport.HeaderFlags {
		header.Set(key, value)
	}

	return header
}

// NewRegistryWithProperties creates a Registry from registry properties
// using the given ClientBuilder.
func NewRegistryWithProperties(props *properties.Registry, builder *ClientBuilder) (*Registry, error) {
	if props == nil {
		return nil, fmt.Errorf("registry properties cannot be nil")
	}
	if builder == nil {
		builder = NewClientBuilder()
	}

	// Build auth client
	client, err := builder.Build(props)
	if err != nil {
		return nil, err
	}

	// Create registry
	reg := &Registry{
		Client:    client,
		Reference: properties.Reference{Registry: props.Reference.Registry},
		PlainHTTP: props.Transport.PlainHTTP,
		Policy:    builder.PolicyEvaluator,
	}

	if logger := builder.warningLogger(); logger != nil {
		reg.HandleWarning = NewWarningLogger(props.Reference.Registry, logger)
	}

	return reg, nil
}

// NewRepositoryWithProperties creates a Repository from registry properties
// using the given ClientBuilder.
func NewRepositoryWithProperties(props *properties.Registry, builder *ClientBuilder) (*Repository, error) {
	if builder == nil {
		builder = NewClientBuilder()
	}

	// Share one construction path with NewRegistryWithProperties so the two
	// cannot drift in what they configure.
	reg, err := NewRegistryWithProperties(props, builder)
	if err != nil {
		return nil, err
	}

	// Create repository
	repo := &Repository{
		Registry:       reg,
		RepositoryName: props.Reference.Repository,
	}

	// Set Referrers API capability if specified
	switch props.Attributes.ReferrersAPI {
	case properties.ReferrersAPISupported:
		repo.SetReferrersCapability(true)
	case properties.ReferrersAPIUnsupported:
		repo.SetReferrersCapability(false)
	}

	// Build mirror repositories
	mirrors, err := buildMirrorRepositories(props, builder)
	if err != nil {
		return nil, err
	}
	repo.mirrors = mirrors

	return repo, nil
}

// buildMirrorRepositories creates mirror Repository instances from the
// mirror properties. Each mirror gets its own auth.Client built from the
// mirror's transport settings.
func buildMirrorRepositories(props *properties.Registry, builder *ClientBuilder) ([]mirrorRepository, error) {
	if len(props.Mirrors) == 0 {
		return nil, nil
	}

	mirrors := make([]mirrorRepository, 0, len(props.Mirrors))
	for _, m := range props.Mirrors {
		// Build mirror properties from the mirror's own transport settings.
		//
		// The primary's credential is deliberately not propagated: a mirror is
		// a different host and may be operated by a different party. Mirror
		// credentials are resolved per host through the ClientBuilder's
		// credential store, the same way any other registry's are.
		mirrorProps := &properties.Registry{
			Reference: properties.Reference{
				Registry:   m.Location,
				Repository: props.Reference.Repository,
			},
			Transport:  m.Transport,
			Attributes: props.Attributes,
		}

		mirrorClient, err := builder.Build(mirrorProps)
		if err != nil {
			return nil, fmt.Errorf("failed to build mirror client for %s: %w", m.Location, err)
		}

		mirrorReg := &Registry{
			Client:    mirrorClient,
			Reference: properties.Reference{Registry: m.Location},
			PlainHTTP: m.Transport.PlainHTTP,
		}
		if logger := builder.warningLogger(); logger != nil {
			mirrorReg.HandleWarning = NewWarningLogger(m.Location, logger)
		}

		pullPolicy := m.PullFromMirror
		if pullPolicy == "" {
			pullPolicy = PullFromMirrorAll
		}

		mirrors = append(mirrors, mirrorRepository{
			Repository: &Repository{
				Registry:       mirrorReg,
				RepositoryName: props.Reference.Repository,
			},
			pullFromMirror: pullPolicy,
		})
	}

	return mirrors, nil
}
