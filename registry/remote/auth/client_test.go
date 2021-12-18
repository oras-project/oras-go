package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestClient_SetUserAgent(t *testing.T) {
	wantUserAgent := "test agent"
	var requestCount, wantRequestCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if userAgent := r.Header.Get("User-Agent"); userAgent != wantUserAgent {
			t.Errorf("unexpected User-Agent: %v, want %v", userAgent, wantUserAgent)
		}
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
}

func TestClient_Do_Basic_Auth(t *testing.T) {
	username := "test_user"
	password := "test_password"
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	var requestCount, wantRequestCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != header {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
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
}

func TestClient_Do_Basic_Auth_Cached(t *testing.T) {
	username := "test_user"
	password := "test_password"
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	var requestCount, wantRequestCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != header {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
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

	// credential change
	username = "test_user2"
	password = "test_password2"
	header = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))

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
}
