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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

const (
	fallbackUsername = "user"
	fallbackPassword = "pass"
	fallbackToken    = "distribution-token"
)

// tokenServer is a token endpoint that answers the OAuth2 POST with postStatus
// and serves the distribution GET for fallbackUsername and fallbackPassword.
type tokenServer struct {
	*httptest.Server
	posts atomic.Int64
	gets  atomic.Int64
}

func newTokenServer(t *testing.T, postStatus int) *tokenServer {
	t.Helper()
	s := &tokenServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.posts.Add(1)
			if postStatus == http.StatusOK {
				w.Write([]byte(`{"access_token":"oauth2-token"}`))
				return
			}
			w.WriteHeader(postStatus)
			w.Write([]byte(`{"errors":[{"code":"UNSUPPORTED","message":"not supported"}]}`))
		case http.MethodGet:
			s.gets.Add(1)
			if user, pass, ok := r.BasicAuth(); !ok || user != fallbackUsername || pass != fallbackPassword {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"bad credential"}]}`))
				return
			}
			w.Write([]byte(`{"token":"` + fallbackToken + `"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func fallbackParams(realm string, scope string) TokenParams {
	parsed, err := ParseScope(scope)
	if err != nil {
		panic(err)
	}
	return TokenParams{
		Resource: properties.Resource{Registry: "registry.example.com"},
		Realm:    realm,
		Service:  "registry.example.com",
		Scopes:   []Scope{parsed},
	}
}

var fallbackCredential = credentials.Credential{Username: fallbackUsername, Password: fallbackPassword}

func TestCompositeTokenFetcher_FetchToken_FallsBackWhenOAuth2Unsupported(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ts := newTokenServer(t, status)
			fetcher := NewCompositeTokenFetcher(ts.Client(), nil, "", false)

			token, err := fetcher.FetchToken(context.Background(), fallbackParams(ts.URL, "repository:test:pull"), fallbackCredential)
			if err != nil {
				t.Fatalf("FetchToken() error = %v", err)
			}
			if token != fallbackToken {
				t.Errorf("FetchToken() = %q, want %q", token, fallbackToken)
			}
			if got := ts.posts.Load(); got != 1 {
				t.Errorf("OAuth2 POST count = %d, want 1", got)
			}
			if got := ts.gets.Load(); got != 1 {
				t.Errorf("distribution GET count = %d, want 1", got)
			}
		})
	}
}

func TestCompositeTokenFetcher_FetchToken_NoFallback(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		cred       credentials.Credential
		strict     bool
		wantStatus int
	}{
		{name: "403 forbidden", status: http.StatusForbidden, cred: fallbackCredential, wantStatus: http.StatusForbidden},
		{name: "429 too many requests", status: http.StatusTooManyRequests, cred: fallbackCredential, wantStatus: http.StatusTooManyRequests},
		{name: "500 server error", status: http.StatusInternalServerError, cred: fallbackCredential, wantStatus: http.StatusInternalServerError},
		{
			name:       "refresh token credential",
			status:     http.StatusNotFound,
			cred:       credentials.Credential{Username: fallbackUsername, Password: fallbackPassword, RefreshToken: "refresh"},
			wantStatus: http.StatusNotFound,
		},
		{name: "strict OAuth2", status: http.StatusNotFound, cred: fallbackCredential, strict: true, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTokenServer(t, tt.status)
			fetcher := NewCompositeTokenFetcher(ts.Client(), nil, "", false)
			fetcher.StrictOAuth2 = tt.strict

			_, err := fetcher.FetchToken(context.Background(), fallbackParams(ts.URL, "repository:test:pull"), tt.cred)
			var errResp *errcode.ErrorResponse
			if !errors.As(err, &errResp) || errResp.StatusCode != tt.wantStatus {
				t.Fatalf("FetchToken() error = %v, want the OAuth2 response with status %d", err, tt.wantStatus)
			}
			if got := ts.gets.Load(); got != 0 {
				t.Errorf("distribution GET count = %d, want 0", got)
			}
		})
	}
}

func TestCompositeTokenFetcher_FetchToken_NoFallbackOnOtherErrors(t *testing.T) {
	wantErr := errors.New("connection refused")
	var distributionCalls int
	fetcher := &CompositeTokenFetcher{
		Distribution: tokenFetcherFunc(func() (string, error) {
			distributionCalls++
			return "distribution", nil
		}),
		OAuth2: &mockTokenFetcher{err: wantErr},
	}

	_, err := fetcher.FetchToken(context.Background(), fallbackParams("https://auth.example.com/token", "repository:test:pull"), fallbackCredential)
	if !errors.Is(err, wantErr) {
		t.Errorf("FetchToken() error = %v, want %v", err, wantErr)
	}
	if distributionCalls != 0 {
		t.Errorf("distribution fetcher called %d times, want 0", distributionCalls)
	}
}

type tokenFetcherFunc func() (string, error)

func (f tokenFetcherFunc) FetchToken(context.Context, TokenParams, credentials.Credential) (string, error) {
	return f()
}

func TestCompositeTokenFetcher_FetchToken_RemembersDistributionRealm(t *testing.T) {
	ts := newTokenServer(t, http.StatusNotFound)
	fetcher := NewCompositeTokenFetcher(ts.Client(), nil, "", false)

	for _, scope := range []string{"repository:test:pull", "repository:other:pull,push"} {
		token, err := fetcher.FetchToken(context.Background(), fallbackParams(ts.URL, scope), fallbackCredential)
		if err != nil {
			t.Fatalf("FetchToken(%q) error = %v", scope, err)
		}
		if token != fallbackToken {
			t.Errorf("FetchToken(%q) = %q, want %q", scope, token, fallbackToken)
		}
	}
	if got := ts.posts.Load(); got != 1 {
		t.Errorf("OAuth2 POST count = %d, want 1: the second fetch should go straight to GET", got)
	}
	if got := ts.gets.Load(); got != 2 {
		t.Errorf("distribution GET count = %d, want 2", got)
	}

	// A realm that was not seen to fall back still gets the OAuth2 attempt.
	other := newTokenServer(t, http.StatusOK)
	token, err := fetcher.FetchToken(context.Background(), fallbackParams(other.URL, "repository:test:pull"), fallbackCredential)
	if err != nil {
		t.Fatalf("FetchToken() on another realm error = %v", err)
	}
	if token != "oauth2-token" || other.posts.Load() != 1 || other.gets.Load() != 0 {
		t.Errorf("another realm: token = %q, POSTs = %d, GETs = %d; want an OAuth2 token from one POST", token, other.posts.Load(), other.gets.Load())
	}
}

func TestCompositeTokenFetcher_FetchToken_NoMemoryWithoutRealmSet(t *testing.T) {
	ts := newTokenServer(t, http.StatusNotFound)
	base := NewCompositeTokenFetcher(ts.Client(), nil, "", false)
	// A fetcher assembled by hand has nowhere to remember a realm.
	fetcher := &CompositeTokenFetcher{Distribution: base.Distribution, OAuth2: base.OAuth2}

	for range 2 {
		if _, err := fetcher.FetchToken(context.Background(), fallbackParams(ts.URL, "repository:test:pull"), fallbackCredential); err != nil {
			t.Fatalf("FetchToken() error = %v", err)
		}
	}
	if got := ts.posts.Load(); got != 2 {
		t.Errorf("OAuth2 POST count = %d, want 2", got)
	}
}

func TestCompositeTokenFetcher_FetchToken_DoesNotRememberFailedFallback(t *testing.T) {
	ts := newTokenServer(t, http.StatusNotFound)
	fetcher := NewCompositeTokenFetcher(ts.Client(), nil, "", false)
	badCredential := credentials.Credential{Username: fallbackUsername, Password: "wrong"}

	for range 2 {
		_, err := fetcher.FetchToken(context.Background(), fallbackParams(ts.URL, "repository:test:pull"), badCredential)
		var errResp *errcode.ErrorResponse
		if !errors.As(err, &errResp) || errResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("FetchToken() error = %v, want the 401 from the distribution GET", err)
		}
	}
	if got := ts.posts.Load(); got != 2 {
		t.Errorf("OAuth2 POST count = %d, want 2: a failed fallback must not be remembered", got)
	}
}

func TestClient_Do_Bearer_DefaultFetcherFallsBackToDistribution(t *testing.T) {
	as := newTokenServer(t, http.StatusNotFound)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+fallbackToken {
			return
		}
		w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm=%q,service="registry.example.com",scope="repository:test:pull"`, as.URL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer registry.Close()

	newClient := func() *Client {
		return &Client{
			CredentialFunc: func(context.Context, properties.Resource) (credentials.Credential, error) {
				return fallbackCredential, nil
			},
		}
	}
	do := func(client *Client) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, registry.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Client.Do() error = %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Client.Do() status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	}

	// The client falls back once and then goes straight to GET.
	client := newClient()
	do(client)
	do(client)
	if posts, gets := as.posts.Load(), as.gets.Load(); posts != 1 || gets != 2 {
		t.Errorf("token server saw %d POSTs and %d GETs, want 1 and 2", posts, gets)
	}

	// A clone starts without that knowledge.
	do(client.Clone())
	if posts := as.posts.Load(); posts != 2 {
		t.Errorf("token server saw %d POSTs after cloning, want 2", posts)
	}
}
