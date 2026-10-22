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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_Do_HTTPSUpgrade(t *testing.T) {
	for _, scheme := range []string{"Basic", "Bearer"} {
		for _, redirect := range []struct {
			name   string
			status int
			method string
			body   string
		}{
			{"GET", http.StatusMovedPermanently, http.MethodGet, ""},
			{"POST to GET", http.StatusSeeOther, http.MethodGet, ""},
			{"POST preserved", http.StatusTemporaryRedirect, http.MethodPost, "payload"},
		} {
			t.Run(scheme+"/"+redirect.name, func(t *testing.T) {
				var httpCalls, authenticatedCalls, tokenCalls int
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if values := r.Header.Values("X-Test"); len(values) != 1 || values[0] != "value" {
						t.Errorf("X-Test values = %q, want [value]", values)
					}
					if r.TLS == nil {
						httpCalls++
						if got := r.Header.Get("Authorization"); got != "" {
							t.Errorf("HTTP request contains Authorization %q", got)
						}
						http.Redirect(w, r, "https://registry.example.com/v2/", redirect.status)
						return
					}
					if r.URL.Path == "/token" {
						tokenCalls++
						fmt.Fprint(w, `{"access_token":"test-token"}`)
						return
					}
					wantAuth := "Basic dXNlcjpwYXNz"
					challenge := `Basic realm="registry"`
					if scheme == "Bearer" {
						wantAuth = "Bearer test-token"
						challenge = `Bearer realm="https://registry.example.com/token",service="registry"`
					}
					if got := r.Header.Get("Authorization"); got != wantAuth {
						if got != "" {
							t.Errorf("HTTPS request contains unexpected Authorization %q", got)
						}
						w.Header().Set("Www-Authenticate", challenge)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					authenticatedCalls++
					if r.Method != redirect.method {
						t.Errorf("authenticated method = %s, want %s", r.Method, redirect.method)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					if string(body) != redirect.body {
						t.Errorf("authenticated body = %q, want %q", body, redirect.body)
					}
				})
				plain := httptest.NewServer(handler)
				defer plain.Close()
				secure := httptest.NewTLSServer(handler)
				defer secure.Close()
				transport := secure.Client().Transport.(*http.Transport).Clone()
				defer transport.CloseIdleConnections()
				transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					switch addr {
					case "registry.example.com:80":
						addr = plain.Listener.Addr().String()
					case "registry.example.com:443":
						addr = secure.Listener.Addr().String()
					default:
						return nil, fmt.Errorf("unexpected address %q", addr)
					}
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				}
				client := &Client{
					Client: &http.Client{Transport: transport},
					Cache:  NewCache(),
					Header: http.Header{"X-Test": {"value"}},
					Credential: func(_ context.Context, hostport string) (Credential, error) {
						if hostport != "registry.example.com" {
							t.Errorf("credential host = %s, want registry.example.com", hostport)
						}
						return Credential{Username: "user", Password: "pass"}, nil
					},
				}
				// Repeat with a shared cache to check that HTTPS Credential never
				// become preemptive authentication on the HTTP endpoint.
				for range 2 {
					method := http.MethodGet
					var body io.Reader
					if redirect.name != "GET" {
						method = http.MethodPost
						body = strings.NewReader("payload")
					}
					req, err := http.NewRequest(method, "http://registry.example.com/v2/", body)
					if err != nil {
						t.Error(err)
						return
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Error(err)
						return
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Errorf("Client.Do() status = %d, want 200", resp.StatusCode)
					}
					if req.Header.Get("Authorization") != "" || req.URL.Scheme != "http" {
						t.Error("Client.Do() changed the caller's request")
					}
				}
				if httpCalls != 2 || authenticatedCalls != 2 {
					t.Errorf("HTTP calls = %d, authenticated HTTPS calls = %d, want 2 each", httpCalls, authenticatedCalls)
				}
				if scheme == "Bearer" && tokenCalls != 2 {
					t.Errorf("token calls = %d, want 2", tokenCalls)
				}
			})
		}
	}
}

type upgradeRoundTripper func(*http.Request) (*http.Response, error)

func (f upgradeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClient_Do_HTTPSUpgradeBoundaries(t *testing.T) {
	const initial = "http://registry.example.com/v2/team/image/manifests/latest"
	const target = "https://registry.example.com/v2/team/image/manifests/latest"
	tests := []struct {
		name     string
		urls     []string
		host     string
		wantAuth bool
	}{
		{name: "standard ports", urls: []string{initial, target}, wantAuth: true},
		{name: "explicit standard ports", urls: []string{
			"http://registry.example.com:80/v2/team/image/manifests/latest",
			"https://registry.example.com:443/v2/team/image/manifests/latest",
		}, wantAuth: true},
		{name: "path redirects before and after upgrade", urls: []string{
			initial, "http://registry.example.com/v2/team/image/manifests/next",
			"https://registry.example.com/v2/team/image/manifests/next", target,
		}, wantAuth: true},
		{name: "different host", urls: []string{initial, "https://other.example.com/v2/team/image/manifests/latest"}},
		{name: "different HTTPS port", urls: []string{initial, "https://registry.example.com:8443/v2/team/image/manifests/latest"}},
		{name: "different HTTP port", urls: []string{"http://registry.example.com:8080/v2/team/image/manifests/latest", target}},
		{name: "different repository", urls: []string{initial, "https://registry.example.com/v2/other/image/manifests/latest"}},
		{name: "host detour", urls: []string{initial, "http://other.example.com/v2/team/image/manifests/latest", target}},
		{name: "port detour", urls: []string{initial, "http://registry.example.com:8080/v2/team/image/manifests/latest", target}},
		{name: "repository detour", urls: []string{initial, "http://registry.example.com/v2/other/image/manifests/latest", target}},
		{name: "HTTPS downgrade", urls: []string{initial, target, "http://registry.example.com/v2/team/image/manifests/next", "https://registry.example.com/v2/team/image/manifests/next"}},
		{name: "userinfo", urls: []string{initial, "https://user:password@registry.example.com/v2/team/image/manifests/latest"}},
		{name: "overridden host", urls: []string{initial, target}, host: "other.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests, credentialsResolved int
			transport := upgradeRoundTripper(func(req *http.Request) (*http.Response, error) {
				status := http.StatusUnauthorized
				header := http.Header{}
				if req.Header.Get("Authorization") == "Basic dXNlcjpwYXNz" {
					if !tt.wantAuth {
						t.Error("unexpected authenticated request")
					}
					if got, want := req.URL.String(), tt.urls[len(tt.urls)-1]; got != want {
						t.Errorf("authenticated URL = %s, want %s", got, want)
					}
					status = http.StatusOK
				} else {
					if requests >= len(tt.urls) {
						return nil, fmt.Errorf("unexpected request to %s", req.URL)
					}
					if got, want := req.URL.String(), tt.urls[requests]; got != want {
						t.Errorf("request URL = %s, want %s", got, want)
					}
					requests++
					if requests < len(tt.urls) {
						status = http.StatusTemporaryRedirect
						header.Set("Location", tt.urls[requests])
					} else {
						header.Set("Www-Authenticate", `Basic realm="registry"`)
					}
				}
				return &http.Response{
					StatusCode: status, Header: header, Request: req,
					Body: io.NopCloser(strings.NewReader("")),
				}, nil
			})
			client := &Client{
				Client: &http.Client{Transport: transport},
				Credential: func(context.Context, string) (Credential, error) {
					credentialsResolved++
					return Credential{Username: "user", Password: "pass"}, nil
				},
			}
			req, err := http.NewRequest(http.MethodGet, tt.urls[0], nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			wantStatus, wantResolved := http.StatusUnauthorized, 0
			if tt.wantAuth {
				wantStatus, wantResolved = http.StatusOK, 1
			}
			if resp.StatusCode != wantStatus || credentialsResolved != wantResolved {
				t.Errorf("status = %d, Credential lookups = %d, want %d and %d", resp.StatusCode, credentialsResolved, wantStatus, wantResolved)
			}
		})
	}
}

func TestClient_Do_HTTPSUpgradeRejectsHTTPRealm(t *testing.T) {
	var requests int
	client := &Client{
		Client: &http.Client{Transport: upgradeRoundTripper(func(req *http.Request) (*http.Response, error) {
			requests++
			header := http.Header{}
			status := http.StatusUnauthorized
			switch requests {
			case 1:
				header.Set("Location", "https://registry.example.com/v2/")
				status = http.StatusTemporaryRedirect
			case 2:
				header.Set("Www-Authenticate", `Bearer realm="http://registry.example.com/token"`)
			default:
				t.Errorf("unexpected request to %s", req.URL)
			}
			return &http.Response{
				StatusCode: status, Header: header, Request: req,
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		})},
		Credential: func(context.Context, string) (Credential, error) {
			t.Error("Credential resolved for an HTTP realm after an HTTPS upgrade")
			return EmptyCredential, nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://registry.example.com/v2/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "registry was contacted over https") {
		t.Errorf("Client.Do() error = %v, want HTTP realm rejection", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
}
