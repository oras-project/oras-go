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
	scope := "repository:test:pull,push"
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("invalid auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("invalid service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("scope"); got != scope {
			t.Errorf("invalid scope: got %s, want %s", got, scope)
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
	scope := "repository:test:pull,push"
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexecuted attempt of authorization service")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if auth := r.Header.Get("Authorization"); auth != header {
			t.Errorf("invalid auth: got %s, want %s", auth, header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("invalid service: got %s, want %s", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("scope"); got != scope {
			t.Errorf("invalid scope: got %s, want %s", got, scope)
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

}

func TestClient_Do_Bearer_OAuth2_RefreshToken(t *testing.T) {

}

func TestClient_Do_Token_Expire(t *testing.T) {

}

func TestClient_Do_Invalid_Credential(t *testing.T) {

}

func TestClient_Do_Scheme_Change(t *testing.T) {

}
