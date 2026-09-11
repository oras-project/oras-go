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

// Package auth provides authentication for a client to a remote registry.
package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

// HTTP header names used in authentication.
const (
	headerAuthorization   = "Authorization"
	headerContentType     = "Content-Type"
	headerUserAgent       = "User-Agent"
	headerWWWAuthenticate = "Www-Authenticate"
)

// ErrBasicCredentialNotFound is returned  when the credential is not found for
// basic auth.
var ErrBasicCredentialNotFound = errors.New("basic credential not found")

// DefaultClient is the default auth-decorated client.
var DefaultClient = &Client{
	Client: retry.DefaultClient,
	Header: http.Header{
		headerUserAgent: {"oras-go"},
	},
	Cache: DefaultCache,
}

// maxResponseBytes specifies the default limit on how many response bytes are
// allowed in the server's response from authorization service servers.
// A typical response message from authorization service servers is around 1 to
// 4 KiB. Since the size of a token must be smaller than the HTTP header size
// limit, which is usually 16 KiB. As specified by the distribution, the
// response may contain 2 identical tokens, that is, 16 x 2 = 32 KiB.
// Hence, 128 KiB should be sufficient.
// References: https://distribution.github.io/distribution/spec/auth/token/
var maxResponseBytes int64 = 128 * 1024 // 128 KiB

// defaultClientID specifies the default client ID used in OAuth2.
// See also ClientID.
var defaultClientID = "oras-go"

// Client is an auth-decorated HTTP client.
// Its zero value is a usable client that uses http.DefaultClient with no cache.
type Client struct {
	// Client is the underlying HTTP client used to access the remote
	// server.
	// If nil, http.DefaultClient is used.
	// It is possible to use the default retry client from the package
	// `github.com/oras-project/oras-go/v3/registry/remote/retry`. That client
	// is already available in the DefaultClient.
	// It is also possible to use a custom client. For example, github.com/hashicorp/go-retryablehttp
	// is a popular HTTP client that supports retries.
	Client *http.Client

	// Header contains the custom headers to be added to each request.
	Header http.Header

	// CredentialFunc specifies the function for resolving the credential for the
	// given registry (i.e. host:port).
	// EmptyCredential is a valid return value and should not be considered as
	// an error.
	// If nil, the credential is always resolved to EmptyCredential.
	CredentialFunc credentials.CredentialFunc

	// Cache caches credentials for direct accessing the remote registry.
	// If nil, no cache is used.
	Cache Cache

	// ClientID used in fetching OAuth2 token as a required field.
	// If empty, a default client ID is used.
	// Reference: https://distribution.github.io/distribution/spec/auth/oauth/#getting-a-token
	ClientID string

	// TokenFetcher determines how a bearer token is acquired when the registry
	// issues a Bearer challenge.
	//
	// If nil, the client fetches an anonymous token via the distribution spec
	// when there is no credential, and otherwise uses OAuth2 with password
	// grant. To follow the legacy distribution spec instead — the approach used
	// in oras-go v1 and v2 — set a composite fetcher in legacy mode:
	//
	//	client.TokenFetcher = auth.NewCompositeTokenFetcher(
	//		client.Client, client.Header, client.ClientID, true)
	//
	// Note that in either case the registry itself is authenticated with a
	// bearer token; the two differ only in how that token is obtained. The
	// authentication scheme itself is not configurable — it is whatever the
	// registry advertises in its WWW-Authenticate challenge.
	//
	// References:
	// - https://distribution.github.io/distribution/spec/auth/jwt/
	// - https://distribution.github.io/distribution/spec/auth/oauth/
	TokenFetcher TokenFetcher
}

// client returns an HTTP client used to access the remote registry.
// http.DefaultClient is return if the client is not configured.
func (c *Client) client() *http.Client {
	if c.Client == nil {
		return http.DefaultClient
	}
	return c.Client
}

// send adds headers to the request and sends the request to the remote server.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	for key, values := range c.Header {
		req.Header[key] = append(req.Header[key], values...)
	}
	return redirectSafeClient(c.client()).Do(req)
}

// redirectSafeClient returns a shallow copy of client whose CheckRedirect drops
// the Authorization header when a redirect crosses an HTTP origin (scheme,
// host, or port). The standard library only strips sensitive headers when the
// hostname changes, so a redirect to a different port on the same host would
// otherwise forward credentials to an unintended endpoint. Any caller-provided
// CheckRedirect is preserved.
//
// This applies to token endpoint requests as well as registry requests: the
// distribution spec flow sends the credential to the realm as HTTP Basic.
//
// Reference: https://github.com/oras-project/oras-go/security/advisories/GHSA-vh4v-2xq2-g5cg
func redirectSafeClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	checkRedirect := client.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameHTTPOrigin(via[len(via)-1].URL, req.URL) {
			req.Header.Del(headerAuthorization)
		}
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		return nil
	}
	return &clientCopy
}

// sameHTTPOrigin reports whether a and b share the same HTTP origin, i.e. the
// same scheme and host. Default ports are normalized so that, for example,
// "example.com" and "example.com:443" compare equal over https.
func sameHTTPOrigin(a, b *url.URL) bool {
	if !strings.EqualFold(a.Scheme, b.Scheme) {
		return false
	}
	return canonicalHost(a) == canonicalHost(b)
}

// canonicalHost returns the lower-cased host of u with the default port for its
// scheme applied when no explicit port is present.
func canonicalHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// credential resolves the credential for the given registry.
func (c *Client) credential(ctx context.Context, reg string) (credentials.Credential, error) {
	if c.CredentialFunc == nil {
		return credentials.EmptyCredential, nil
	}
	return c.CredentialFunc(ctx, reg)
}

// cache resolves the cache.
// noCache is return if the cache is not configured.
func (c *Client) cache() Cache {
	if c.Cache == nil {
		return noCache{}
	}
	return c.Cache
}

// validateRealm rejects bearer token realm URLs that would have the client
// forward credentials to obviously unsafe destinations:
//
//   - schemes other than http or https,
//   - http realms when the registry was contacted over https (TLS downgrade),
//   - hosts that are IP literals in loopback, link-local, private, or
//     unspecified ranges (e.g. cloud instance metadata services such as
//     169.254.169.254).
//
// Cross-host realms with a public hostname are permitted, because the
// distribution spec allows a separate token endpoint (e.g. Docker Hub's
// auth.docker.io). When the registry itself is reached at the same hostname
// as the realm, the IP-literal check is skipped so loopback and in-cluster
// deployments continue to work.
func validateRealm(realm string, registryURL *url.URL) error {
	if realm == "" {
		return nil
	}
	realmURL, err := url.Parse(realm)
	if err != nil {
		return fmt.Errorf("failed to parse bearer realm %q: %w", realm, err)
	}
	switch realmURL.Scheme {
	case "https":
		// always allowed
	case "http":
		if registryURL != nil && registryURL.Scheme == "https" {
			return fmt.Errorf("bearer realm %q uses http but registry was contacted over https", realm)
		}
	default:
		return fmt.Errorf("bearer realm %q uses unsupported scheme %q", realm, realmURL.Scheme)
	}
	if ip := net.ParseIP(realmURL.Hostname()); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsPrivate() || ip.IsUnspecified() {
			if registryURL == nil || realmURL.Hostname() != registryURL.Hostname() {
				return fmt.Errorf("bearer realm host %q is a loopback, link-local, private, or unspecified address", realmURL.Hostname())
			}
		}
	}
	return nil
}

// SetUserAgent sets the user agent for all out-going requests.
func (c *Client) SetUserAgent(userAgent string) {
	if c.Header == nil {
		c.Header = http.Header{}
	}
	c.Header.Set(headerUserAgent, userAgent)
}

// Do sends the request to the remote server, attempting to resolve
// authentication if 'Authorization' header is not set.
//
// On authentication failure due to bad credential,
//   - Do returns error if it fails to fetch token for bearer auth.
//   - Do returns the registry response without error for basic auth.
func (c *Client) Do(originalReq *http.Request) (*http.Response, error) {
	if auth := originalReq.Header.Get(headerAuthorization); auth != "" {
		return c.send(originalReq)
	}

	ctx := originalReq.Context()
	req := originalReq.Clone(ctx)

	// attempt cached auth token
	var attemptedKey string
	cache := c.cache()
	host := originalReq.Host
	if host == "" {
		host = originalReq.URL.Host
	}
	scheme, err := cache.GetScheme(ctx, host)
	if err == nil {
		switch scheme {
		case SchemeBasic:
			token, err := cache.GetToken(ctx, host, SchemeBasic, "")
			if err == nil {
				req.Header.Set(headerAuthorization, "Basic "+token)
			}
		case SchemeBearer:
			scopes := GetScopesForHost(ctx, host)
			attemptedKey = strings.Join(scopes, " ")
			token, err := cache.GetToken(ctx, host, SchemeBearer, attemptedKey)
			if err == nil {
				req.Header.Set(headerAuthorization, "Bearer "+token)
			}
		}
	}

	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// If the challenge came from a different origin than originally requested
	// (e.g. the request was redirected to another host or port), do not resolve
	// or send the registry credentials to that origin.
	// Reference: https://github.com/oras-project/oras-go/security/advisories/GHSA-vh4v-2xq2-g5cg
	if resp.Request != nil && !sameHTTPOrigin(originalReq.URL, resp.Request.URL) {
		return resp, nil
	}

	// attempt again with credentials for recognized schemes
	challenge := resp.Header.Get(headerWWWAuthenticate)
	scheme, params := parseChallenge(challenge)
	switch scheme {
	case SchemeBasic:
		resp.Body.Close()

		token, err := cache.Set(ctx, host, SchemeBasic, "", func(ctx context.Context) (string, error) {
			return c.fetchBasicAuth(ctx, host)
		})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, err)
		}

		req = originalReq.Clone(ctx)
		req.Header.Set(headerAuthorization, "Basic "+token)
	case SchemeBearer:
		resp.Body.Close()

		scopes := GetScopesForHost(ctx, host)
		if paramScope := params["scope"]; paramScope != "" {
			// merge hinted scopes with challenged scopes
			scopes = append(scopes, strings.Split(paramScope, " ")...)
			scopes = CleanScopes(scopes)
		}
		key := strings.Join(scopes, " ")

		// attempt the cache again if there is a scope change
		if key != attemptedKey {
			if token, err := cache.GetToken(ctx, host, SchemeBearer, key); err == nil {
				req = originalReq.Clone(ctx)
				req.Header.Set(headerAuthorization, "Bearer "+token)
				if err := rewindRequestBody(req); err != nil {
					return nil, err
				}

				resp, err := c.send(req)
				if err != nil {
					return nil, err
				}
				if resp.StatusCode != http.StatusUnauthorized {
					return resp, nil
				}
				resp.Body.Close()
			}
		}

		// attempt with credentials
		realm := params["realm"]
		if err := validateRealm(realm, originalReq.URL); err != nil {
			return nil, fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, err)
		}
		service := params["service"]
		token, err := cache.Set(ctx, host, SchemeBearer, key, func(ctx context.Context) (string, error) {
			return c.fetchBearerToken(ctx, host, realm, service, scopes)
		})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, err)
		}

		req = originalReq.Clone(ctx)
		req.Header.Set(headerAuthorization, "Bearer "+token)
	default:
		return resp, nil
	}
	if err := rewindRequestBody(req); err != nil {
		return nil, err
	}

	return c.send(req)
}

// fetchBasicAuth fetches a basic auth token for the basic challenge.
func (c *Client) fetchBasicAuth(ctx context.Context, registry string) (string, error) {
	cred, err := c.credential(ctx, registry)
	if err != nil {
		return "", fmt.Errorf("failed to resolve credential: %w", err)
	}
	if cred == credentials.EmptyCredential {
		return "", ErrBasicCredentialNotFound
	}
	if cred.Username == "" || cred.Password == "" {
		return "", errors.New("missing username or password for basic auth")
	}
	auth := cred.Username + ":" + cred.Password
	return base64.StdEncoding.EncodeToString([]byte(auth)), nil
}

// fetchBearerToken fetches an access token for the bearer challenge.
//
// The acquisition strategy is delegated to TokenFetcher. When none is
// configured, a CompositeTokenFetcher is used: anonymous access goes through
// the distribution spec token endpoint and credentialed access uses OAuth2.
func (c *Client) fetchBearerToken(ctx context.Context, registry, realm, service string, scopes []string) (string, error) {
	cred, err := c.credential(ctx, registry)
	if err != nil {
		return "", err
	}

	fetcher := c.TokenFetcher
	if fetcher == nil {
		fetcher = NewCompositeTokenFetcher(c.Client, c.Header, c.ClientID, false)
	}
	params := TokenParams{
		Registry: registry,
		Realm:    realm,
		Service:  service,
		Scopes:   scopes,
	}
	return fetcher.FetchToken(ctx, params, cred)
}

// rewindRequestBody tries to rewind the request body if exists.
func rewindRequestBody(req *http.Request) error {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody == nil {
		return fmt.Errorf("%s %q: request body is not rewindable", req.Method, req.URL)
	}
	body, err := req.GetBody()
	if err != nil {
		return fmt.Errorf("%s %q: failed to get request body: %w", req.Method, req.URL, err)
	}
	req.Body = body
	return nil
}
