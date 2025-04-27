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

// Package auth_test includes the testable examples for the http client.
package auth_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	. "oras.land/oras-go/v2/registry/internal/doc"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const (
	username     = "test_user"
	password     = "test_password"
	accessToken  = "test/access/token"
	refreshToken = "test/refresh/token"
	_            = ExampleUnplayable
)

var (
	host                  string
	expectedHostAddress   string
	targetURL             string
	clientConfigTargetURL string
	basicAuthTargetURL    string
	accessTokenTargetURL  string
	refreshTokenTargetURL string
	tokenScopes           = []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
)

func TestMain(m *testing.M) {
	// create an authorization server
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusUnauthorized)
			panic("unexecuted attempt of authorization service")
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			panic("failed to parse form")
		}
		if got := r.PostForm.Get("service"); got != host {
			w.WriteHeader(http.StatusUnauthorized)
		}
		// handles refresh token requests
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			w.WriteHeader(http.StatusUnauthorized)
		}
		scope := strings.Join(tokenScopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			w.WriteHeader(http.StatusUnauthorized)
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken {
			w.WriteHeader(http.StatusUnauthorized)
		}
		// writes back access token
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			panic(err)
		}
	}))
	defer as.Close()

	// create a test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			panic("unexpected access")
		}
		switch path {
		case "/basicAuth":
			wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
			authHeader := r.Header.Get("Authorization")
			if authHeader != wantedAuthHeader {
				w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/clientConfig":
			wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
			authHeader := r.Header.Get("Authorization")
			if authHeader != wantedAuthHeader {
				w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/accessToken":
			wantedAuthHeader := "Bearer " + accessToken
			if auth := r.Header.Get("Authorization"); auth != wantedAuthHeader {
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, strings.Join(tokenScopes, " "))
				w.Header().Set("Www-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/refreshToken":
			wantedAuthHeader := "Bearer " + accessToken
			if auth := r.Header.Get("Authorization"); auth != wantedAuthHeader {
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, strings.Join(tokenScopes, " "))
				w.Header().Set("Www-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/simple":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotAcceptable)
		}
	}))
	defer ts.Close()
	host = ts.URL
	uri, _ := url.Parse(host)
	expectedHostAddress = uri.Host
	targetURL = fmt.Sprintf("%s/simple", host)
	basicAuthTargetURL = fmt.Sprintf("%s/basicAuth", host)
	clientConfigTargetURL = fmt.Sprintf("%s/clientConfig", host)
	accessTokenTargetURL = fmt.Sprintf("%s/accessToken", host)
	refreshTokenTargetURL = fmt.Sprintf("%s/refreshToken", host)
	http.DefaultClient = ts.Client()

	os.Exit(m.Run())
}

// ExampleClient_Do_minimalClient gives an example of a minimal working client.
func ExampleClient_Do_minimalClient() {
	var client auth.Client
	// targetURL can be any URL. For example, https://registry.wabbit-networks.io/v2/
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleClient_Do_basicAuth gives an example of using client with credentials.
func ExampleClient_Do_basicAuth() {
	client := &auth.Client{
		// expectedHostAddress is of form ipaddr:port
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			Username: username,
			Password: password,
		}),
	}
	// basicAuthTargetURL can be any URL. For example, https://registry.wabbit-networks.io/v2/
	req, err := http.NewRequest(http.MethodGet, basicAuthTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleClient_Do_clientConfigurations shows the client configurations available,
// including using cache, setting user agent and configuring OAuth2.
func ExampleClient_Do_clientConfigurations() {
	client := &auth.Client{
		// expectedHostAddress is of form ipaddr:port
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			Username: username,
			Password: password,
		}),
		// ForceAttemptOAuth2 controls whether to follow OAuth2 with password grant.
		ForceAttemptOAuth2: true,
		// Cache caches credentials for accessing the remote registry.
		Cache: auth.NewCache(),
	}
	// SetUserAgent sets the user agent for all out-going requests.
	client.SetUserAgent("example user agent")
	// Tokens carry restrictions about what resources they can access and how.
	// Such restrictions are represented and enforced as Scopes.
	// Reference: https://distribution.github.io/distribution/spec/auth/scope/
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	// WithScopes returns a context with scopes added.
	ctx := auth.WithScopes(context.Background(), scopes...)

	// clientConfigTargetURL can be any URL. For example, https://registry.wabbit-networks.io/v2/
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientConfigTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleClient_Do_withAccessToken gives an example of using client with an access token.
func ExampleClient_Do_withAccessToken() {
	client := &auth.Client{
		// expectedHostAddress is of form ipaddr:port
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			AccessToken: accessToken,
		}),
	}
	// accessTokenTargetURL can be any URL. For example, https://registry.wabbit-networks.io/v2/
	req, err := http.NewRequest(http.MethodGet, accessTokenTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleClient_Do_withRefreshToken gives an example of using client with a refresh token.
func ExampleClient_Do_withRefreshToken() {
	client := &auth.Client{
		// expectedHostAddress is of form ipaddr:port
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			RefreshToken: refreshToken,
		}),
	}

	// refreshTokenTargetURL can be any URL. For example, https://registry.wabbit-networks.io/v2/
	req, err := http.NewRequest(http.MethodGet, refreshTokenTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}
