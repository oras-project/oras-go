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

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/errutil"
)

// TokenParams contains parameters for token acquisition.
type TokenParams struct {
	// Registry is the registry hostname (i.e. host:port).
	Registry string
	// Realm is the token endpoint URL.
	Realm string
	// Service is the service parameter from the WWW-Authenticate header.
	Service string
	// Scopes are the requested scopes for the token.
	Scopes []string
}

// TokenFetcher abstracts the token acquisition strategy.
type TokenFetcher interface {
	// FetchToken fetches an access token for the given parameters and credential.
	FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error)
}

// DistributionTokenFetcher implements distribution spec token endpoint (GET).
// This fetches tokens using the legacy distribution specification.
//
// References:
//   - https://distribution.github.io/distribution/spec/auth/jwt/
//   - https://distribution.github.io/distribution/spec/auth/token/
type DistributionTokenFetcher struct {
	// Client is the underlying HTTP client used to send requests.
	// If nil, http.DefaultClient is used.
	Client *http.Client
	// Header contains custom headers to be added to each request.
	Header http.Header
}

// FetchToken fetches an access token using the distribution spec token endpoint.
// It fetches anonymous tokens if no credential is provided.
func (f *DistributionTokenFetcher) FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params.Realm, nil)
	if err != nil {
		return "", err
	}
	if cred.Username != "" || cred.Password != "" {
		req.SetBasicAuth(cred.Username, cred.Password)
	}
	q := req.URL.Query()
	if params.Service != "" {
		q.Set("service", params.Service)
	}
	for _, scope := range params.Scopes {
		q.Add("scope", scope)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := f.send(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errutil.ParseErrorResponse(resp)
	}

	// As specified in https://distribution.github.io/distribution/spec/auth/token/ section
	// "Token Response Fields", the token is either in `token` or
	// `access_token`. If both present, they are identical.
	var result struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	lr := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(lr).Decode(&result); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	if result.AccessToken != "" {
		return result.AccessToken, nil
	}
	if result.Token != "" {
		return result.Token, nil
	}
	return "", fmt.Errorf("%s %q: empty token returned", resp.Request.Method, resp.Request.URL)
}

// send adds custom headers and sends the request.
func (f *DistributionTokenFetcher) send(req *http.Request) (*http.Response, error) {
	for key, values := range f.Header {
		req.Header[key] = append(req.Header[key], values...)
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// OAuth2TokenFetcher implements OAuth2 password/refresh_token grants (POST).
// This fetches tokens using the OAuth2 specification.
//
// Reference: https://distribution.github.io/distribution/spec/auth/oauth/
type OAuth2TokenFetcher struct {
	// Client is the underlying HTTP client used to send requests.
	// If nil, http.DefaultClient is used.
	Client *http.Client
	// Header contains custom headers to be added to each request.
	Header http.Header
	// ClientID is used in fetching OAuth2 token as a required field.
	// If empty, a default client ID is used.
	ClientID string
}

// FetchToken fetches an OAuth2 access token.
func (f *OAuth2TokenFetcher) FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error) {
	form := url.Values{}
	if cred.RefreshToken != "" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", cred.RefreshToken)
	} else if cred.Username != "" && cred.Password != "" {
		form.Set("grant_type", "password")
		form.Set("username", cred.Username)
		form.Set("password", cred.Password)
	} else {
		return "", errors.New("missing username or password for bearer auth")
	}
	form.Set("service", params.Service)
	clientID := f.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}
	form.Set("client_id", clientID)
	if len(params.Scopes) != 0 {
		form.Set("scope", strings.Join(params.Scopes, " "))
	}
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, params.Realm, body)
	if err != nil {
		return "", err
	}
	req.Header.Set(headerContentType, "application/x-www-form-urlencoded")

	resp, err := f.send(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errutil.ParseErrorResponse(resp)
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	lr := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(lr).Decode(&result); err != nil {
		return "", fmt.Errorf("%s %q: failed to decode response: %w", resp.Request.Method, resp.Request.URL, err)
	}
	if result.AccessToken != "" {
		return result.AccessToken, nil
	}
	return "", fmt.Errorf("%s %q: empty token returned", resp.Request.Method, resp.Request.URL)
}

// send adds custom headers and sends the request.
func (f *OAuth2TokenFetcher) send(req *http.Request) (*http.Response, error) {
	for key, values := range f.Header {
		req.Header[key] = append(req.Header[key], values...)
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// CompositeTokenFetcher selects strategy based on credential type and legacy mode.
// It delegates to either DistributionTokenFetcher or OAuth2TokenFetcher based
// on the credential and configuration.
type CompositeTokenFetcher struct {
	// Distribution is the fetcher for distribution spec tokens.
	Distribution TokenFetcher
	// OAuth2 is the fetcher for OAuth2 tokens.
	OAuth2 TokenFetcher
	// LegacyMode controls whether to use the legacy distribution spec
	// instead of OAuth2 with password grant when authenticating using
	// username and password.
	LegacyMode bool
}

// FetchToken selects the appropriate fetcher and delegates the token acquisition.
// The selection logic is:
//   - If credential has an access token, return it directly.
//   - If credential is empty or (has no refresh token and legacy mode is enabled),
//     use the distribution fetcher.
//   - Otherwise, use the OAuth2 fetcher.
func (f *CompositeTokenFetcher) FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error) {
	// Return access token directly if provided
	if cred.AccessToken != "" {
		return cred.AccessToken, nil
	}

	// Use distribution fetcher for empty credentials or legacy mode without refresh token
	if cred == credentials.EmptyCredential || (cred.RefreshToken == "" && f.LegacyMode) {
		return f.Distribution.FetchToken(ctx, params, cred)
	}

	// Use OAuth2 fetcher otherwise
	return f.OAuth2.FetchToken(ctx, params, cred)
}

// NewCompositeTokenFetcher creates a new CompositeTokenFetcher with default
// Distribution and OAuth2 fetchers using the provided client and header.
func NewCompositeTokenFetcher(client *http.Client, header http.Header, clientID string, legacyMode bool) *CompositeTokenFetcher {
	return &CompositeTokenFetcher{
		Distribution: &DistributionTokenFetcher{
			Client: client,
			Header: header,
		},
		OAuth2: &OAuth2TokenFetcher{
			Client:   client,
			Header:   header,
			ClientID: clientID,
		},
		LegacyMode: legacyMode,
	}
}
