package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	SchemeBasic  = "basic"
	SchemeBearer = "bearer"
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
	Credential func(context.Context, string) (Credential, error)

	Cache Cache

	// ClientID used in fetching OAuth2 token as a required field.
	// If empty, a default client ID is used.
	// Reference: https://docs.docker.com/registry/spec/auth/oauth/#getting-a-token
	ClientID string

	// ForceAttemptOAuth2 controls whether to follow OAuth2 with password grant
	// instead the distribution spec when authenticating using username and
	// password.
	// References:
	// - https://docs.docker.com/registry/spec/auth/jwt/
	// - https://docs.docker.com/registry/spec/auth/oauth/
	ForceAttemptOAuth2 bool
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

// credential resolves the credential for the given registry.
func (a *Authorizer) credential(ctx context.Context, reg string) (Credential, error) {
	if a.Credential == nil {
		return EmptyCredential, nil
	}
	return a.Credential(ctx, reg)
}

func (a *Authorizer) cache() Cache {
	if a.Cache == nil {
		return noCache{}
	}
	return a.Cache
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
	cache := a.cache()
	reg := originalReq.Host
	scheme, err := cache.GetScheme(ctx, reg)
	if err == nil {
		switch scheme {
		case SchemeBasic:
			token, err := cache.GetToken(ctx, reg, "")
			if err == nil {
				req.Header.Set("Authorization", "Basic "+token)
			}
		case SchemeBearer:
			token, err := cache.GetToken(ctx, reg, strings.Join(GetScopes(ctx), " "))
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		}
	}

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
	case SchemeBasic:
		resp.Body.Close()

		token, err := cache.Set(ctx, reg, SchemeBasic, "", func(ctx context.Context) (string, error) {
			return a.fetchBasicAuth(ctx, reg)
		})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, err)
		}

		req = originalReq.Clone(ctx)
		req.Header.Set("Authorization", "Basic "+token)
	case SchemeBearer:
		resp.Body.Close()

		realm := params["realm"]
		service := params["service"]
		scopes := GetScopes(ctx)
		if scope := params["scope"]; scope != "" {
			scopes = append(scopes, strings.Split(scope, " ")...)
		}
		scopes = CleanScopes(scopes)
		key := service + " " + strings.Join(scopes, " ")
		token, err := cache.Set(ctx, reg, SchemeBearer, key, func(ctx context.Context) (string, error) {
			return a.fetchBearerToken(ctx, reg, realm, service, scopes)
		})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", resp.Request.Method, resp.Request.URL, err)
		}

		req = originalReq.Clone(ctx)
		req.Header.Set("Authorization", "Bearer "+token)
	default:
		return resp, nil
	}

	return a.send(req)
}

// fetchBasicAuth fetches a basic auth token for the basic challenge.
func (a *Authorizer) fetchBasicAuth(ctx context.Context, registry string) (string, error) {
	cred, err := a.credential(ctx, registry)
	if err != nil {
		return "", fmt.Errorf("failed to resolve credential: %w", err)
	}
	if cred == EmptyCredential {
		return "", errors.New("credential required for basic auth")
	}
	if cred.Username == "" || cred.Password == "" {
		return "", errors.New("missing username or password for basic auth")
	}
	auth := cred.Username + ":" + cred.Password
	return base64.StdEncoding.EncodeToString([]byte(auth)), nil
}

// fetchBearerToken fetches an access token for the bearer challenge.
func (a *Authorizer) fetchBearerToken(ctx context.Context, registry, realm, service string, scopes []string) (string, error) {
	cred, err := a.credential(ctx, registry)
	if err != nil {
		return "", err
	}
	if cred.AccessToken != "" {
		return cred.AccessToken, nil
	}
	if cred == EmptyCredential || !a.ForceAttemptOAuth2 {
		return a.fetchDistributionToken(ctx, realm, service, scopes, cred.Username, cred.Password)
	}
	return a.fetchOAuth2Token(ctx, realm, service, scopes, cred)
}

// fetchDistributionToken fetches an access token as defined by the distribution
// specification.
// It fetches anonymous tokens if no credential is provided.
// References:
// - https://docs.docker.com/registry/spec/auth/jwt/
// - https://docs.docker.com/registry/spec/auth/token/
func (a *Authorizer) fetchDistributionToken(ctx context.Context, realm, service string, scopes []string, username, password string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return "", err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	q := req.URL.Query()
	if service != "" {
		q.Set("service", service)
	}
	for _, scope := range scopes {
		q.Add("scope", scope)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.send(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s %q: unexpected status code %d: %s", resp.Request.Method, resp.Request.URL, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// As specified in https://docs.docker.com/registry/spec/auth/token/ section
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
	if result.Token == "" {
		return result.Token, nil
	}
	return "", fmt.Errorf("%s %q: empty token returned", resp.Request.Method, resp.Request.URL)
}

// fetchOAuth2Token fetches an OAuth2 access token.
// Reference: https://docs.docker.com/registry/spec/auth/oauth/
func (a *Authorizer) fetchOAuth2Token(ctx context.Context, realm, service string, scopes []string, cred Credential) (string, error) {
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
	form.Set("service", service)
	clientID := a.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}
	form.Set("client_id", clientID)
	if len(scopes) != 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, realm, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.send(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s %q: unexpected status code %d: %s", resp.Request.Method, resp.Request.URL, resp.StatusCode, http.StatusText(resp.StatusCode))
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
