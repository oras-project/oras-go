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
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClient_SetUserAgent(t *testing.T) {
	wantUserAgent := "test agent"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if userAgent := r.UserAgent(); userAgent != wantUserAgent {
			t.Errorf("unexpected User-Agent: %v, want %v", userAgent, wantUserAgent)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()

	var client Client
	client.SetUserAgent(wantUserAgent)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Basic_Auth(t *testing.T) {
	username := "test_user"
	password := "test_password"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Basic_Auth_Cached(t *testing.T) {
	username := "test_user"
	password := "test_password"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		Cache: NewCache(),
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// repeated request
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Bearer_AccessToken(t *testing.T) {
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexecuted attempt of authorization service")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer as.Close()
	var service string
	scope := "repository:test:pull,push"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, scope)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				AccessToken: accessToken,
			}, nil
		},
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// credential change
	accessToken = "test/access/token/2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Bearer_AccessToken_Cached(t *testing.T) {
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexecuted attempt of authorization service")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer as.Close()
	var service string
	scope := "repository:test:pull,push"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, scope)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				AccessToken: accessToken,
			}, nil
		},
		Cache: NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// repeated request
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}

	// credential change
	accessToken = "test/access/token/2"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Bearer_Auth(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("unexpected auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("unexpected service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query()["scope"]; !reflect.DeepEqual(got, scopes) {
			t.Errorf("unexpected scope: got %s, want %s", got, scopes)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Bearer_Auth_Cached(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("unexpected auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("unexpected service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query()["scope"]; !reflect.DeepEqual(got, scopes) {
			t.Errorf("unexpected scope: got %s, want %s", got, scopes)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		Cache: NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scopes...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// repeated request
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Bearer_OAuth2_Password(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "password" {
			t.Errorf("unexpected grant type: %v, want %v", got, "password")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username {
			t.Errorf("unexpected username: %v, want %v", got, username)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password {
			t.Errorf("unexpected password: %v, want %v", got, password)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		ForceAttemptOAuth2: true,
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Bearer_OAuth2_Password_Cached(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "password" {
			t.Errorf("unexpected grant type: %v, want %v", got, "password")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username {
			t.Errorf("unexpected username: %v, want %v", got, username)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password {
			t.Errorf("unexpected password: %v, want %v", got, password)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scopes...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// repeated request
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	username = "test_user2"
	password = "test_password2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Bearer_OAuth2_RefreshToken(t *testing.T) {
	refreshToken := "test/refresh/token"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("unexpected grant type: %v, want %v", got, "refresh_token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				RefreshToken: refreshToken,
			}, nil
		},
	}

	// first request
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	refreshToken = "test/refresh/token/2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Bearer_OAuth2_RefreshToken_Cached(t *testing.T) {
	refreshToken := "test/refresh/token"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("unexpected grant type: %v, want %v", got, "refresh_token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				RefreshToken: refreshToken,
			}, nil
		},
		Cache: NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scopes...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// repeated request
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// credential change
	refreshToken = "test/refresh/token/2"
	accessToken = "test/access/token/2"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Token_Expire(t *testing.T) {
	refreshToken := "test/refresh/token"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("unexpected grant type: %v, want %v", got, "refresh_token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				RefreshToken: refreshToken,
			}, nil
		},
		Cache: NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scopes...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// invalidate the access token and request again
	accessToken = "test/access/token/2"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Scope_Hint_Mismatch(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	scope := "repository:test:delete"
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "password" {
			t.Errorf("unexpected grant type: %v, want %v", got, "password")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("service"); got != service {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scopes := CleanScopes(append([]string{scope}, scopes...))
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username {
			t.Errorf("unexpected username: %v, want %v", got, username)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password {
			t.Errorf("unexpected password: %v, want %v", got, password)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, scope)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}

	// first request
	ctx := WithScopes(context.Background(), scopes...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// repeated request
	// although the actual scope does not match the hinted scopes, the client
	// with cache cannot avoid a request to obtain a challenge but can prevent
	// a repeated call to the authorization server.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Invalid_Credential_Basic(t *testing.T) {
	username := "test_user"
	password := "test_password"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
		t.Error("authentication should fail but succeeded")
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: "bad credential",
			}, nil
		},
	}

	// request should fail
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusUnauthorized)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
}

func TestClient_Do_Invalid_Credential_Bearer(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scopes := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			atomic.AddInt64(&authCount, 1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		t.Error("authentication should fail but succeeded")
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, strings.Join(scopes, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
		t.Error("authentication should fail but succeeded")
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: "bad credential",
			}, nil
		},
	}

	// request should fail
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatalf("Client.Do() error = %v, wantErr %v", err, true)
	}
	if wantRequestCount++; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Anonymous_Pull(t *testing.T) {
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scope := "repository:test:pull"
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected auth: got %s, want %s", auth, "")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("unexpected service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("scope"); got != scope {
			t.Errorf("unexpected scope: got %s, want %s", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, scope)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	// request with the default client
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}

func TestClient_Do_Scheme_Change(t *testing.T) {
	username := "test_user"
	password := "test_password"
	accessToken := "test/access/token"
	var requestCount, wantRequestCount int64
	var successCount, wantSuccessCount int64
	var authCount, wantAuthCount int64
	var service string
	scope := "repository:test:pull"
	challengeBearerAuth := true
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("unexpected auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("unexpected service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("scope"); got != scope {
			t.Errorf("unexpected scope: got %s, want %s", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		bearerHeader := "Bearer " + accessToken
		basicHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		header := r.Header.Get("Authorization")
		if (challengeBearerAuth && header != bearerHeader) || (!challengeBearerAuth && header != basicHeader) {
			var challenge string
			if challengeBearerAuth {
				challenge = fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service, scope)
			} else {
				challenge = `Basic realm="Test Server"`
			}
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount, 1)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service = uri.Host

	client := &Client{
		Credential: func(ctx context.Context, reg string) (Credential, error) {
			if reg != uri.Host {
				err := fmt.Errorf("registry mismatch: got %v, want %v", reg, uri.Host)
				t.Error(err)
				return EmptyCredential, err
			}
			return Credential{
				Username: username,
				Password: password,
			}, nil
		},
		Cache: NewCache(),
	}

	// request with bearer auth
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if wantAuthCount++; authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}

	// change to basic auth
	challengeBearerAuth = false
	req, err = http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if wantRequestCount += 2; requestCount != wantRequestCount {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount, wantRequestCount)
	}
	if wantSuccessCount++; successCount != wantSuccessCount {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount, wantSuccessCount)
	}
	if authCount != wantAuthCount {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount, wantAuthCount)
	}
}
