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
	"os"
	"reflect"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
)

const (
	username    = "test_user"
	password    = "test_password"
	accessToken = "test/access/token"
)

var host string
var simpleURL string
var basicAuthURL string
var accessTokenURL string
var clientConfigURL string
var scopes = []string{
	"repository:dst:pull,push",
	"repository:src:pull",
}

func TestMain(m *testing.M) {
	// create an authorization server
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			panic("unexecuted attempt of authorization service")
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			w.WriteHeader(http.StatusUnauthorized)
		}
		if got := r.URL.Query().Get("service"); got != host {
			w.WriteHeader(http.StatusUnauthorized)
		}
		if got := r.URL.Query()["scope"]; !reflect.DeepEqual(got, scopes) {
			w.WriteHeader(http.StatusUnauthorized)
		}
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
		case "/accessToken":
			wantedAuthHeader := "Bearer " + accessToken
			if auth := r.Header.Get("Authorization"); auth != wantedAuthHeader {
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, strings.Join(scopes, " "))
				w.Header().Set("Www-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/clientConfig":
			wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
			authHeader := r.Header.Get("Authorization")
			if authHeader != wantedAuthHeader {
				w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
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
	simpleURL = fmt.Sprintf("%s/simple", host)
	basicAuthURL = fmt.Sprintf("%s/basicAuth", host)
	accessTokenURL = fmt.Sprintf("%s/accessToken", host)
	clientConfigURL = fmt.Sprintf("%s/clientConfig", host)
	http.DefaultClient = ts.Client()

	os.Exit(m.Run())
}

// ExampleClient_Do_minimalClient gives an example of a minimal working client.
func ExampleClient_Do_minimalClient() {
	var client auth.Client
	req, err := http.NewRequest(http.MethodGet, simpleURL, nil)
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
		Credential: func(ctx context.Context, reg string) (auth.Credential, error) {
			return auth.Credential{
				Username: username,
				Password: password,
			}, nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, basicAuthURL, nil)
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
		Credential: func(ctx context.Context, reg string) (auth.Credential, error) {
			return auth.Credential{
				AccessToken: accessToken,
			}, nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, accessTokenURL, nil)
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

// ExampleClient_Do_clientConfiguration shows the client configurations available,
// including using cache, setting user agent and configuring OAuth2.
func ExampleClient_Do_clientConfiguration() {
	client := &auth.Client{
		Credential: func(ctx context.Context, reg string) (auth.Credential, error) {
			return auth.Credential{
				Username: username,
				Password: password,
			}, nil
		},
		ForceAttemptOAuth2: false,
		Cache:              auth.NewCache(),
	}
	client.SetUserAgent("example user agent")
	ctx := auth.WithScopes(context.Background(), scopes...)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientConfigURL, nil)
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
