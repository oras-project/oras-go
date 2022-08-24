package auth_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestCredential_StaticCredential_basicAuth(t *testing.T) {
	test_username := "test_username"
	test_password := "test_password"

	// create a test server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			panic("unexpected access")
		}
		switch path {
		case "/basicAuth":
			wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(test_username+":"+test_password))
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
	expectedHostAddress := uri.Host
	basicAuthTargetURL := fmt.Sprintf("%s/basicAuth", host)

	// create a test client_correct with correct credentials
	client_correct := &auth.Client{
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			Username: test_username,
			Password: test_password,
		}),
	}
	req, err := http.NewRequest(http.MethodGet, basicAuthTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp_correct, err := client_correct.Do(req)
	if err != nil {
		panic(err)
	}

	if resp_correct.StatusCode != 200 {
		t.Error("Bad")
	}

	// create a test client with incorrect credentials
	client_incorrect := &auth.Client{
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			Username: "bad",
			Password: "bad",
		}),
	}
	resp_incorrect, err := client_incorrect.Do(req)
	if err != nil {
		panic(err)
	}

	if resp_incorrect.StatusCode != 401 {
		t.Error("Bad")
	}
}

func TestCredential_StaticCredential_AccessToken(t *testing.T) {
	client := &auth.Client{
		Credential: auth.StaticCredential("address", auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}
	req, err := http.NewRequest(http.MethodGet, basicAuthTargetURL, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	if resp.StatusCode != 200 {
		t.Error("Bad")
	}
}
