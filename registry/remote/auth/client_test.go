package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_SetUserAgent(t *testing.T) {
	wantUserAgent := "test agent"
	requested := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/ping" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if userAgent := r.Header.Get("User-Agent"); userAgent != wantUserAgent {
			t.Errorf("unexpected User-Agent: %v, want %v", userAgent, wantUserAgent)
		}
		requested = true
	}))
	defer ts.Close()

	var client Client
	client.SetUserAgent(wantUserAgent)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ping", nil)
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
	if !requested {
		t.Errorf("no request made by Client.Do()")
	}
}
