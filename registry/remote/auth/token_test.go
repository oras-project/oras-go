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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

func TestDistributionTokenFetcher_FetchToken(t *testing.T) {
	wantToken := "test_token"
	wantService := "test_service"
	wantScope := "repository:test:pull"
	username := "test_user"
	password := "test_password"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s, want GET", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Verify query parameters
		if got := r.URL.Query().Get("service"); got != wantService {
			t.Errorf("unexpected service: %s, want %s", got, wantService)
		}
		if got := r.URL.Query().Get("scope"); got != wantScope {
			t.Errorf("unexpected scope: %s, want %s", got, wantScope)
		}
		// Verify basic auth
		u, p, ok := r.BasicAuth()
		if !ok {
			t.Error("expected basic auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if u != username || p != password {
			t.Errorf("unexpected credentials: %s:%s, want %s:%s", u, p, username, password)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Return token response
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Registry: "test-registry",
		Realm:    ts.URL,
		Service:  wantService,
		Scopes:   []string{wantScope},
	}
	cred := credentials.Credential{
		Username: username,
		Password: password,
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestDistributionTokenFetcher_FetchToken_Anonymous(t *testing.T) {
	wantToken := "anonymous_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s, want GET", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Verify no auth header
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected Authorization header: %s", auth)
		}

		// Return token response using 'token' field
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Registry: "test-registry",
		Realm:    ts.URL,
		Service:  "test_service",
		Scopes:   []string{"repository:test:pull"},
	}

	token, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestDistributionTokenFetcher_FetchToken_EmptyToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm: ts.URL,
	}

	_, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err == nil {
		t.Error("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "empty token returned") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDistributionTokenFetcher_FetchToken_CustomHeaders(t *testing.T) {
	wantToken := "test_token"
	wantHeader := "test-value"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Custom-Header"); got != wantHeader {
			t.Errorf("unexpected custom header: %s, want %s", got, wantHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
		Header: http.Header{
			"X-Custom-Header": {wantHeader},
		},
	}

	params := TokenParams{
		Realm: ts.URL,
	}

	token, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_Password(t *testing.T) {
	wantToken := "oauth2_token"
	wantService := "test_service"
	wantScope := "repository:test:pull"
	wantClientID := "test_client"
	username := "test_user"
	password := "test_password"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s, want POST", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected Content-Type: %s", ct)
		}

		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "password" {
			t.Errorf("unexpected grant_type: %s, want password", got)
		}
		if got := r.Form.Get("username"); got != username {
			t.Errorf("unexpected username: %s, want %s", got, username)
		}
		if got := r.Form.Get("password"); got != password {
			t.Errorf("unexpected password: %s, want %s", got, password)
		}
		if got := r.Form.Get("service"); got != wantService {
			t.Errorf("unexpected service: %s, want %s", got, wantService)
		}
		if got := r.Form.Get("scope"); got != wantScope {
			t.Errorf("unexpected scope: %s, want %s", got, wantScope)
		}
		if got := r.Form.Get("client_id"); got != wantClientID {
			t.Errorf("unexpected client_id: %s, want %s", got, wantClientID)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client:   ts.Client(),
		ClientID: wantClientID,
	}

	params := TokenParams{
		Registry: "test-registry",
		Realm:    ts.URL,
		Service:  wantService,
		Scopes:   []string{wantScope},
	}
	cred := credentials.Credential{
		Username: username,
		Password: password,
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_RefreshToken(t *testing.T) {
	wantToken := "oauth2_token"
	wantRefreshToken := "test_refresh_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s, want POST", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("unexpected grant_type: %s, want refresh_token", got)
		}
		if got := r.Form.Get("refresh_token"); got != wantRefreshToken {
			t.Errorf("unexpected refresh_token: %s, want %s", got, wantRefreshToken)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Registry: "test-registry",
		Realm:    ts.URL,
		Service:  "test_service",
	}
	cred := credentials.Credential{
		RefreshToken: wantRefreshToken,
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_DefaultClientID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		if got := r.Form.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client_id: %s, want %s", got, defaultClientID)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": "token"}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
		// No ClientID set, should use default
	}

	params := TokenParams{
		Realm: ts.URL,
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	_, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
}

func TestOAuth2TokenFetcher_FetchToken_NoCredentials(t *testing.T) {
	fetcher := &OAuth2TokenFetcher{}

	params := TokenParams{
		Realm: "http://example.com/token",
	}

	_, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err == nil {
		t.Error("expected error for empty credentials")
	}
	if !strings.Contains(err.Error(), "missing username or password") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2TokenFetcher_FetchToken_EmptyToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm: ts.URL,
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	_, err := fetcher.FetchToken(context.Background(), params, cred)
	if err == nil {
		t.Error("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "empty token returned") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompositeTokenFetcher_FetchToken_AccessToken(t *testing.T) {
	wantToken := "direct_access_token"

	fetcher := &CompositeTokenFetcher{
		Distribution: &mockTokenFetcher{token: "distribution_token"},
		OAuth2:       &mockTokenFetcher{token: "oauth2_token"},
	}

	params := TokenParams{
		Registry: "test-registry",
	}
	cred := credentials.Credential{
		AccessToken: wantToken,
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestCompositeTokenFetcher_FetchToken_EmptyCredential(t *testing.T) {
	wantToken := "distribution_token"

	fetcher := &CompositeTokenFetcher{
		Distribution: &mockTokenFetcher{token: wantToken},
		OAuth2:       &mockTokenFetcher{token: "oauth2_token"},
	}

	params := TokenParams{
		Registry: "test-registry",
	}

	token, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s (should use distribution fetcher)", token, wantToken)
	}
}

func TestCompositeTokenFetcher_FetchToken_LegacyMode(t *testing.T) {
	wantToken := "distribution_token"

	fetcher := &CompositeTokenFetcher{
		Distribution: &mockTokenFetcher{token: wantToken},
		OAuth2:       &mockTokenFetcher{token: "oauth2_token"},
		LegacyMode:   true,
	}

	params := TokenParams{
		Registry: "test-registry",
	}
	// Credential with username/password but no refresh token
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s (should use distribution fetcher in legacy mode)", token, wantToken)
	}
}

func TestCompositeTokenFetcher_FetchToken_OAuth2(t *testing.T) {
	wantToken := "oauth2_token"

	fetcher := &CompositeTokenFetcher{
		Distribution: &mockTokenFetcher{token: "distribution_token"},
		OAuth2:       &mockTokenFetcher{token: wantToken},
		LegacyMode:   false, // Not legacy mode
	}

	params := TokenParams{
		Registry: "test-registry",
	}
	// Credential with username/password and no refresh token
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s (should use OAuth2 fetcher when not in legacy mode)", token, wantToken)
	}
}

func TestCompositeTokenFetcher_FetchToken_RefreshToken(t *testing.T) {
	wantToken := "oauth2_token"

	fetcher := &CompositeTokenFetcher{
		Distribution: &mockTokenFetcher{token: "distribution_token"},
		OAuth2:       &mockTokenFetcher{token: wantToken},
		LegacyMode:   true, // Even in legacy mode, refresh token uses OAuth2
	}

	params := TokenParams{
		Registry: "test-registry",
	}
	cred := credentials.Credential{
		RefreshToken: "refresh_token",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s (should use OAuth2 fetcher for refresh token)", token, wantToken)
	}
}

func TestNewCompositeTokenFetcher(t *testing.T) {
	client := &http.Client{}
	header := http.Header{"X-Test": {"value"}}
	clientID := "test-client"

	fetcher := NewCompositeTokenFetcher(client, header, clientID, true)

	if fetcher.LegacyMode != true {
		t.Error("LegacyMode should be true")
	}

	distFetcher, ok := fetcher.Distribution.(*DistributionTokenFetcher)
	if !ok {
		t.Fatal("Distribution should be *DistributionTokenFetcher")
	}
	if distFetcher.Client != client {
		t.Error("Distribution fetcher client mismatch")
	}
	if distFetcher.Header.Get("X-Test") != "value" {
		t.Error("Distribution fetcher header mismatch")
	}

	oauth2Fetcher, ok := fetcher.OAuth2.(*OAuth2TokenFetcher)
	if !ok {
		t.Fatal("OAuth2 should be *OAuth2TokenFetcher")
	}
	if oauth2Fetcher.Client != client {
		t.Error("OAuth2 fetcher client mismatch")
	}
	if oauth2Fetcher.Header.Get("X-Test") != "value" {
		t.Error("OAuth2 fetcher header mismatch")
	}
	if oauth2Fetcher.ClientID != clientID {
		t.Errorf("OAuth2 fetcher ClientID = %s, want %s", oauth2Fetcher.ClientID, clientID)
	}
}

// mockTokenFetcher is a test helper that returns a fixed token.
type mockTokenFetcher struct {
	token string
	err   error
}

func (m *mockTokenFetcher) FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.token, nil
}

func TestDistributionTokenFetcher_FetchToken_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errors":[{"code":"UNKNOWN","message":"server error"}]}`))
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm: ts.URL,
	}

	_, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err == nil {
		t.Error("expected error for server error response")
	}
}

func TestDistributionTokenFetcher_FetchToken_NilClient(t *testing.T) {
	wantToken := "nil_client_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	// No custom Client set, should use http.DefaultClient.
	fetcher := &DistributionTokenFetcher{}

	params := TokenParams{
		Realm: ts.URL,
	}

	token, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errors":[{"code":"UNKNOWN","message":"server error"}]}`))
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm: ts.URL,
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	_, err := fetcher.FetchToken(context.Background(), params, cred)
	if err == nil {
		t.Error("expected error for server error response")
	}
}

func TestOAuth2TokenFetcher_FetchToken_NilClient(t *testing.T) {
	wantToken := "nil_client_oauth2_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	// No custom Client set, should use http.DefaultClient.
	fetcher := &OAuth2TokenFetcher{}

	params := TokenParams{
		Realm: ts.URL,
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_CustomHeaders(t *testing.T) {
	wantToken := "header_token"
	wantHeader := "custom-value"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Custom"); got != wantHeader {
			t.Errorf("custom header = %q, want %q", got, wantHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
		Header: http.Header{
			"X-Custom": {wantHeader},
		},
	}

	params := TokenParams{
		Realm: ts.URL,
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestOAuth2TokenFetcher_FetchToken_NoScopes(t *testing.T) {
	wantToken := "no_scope_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}
		// Scope should not be set when no scopes provided.
		if got := r.Form.Get("scope"); got != "" {
			t.Errorf("scope = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &OAuth2TokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm:  ts.URL,
		Scopes: nil, // No scopes
	}
	cred := credentials.Credential{
		Username: "user",
		Password: "pass",
	}

	token, err := fetcher.FetchToken(context.Background(), params, cred)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

func TestDistributionTokenFetcher_FetchToken_MultipleScopes(t *testing.T) {
	wantToken := "multi_scope_token"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopes := r.URL.Query()["scope"]
		if len(scopes) != 2 {
			t.Errorf("expected 2 scope params, got %d", len(scopes))
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"access_token": wantToken}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer ts.Close()

	fetcher := &DistributionTokenFetcher{
		Client: ts.Client(),
	}

	params := TokenParams{
		Realm:  ts.URL,
		Scopes: []string{"repository:test:pull", "repository:test:push"},
	}

	token, err := fetcher.FetchToken(context.Background(), params, credentials.EmptyCredential)
	if err != nil {
		t.Fatalf("FetchToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("FetchToken() = %s, want %s", token, wantToken)
	}
}

// Verify url is imported but potentially unused warning fix
var _ = url.Parse
