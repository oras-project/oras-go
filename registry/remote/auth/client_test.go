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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"oras.land/oras-go/v2/registry/remote/errcode"
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

func TestClient_Do_Bearer_AccessToken_Cached_PerHost(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexecuted attempt of authorization service")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer as.Close()
	// set up server 1
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var service1 string
	scope1 := "repository:test:pull"
	accessToken1 := "test/access/token/1"
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service1, scope1)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			AccessToken: accessToken1,
		}),
		Cache: NewCache(),
	}

	// set up server 2
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var service2 string
	scope2 := "repository:test:pull,push"
	accessToken2 := "test/access/token/2"
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, service2, scope2)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts2.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			AccessToken: accessToken2,
		}),
		Cache: NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scope1)
	ctx = WithScopesForHost(ctx, uri2.Host, scope2)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	// first request to server 2
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount1 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}

	// repeated request to server 1
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1++; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	// repeated request to server 2
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2++; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}

	// credential change for server 1
	accessToken1 = "test/access/token/1/new"
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client1.Credential = StaticCredential(uri1.Host, Credential{
		AccessToken: accessToken1,
	})
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	// credential change for server 2
	accessToken2 = "test/access/token/2/new"
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client2.Credential = StaticCredential(uri2.Host, Credential{
		AccessToken: accessToken2,
	})
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
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

func TestClient_Do_Bearer_Auth_Cached_PerHost(t *testing.T) {
	// set up server 1
	username1 := "test_user1"
	password1 := "test_password1"
	accessToken1 := "test/access/token/1"
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var authCount1, wantAuthCount1 int64
	var service1 string
	scopes1 := []string{
		"repository:src:pull",
	}
	as1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username1+":"+password1))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("unexpected auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service1 {
			t.Errorf("unexpected service: got %s, want %s", got, service1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query()["scope"]; !reflect.DeepEqual(got, scopes1) {
			t.Errorf("unexpected scope: got %s, want %s", got, scopes1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount1, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken1); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as1.URL, service1, strings.Join(scopes1, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			Username: username1,
			Password: password1,
		}),
		Cache: NewCache(),
	}

	// set up server 2
	username2 := "test_user2"
	password2 := "test_password2"
	accessToken2 := "test/access/token/1"
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var authCount2, wantAuthCount2 int64
	var service2 string
	scopes2 := []string{
		"repository:dst:pull,push",
	}
	as2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username2+":"+password2))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("unexpected auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service2 {
			t.Errorf("unexpected service: got %s, want %s", got, service2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query()["scope"]; !reflect.DeepEqual(got, scopes2) {
			t.Errorf("unexpected scope: got %s, want %s", got, scopes2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount2, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken2); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as2.URL, service2, strings.Join(scopes2, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts2.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			Username: username2,
			Password: password2,
		}),
		Cache: NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scopes1...)
	ctx = WithScopesForHost(ctx, uri2.Host, scopes2...)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// first request to server 2
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// repeated request to server 1
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1++; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// repeated request to server 2
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2++; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// credential change for server 1
	username1 = "test_user1_new"
	password1 = "test_password1_new"
	accessToken1 = "test/access/token/1/new"
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client1.Credential = StaticCredential(uri1.Host, Credential{
		Username: username1,
		Password: password1,
	})
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// credential change for server 2
	username2 = "test_user2_new"
	password2 = "test_password2_new"
	accessToken2 = "test/access/token/2/new"
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client2.Credential = StaticCredential(uri2.Host, Credential{
		Username: username2,
		Password: password2,
	})
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
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

func TestClient_Do_Bearer_OAuth2_Password_Cached_PerHost(t *testing.T) {
	// set up server 1
	username1 := "test_user1"
	password1 := "test_password1"
	accessToken1 := "test/access/token/1"
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var authCount1, wantAuthCount1 int64
	var service1 string
	scopes1 := []string{
		"repository:src:pull",
	}
	as1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service1 {
			t.Errorf("unexpected service: %v, want %v", got, service1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes1, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username1 {
			t.Errorf("unexpected username: %v, want %v", got, username1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password1 {
			t.Errorf("unexpected password: %v, want %v", got, password1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount1, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken1); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as1.URL, service1, strings.Join(scopes1, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			Username: username1,
			Password: password1,
		}),
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}
	// set up server 2
	username2 := "test_user2"
	password2 := "test_password2"
	accessToken2 := "test/access/token/2"
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var authCount2, wantAuthCount2 int64
	var service2 string
	scopes2 := []string{
		"repository:dst:pull,push",
	}
	as2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service2 {
			t.Errorf("unexpected service: %v, want %v", got, service2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes2, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username2 {
			t.Errorf("unexpected username: %v, want %v", got, username2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password2 {
			t.Errorf("unexpected password: %v, want %v", got, password2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount2, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken2); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as2.Close()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as2.URL, service2, strings.Join(scopes2, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts2.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			Username: username2,
			Password: password2,
		}),
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scopes1...)
	ctx = WithScopesForHost(ctx, uri2.Host, scopes2...)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// first request to server 2
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// repeated request to server 1
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1++; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// repeated request to server 2
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2++; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// credential change for server 1
	username1 = "test_user1_new"
	password1 = "test_password1_new"
	accessToken1 = "test/access/token/1/new"
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client1.Credential = StaticCredential(uri1.Host, Credential{
		Username: username1,
		Password: password1,
	})
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// credential change for server 2
	username2 = "test_user2_new"
	password2 = "test_password2_new"
	accessToken2 = "test/access/token/2/new"
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client2.Credential = StaticCredential(uri2.Host, Credential{
		Username: username2,
		Password: password2,
	})
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
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

func TestClient_Do_Bearer_OAuth2_RefreshToken_Cached_PerHost(t *testing.T) {
	// set up server 1
	refreshToken1 := "test/refresh/token/1"
	accessToken1 := "test/access/token/1"
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var authCount1, wantAuthCount1 int64
	var service1 string
	scopes1 := []string{
		"repository:src:pull",
	}
	as1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service1 {
			t.Errorf("unexpected service: %v, want %v", got, service1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes1, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken1 {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount1, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken1); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as1.URL, service1, strings.Join(scopes1, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			RefreshToken: refreshToken1,
		}),
		Cache: NewCache(),
	}

	// set up server 2
	refreshToken2 := "test/refresh/token/1"
	accessToken2 := "test/access/token/1"
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var authCount2, wantAuthCount2 int64
	var service2 string
	scopes2 := []string{
		"repository:dst:pull,push",
	}
	as2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service2 {
			t.Errorf("unexpected service: %v, want %v", got, service2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes2, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken2 {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount2, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken2); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as2.Close()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as2.URL, service2, strings.Join(scopes2, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts2.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			RefreshToken: refreshToken2,
		}),
		Cache: NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scopes1...)
	ctx = WithScopesForHost(ctx, uri2.Host, scopes2...)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// first request to server 2
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// repeated request to server 1
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1++; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// repeated request to server 2
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2++; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// credential change to server 1
	refreshToken1 = "test/refresh/token/1/new"
	accessToken1 = "test/access/token/1/new"
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client1.Credential = StaticCredential(uri1.Host, Credential{
		RefreshToken: refreshToken1,
	})
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// credential change to server 2
	refreshToken2 = "test/refresh/token/2/new"
	accessToken2 = "test/access/token/2/new"
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	client2.Credential = StaticCredential(uri2.Host, Credential{
		RefreshToken: refreshToken2,
	})
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
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

func TestClient_Do_Token_Expire_PerHost(t *testing.T) {
	// set up server 1
	refreshToken1 := "test/refresh/token/1"
	accessToken1 := "test/access/token/1"
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var authCount1, wantAuthCount1 int64
	var service1 string
	scopes1 := []string{
		"repository:src:pull",
	}
	as1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service1 {
			t.Errorf("unexpected service: %v, want %v", got, service1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes1, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken1 {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount1, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken1); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as1.URL, service1, strings.Join(scopes1, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			RefreshToken: refreshToken1,
		}),
		Cache: NewCache(),
	}
	// set up server 2
	refreshToken2 := "test/refresh/token/2"
	accessToken2 := "test/access/token/2"
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var authCount2, wantAuthCount2 int64
	var service2 string
	scopes2 := []string{
		"repository:dst:pull,push",
	}
	as2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service2 {
			t.Errorf("unexpected service: %v, want %v", got, service2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes2, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != refreshToken2 {
			t.Errorf("unexpected refresh token: %v, want %v", got, refreshToken2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount2, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken2); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as2.Close()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as2.URL, service2, strings.Join(scopes2, " "))
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts2.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			RefreshToken: refreshToken2,
		}),
		Cache: NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scopes1...)
	ctx = WithScopesForHost(ctx, uri2.Host, scopes2...)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// first request to server 2
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// invalidate the access token and request again to server 1
	accessToken1 = "test/access/token/1/new"
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}
	// invalidate the access token and request again to server 2
	accessToken2 = "test/access/token/2/new"
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
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

func TestClient_Do_Scope_Hint_Mismatch_PerHost(t *testing.T) {
	// set up server 1
	username1 := "test_user1"
	password1 := "test_password1"
	accessToken1 := "test/access/token/1"
	var requestCount1, wantRequestCount1 int64
	var successCount1, wantSuccessCount1 int64
	var authCount1, wantAuthCount1 int64
	var service1 string
	scopes1 := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	scope1 := "repository:test1:delete"
	as1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service1 {
			t.Errorf("unexpected service: %v, want %v", got, service1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scopes := CleanScopes(append([]string{scope1}, scopes1...))
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username1 {
			t.Errorf("unexpected username: %v, want %v", got, username1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password1 {
			t.Errorf("unexpected password: %v, want %v", got, password1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount1, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken1); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as1.Close()
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount1, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken1
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as1.URL, service1, scope1)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount1, 1)
	}))
	defer ts1.Close()
	uri1, err := url.Parse(ts1.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service1 = uri1.Host
	client1 := &Client{
		Credential: StaticCredential(uri1.Host, Credential{
			Username: username1,
			Password: password1,
		}),
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}

	// set up server 1
	username2 := "test_user2"
	password2 := "test_password2"
	accessToken2 := "test/access/token/2"
	var requestCount2, wantRequestCount2 int64
	var successCount2, wantSuccessCount2 int64
	var authCount2, wantAuthCount2 int64
	var service2 string
	scopes2 := []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
	scope2 := "repository:test2:delete"
	as2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.PostForm.Get("service"); got != service2 {
			t.Errorf("unexpected service: %v, want %v", got, service2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("client_id"); got != defaultClientID {
			t.Errorf("unexpected client id: %v, want %v", got, defaultClientID)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scopes := CleanScopes(append([]string{scope2}, scopes2...))
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("username"); got != username2 {
			t.Errorf("unexpected username: %v, want %v", got, username2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("password"); got != password2 {
			t.Errorf("unexpected password: %v, want %v", got, password2)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		atomic.AddInt64(&authCount2, 1)
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, accessToken2); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer as2.Close()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount2, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		header := "Bearer " + accessToken2
		if auth := r.Header.Get("Authorization"); auth != header {
			challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as2.URL, service2, scope2)
			w.Header().Set("Www-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(&successCount2, 1)
	}))
	defer ts1.Close()
	uri2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	service2 = uri2.Host
	client2 := &Client{
		Credential: StaticCredential(uri2.Host, Credential{
			Username: username2,
			Password: password2,
		}),
		ForceAttemptOAuth2: true,
		Cache:              NewCache(),
	}

	ctx := context.Background()
	ctx = WithScopesForHost(ctx, uri1.Host, scopes1...)
	ctx = WithScopesForHost(ctx, uri2.Host, scopes2...)
	// first request to server 1
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err := client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if wantAuthCount1++; authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// first request to server 1
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err := client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if wantAuthCount2++; authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
	}

	// repeated request to server 1
	// although the actual scope does not match the hinted scopes, the client
	// with cache cannot avoid a request to obtain a challenge but can prevent
	// a repeated call to the authorization server.
	req1, err = http.NewRequestWithContext(ctx, http.MethodGet, ts1.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp1, err = client1.Do(req1)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp1.StatusCode, http.StatusOK)
	}
	if wantRequestCount1 += 2; requestCount1 != wantRequestCount1 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount1, wantRequestCount1)
	}
	if wantSuccessCount1++; successCount1 != wantSuccessCount1 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount1, wantSuccessCount1)
	}
	if authCount1 != wantAuthCount1 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount1, wantAuthCount1)
	}

	// repeated request to server 2
	// although the actual scope does not match the hinted scopes, the client
	// with cache cannot avoid a request to obtain a challenge but can prevent
	// a repeated call to the authorization server.
	req2, err = http.NewRequestWithContext(ctx, http.MethodGet, ts2.URL, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	resp2, err = client2.Do(req2)
	if err != nil {
		t.Fatalf("Client.Do() error = %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Client.Do() = %v, want %v", resp2.StatusCode, http.StatusOK)
	}
	if wantRequestCount2 += 2; requestCount2 != wantRequestCount2 {
		t.Errorf("unexpected number of requests: %d, want %d", requestCount2, wantRequestCount2)
	}
	if wantSuccessCount2++; successCount2 != wantSuccessCount2 {
		t.Errorf("unexpected number of successful requests: %d, want %d", successCount2, wantSuccessCount2)
	}
	if authCount2 != wantAuthCount2 {
		t.Errorf("unexpected number of auth requests: %d, want %d", authCount2, wantAuthCount2)
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

func TestStaticCredential(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		target   string
		cred     Credential
		want     Credential
	}{
		{
			name:     "Matched credential for regular registry",
			registry: "registry.example.com",
			target:   "registry.example.com",
			cred: Credential{
				Username: "username",
				Password: "password",
			},
			want: Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:     "Matched credential for docker.io",
			registry: "docker.io",
			target:   "registry-1.docker.io",
			cred: Credential{
				Username: "username",
				Password: "password",
			},
			want: Credential{
				Username: "username",
				Password: "password",
			},
		},
		{
			name:     "Mismatched credential for regular registry",
			registry: "registry.example.com",
			target:   "whatever.example.com",
			cred: Credential{
				Username: "username",
				Password: "password",
			},
			want: EmptyCredential,
		},
		{
			name:     "Mismatched credential for docker.io",
			registry: "docker.io",
			target:   "whatever.docker.io",
			cred: Credential{
				Username: "username",
				Password: "password",
			},
			want: EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				Credential: StaticCredential(tt.registry, tt.cred),
			}
			ctx := context.Background()
			got, err := client.Credential(ctx, tt.target)
			if err != nil {
				t.Fatal("Client.Credential() error =", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Client.Credential() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClient_StaticCredential_basicAuth(t *testing.T) {
	testUsername := "username"
	testPassword := "password"

	// create a test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			t.Fatal("unexpected access")
		}
		switch path {
		case "/basicAuth":
			wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(testUsername+":"+testPassword))
			authHeader := r.Header.Get("Authorization")
			if authHeader != wantedAuthHeader {
				w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			w.WriteHeader(http.StatusNotAcceptable)
		}
	}))
	defer ts.Close()
	host := ts.URL
	uri, _ := url.Parse(host)
	hostAddress := uri.Host
	basicAuthURL := fmt.Sprintf("%s/basicAuth", host)

	// create a test client with the correct credentials
	clientValid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			Username: testUsername,
			Password: testPassword,
		}),
	}
	req, err := http.NewRequest(http.MethodGet, basicAuthURL, nil)
	if err != nil {
		t.Fatalf("could not create request, err = %v", err)
	}
	respValid, err := clientValid.Do(req)
	if err != nil {
		t.Fatalf("could not send request, err = %v", err)
	}
	if respValid.StatusCode != 200 {
		t.Errorf("incorrect status code: %d, expected 200", respValid.StatusCode)
	}

	// create a test client with incorrect credentials
	clientInvalid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			Username: "foo",
			Password: "bar",
		}),
	}
	respInvalid, err := clientInvalid.Do(req)
	if err != nil {
		t.Fatalf("could not send request, err = %v", err)
	}
	if respInvalid.StatusCode != 401 {
		t.Errorf("incorrect status code: %d, expected 401", respInvalid.StatusCode)
	}
}

func TestClient_StaticCredential_withAccessToken(t *testing.T) {
	var host string
	testAccessToken := "test/access/token"
	scope := "repository:test:pull,push"

	// create an authorization server
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		t.Error("unexecuted attempt of authorization service")
	}))
	defer as.Close()

	// create a test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			t.Fatal("unexpected access")
		}
		switch path {
		case "/accessToken":
			wantedAuthHeader := "Bearer " + testAccessToken
			if auth := r.Header.Get("Authorization"); auth != wantedAuthHeader {
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, scope)
				w.Header().Set("Www-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			w.WriteHeader(http.StatusNotAcceptable)
		}
	}))
	defer ts.Close()
	host = ts.URL
	uri, _ := url.Parse(host)
	hostAddress := uri.Host
	accessTokenURL := fmt.Sprintf("%s/accessToken", host)

	// create a test client with the correct credentials
	clientValid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			AccessToken: testAccessToken,
		}),
	}
	req, err := http.NewRequest(http.MethodGet, accessTokenURL, nil)
	if err != nil {
		t.Fatalf("could not create request, err = %v", err)
	}
	respValid, err := clientValid.Do(req)
	if err != nil {
		t.Fatalf("could not send request, err = %v", err)
	}
	if respValid.StatusCode != 200 {
		t.Errorf("incorrect status code: %d, expected 200", respValid.StatusCode)
	}

	// create a test client with incorrect credentials
	clientInvalid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			AccessToken: "foo",
		}),
	}
	respInvalid, err := clientInvalid.Do(req)
	if err != nil {
		t.Fatalf("could not send request, err = %v", err)
	}
	if respInvalid.StatusCode != 401 {
		t.Errorf("incorrect status code: %d, expected 401", respInvalid.StatusCode)
	}
}

func TestClient_StaticCredential_withRefreshToken(t *testing.T) {
	var host string
	testAccessToken := "test/access/token"
	testRefreshToken := "test/refresh/token"
	scope := "repository:test:pull,push"

	// create an authorization server
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusUnauthorized)
			t.Error("unexecuted attempt of authorization service")
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			t.Error("failed to parse form")
		}
		if got := r.PostForm.Get("service"); got != host {
			w.WriteHeader(http.StatusUnauthorized)
		}
		// handles refresh token requests
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			w.WriteHeader(http.StatusUnauthorized)
		}
		if got := r.PostForm.Get("scope"); got != scope {
			w.WriteHeader(http.StatusUnauthorized)
		}
		if got := r.PostForm.Get("refresh_token"); got != testRefreshToken {
			w.WriteHeader(http.StatusUnauthorized)
		}
		// writes back access token
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, testAccessToken); err != nil {
			t.Fatalf("could not write back access token, error = %v", err)
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
		case "/refreshToken":
			wantedAuthHeader := "Bearer " + testAccessToken
			if auth := r.Header.Get("Authorization"); auth != wantedAuthHeader {
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, scope)
				w.Header().Set("Www-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			w.WriteHeader(http.StatusNotAcceptable)
		}
	}))
	defer ts.Close()
	host = ts.URL
	uri, _ := url.Parse(host)
	hostAddress := uri.Host
	refreshTokenURL := fmt.Sprintf("%s/refreshToken", host)

	// create a test client with the correct credentials
	clientValid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			RefreshToken: testRefreshToken,
		}),
	}
	req, err := http.NewRequest(http.MethodGet, refreshTokenURL, nil)
	if err != nil {
		t.Fatalf("could not create request, err = %v", err)
	}
	respValid, err := clientValid.Do(req)
	if err != nil {
		t.Fatalf("could not send request, err = %v", err)
	}
	if respValid.StatusCode != 200 {
		t.Errorf("incorrect status code: %d, expected 200", respValid.StatusCode)
	}

	// create a test client with incorrect credentials
	clientInvalid := &Client{
		Credential: StaticCredential(hostAddress, Credential{
			RefreshToken: "bar",
		}),
	}
	_, err = clientInvalid.Do(req)

	var expectedError *errcode.ErrorResponse
	if !errors.As(err, &expectedError) || expectedError.StatusCode != http.StatusUnauthorized {
		t.Errorf("incorrect error: %v, expected %v", err, expectedError)
	}
}

func TestClient_fetchBasicAuth(t *testing.T) {
	c := &Client{
		Credential: func(ctx context.Context, registry string) (Credential, error) {
			return EmptyCredential, nil
		},
	}
	_, err := c.fetchBasicAuth(context.Background(), "")
	if err != ErrBasicCredentialNotFound {
		t.Errorf("incorrect error: %v, expected %v", err, ErrBasicCredentialNotFound)
	}
}
