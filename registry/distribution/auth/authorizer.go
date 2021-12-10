package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxResponseBytes specifies the default limit on how many response bytes are
// allowed in the server's response from authorization service servers.
// A typical response message from authorization service servers is around 1 to
// 4 KiB. Since the size of a token must be smaller than the HTTP header size
// limit, which is usually 16 KiB. As specified by the distribution, the
// response may contain 2 identical tokens, that is, 16 x 2 = 32 KiB.
// Hence, 128 KiB should be sufficient.
// References: https://docs.docker.com/registry/spec/auth/token/
var maxResponseBytes int64 = 128 * 1024 // 128 KiB

// defaultClientID specifies the default client ID used in OAuth2.
// See also `Authorizer.ClientID`.
var defaultClientID = "oras-go"

// Authorizer is an auth-decorated HTTP client.
type Authorizer struct {
	// Transport is the underlying HTTP transport used to access the remote
	// server.
	// If nil, a default HTTP transport is used.
	Client *http.Client

	// Header contains the custom headers to be added to each request.
	Header http.Header

	// Credential specifies the function for resolving the credential for the
	// given registry (i.e. host:port).
	// `EmptyCredential` is a valid return value and should not be considered as
	// an error.
	// If nil, the credential is always resolved to `EmptyCredential`.
	Credential func(string) (Credential, error)

	// ClientID used in fetching OAuth2 token as a required field.
	// If empty, a default client ID is used.
	// Reference: https://docs.docker.com/registry/spec/auth/oauth/#getting-a-token
	ClientID string
}

// client returns an HTTP client used to access the remote registry.
// http.DefaultClient is return if the client is not configured.
func (a *Authorizer) client() *http.Client {
	if a.Client == nil {
		return http.DefaultClient
	}
	return a.Client
}

// send adds headers to the request and sends the request to the remote server.
func (a *Authorizer) send(req *http.Request) (*http.Response, error) {
	for key, values := range a.Header {
		req.Header[key] = append(req.Header[key], values...)
	}
	return a.client().Do(req)
}

// SetUserAgent sets the user agent for all out-going requests.
func (a *Authorizer) SetUserAgent(userAgent string) {
	a.Header.Set("User-Agent", userAgent)
}

// Do sends the request to the remote server with resolving authentication
// attempted.
func (a *Authorizer) Do(originalReq *http.Request) (*http.Response, error) {
	ctx := originalReq.Context()
	req := originalReq.Clone(ctx)
	resp, err := a.send(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("Www-Authenticate")
	scheme, params := parseChallenge(challenge)
	switch scheme {
	case "basic":
		if a.Credential == nil {
			return resp, nil
		}
		resp.Body.Close()

		creds, err := a.Credential(req.Host)
		if err != nil {
			return nil, fmt.Errorf("%s %q: failed to resolve credential: %v", resp.Request.Method, resp.Request.URL, err)
		}
		if creds.Username == "" {
			return nil, fmt.Errorf("%s %q: username required for basic auth", resp.Request.Method, resp.Request.URL)
		}

		req = originalReq.Clone(ctx)
		req.SetBasicAuth(creds.Username, creds.Password)
	case "bearer":
		resp.Body.Close()

		token, resp, err := a.fetchToken(ctx, req.Host, params)
		if err != nil {
			return nil, fmt.Errorf("%s %q: failed to fetch token: %v", resp.Request.Method, resp.Request.URL, err)
		}
		if resp != nil {
			return resp, nil
		}

		req = originalReq.Clone(ctx)
		req.Header.Set("Authorization", "Bearer "+token)
	default:
		return resp, nil
	}

	return a.send(req)
}

// fetchToken fetches an access token for the bearer challenge.
func (a *Authorizer) fetchToken(ctx context.Context, host string, params map[string]string) (string, *http.Response, error) {
	if a.Credential == nil {
		return a.fetchDistributionToken(ctx, params, "", "")
	}
	creds, err := a.Credential(host)
	if err != nil {
		return "", nil, err
	}
	if creds.RefreshToken == "" {
		return a.fetchDistributionToken(ctx, params, creds.Username, creds.Password)
	}
	return a.fetchOAuth2Token(ctx, params, creds.RefreshToken)
}

// fetchDistributionToken fetches an access token as defined by the distribution
// specification.
// It fetches anonymous tokens if no credential is provided.
// References:
// - https://docs.docker.com/registry/spec/auth/jwt/
// - https://docs.docker.com/registry/spec/auth/token/
func (a *Authorizer) fetchDistributionToken(ctx context.Context, params map[string]string, username, password string) (string, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params["realm"], nil)
	if err != nil {
		return "", nil, err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	q := req.URL.Query()
	if service, ok := params["service"]; ok {
		q.Set("service", service)
	}
	if scope, ok := params["scope"]; ok {
		q.Set("scope", scope)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.send(req)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp, nil
	}
	defer resp.Body.Close()

	// As specified in https://docs.docker.com/registry/spec/auth/token/ section
	// "Token Response Fields", the token is either in `token` or
	// `access_token`. If both present, they are identical.
	var result struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	lr := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(lr).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("%s %q: failed to decode response: %v", resp.Request.Method, resp.Request.URL, err)
	}
	if result.AccessToken != "" {
		return result.AccessToken, nil, nil
	}
	if result.Token == "" {
		return result.Token, nil, nil
	}
	return "", nil, fmt.Errorf("%s %q: empty token returned", resp.Request.Method, resp.Request.URL)
}

// fetchOAuth2Token fetches an OAuth2 access token.
// Reference: https://docs.docker.com/registry/spec/auth/oauth/
func (a *Authorizer) fetchOAuth2Token(ctx context.Context, params map[string]string, token string) (string, *http.Response, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", token)
	form.Set("service", params["service"])
	clientID := a.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}
	form.Set("client_id", clientID)
	if scope, ok := params["scope"]; ok {
		form.Set("scope", scope)
	}
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, params["realm"], body)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.send(req)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp, nil
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
	}
	lr := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(lr).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("%s %q: failed to decode response: %v", resp.Request.Method, resp.Request.URL, err)
	}
	if result.AccessToken != "" {
		return result.AccessToken, nil, nil
	}
	return "", nil, fmt.Errorf("%s %q: empty token returned", resp.Request.Method, resp.Request.URL)
}
