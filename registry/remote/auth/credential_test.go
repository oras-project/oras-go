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
	clientValid := &auth.Client{
		Credential: auth.StaticCredential(hostAddress, auth.Credential{
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
	clientInvalid := &auth.Client{
		Credential: auth.StaticCredential(hostAddress, auth.Credential{
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

func TestCredential_StaticCredential_withAccessToken(t *testing.T) {
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
	host := ts.URL
	uri, _ := url.Parse(host)
	hostAddress := uri.Host
	accessTokenURL := fmt.Sprintf("%s/accessToken", host)

	// create a test client with the correct credentials
	clientValid := &auth.Client{
		Credential: auth.StaticCredential(hostAddress, auth.Credential{
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
	clientInvalid := &auth.Client{
		Credential: auth.StaticCredential(hostAddress, auth.Credential{
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

func TestCredential_StaticCredential_withRefreshToken(t *testing.T) {
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
	clientValid := &auth.Client{
		Credential: auth.StaticCredential(hostAddress, auth.Credential{
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

	// create a test client with the correct credentials
	// clientInvalid := &auth.Client{
	// 	Credential: auth.StaticCredential(hostAddress, auth.Credential{
	// 		RefreshToken: "bar",
	// 	}),
	// }
	// respInvalid, err := clientInvalid.Do(req)
	// if err != nil {
	// 	t.Fatalf("could not send request, err = %v", err)
	// }
	// if respInvalid.StatusCode != 401 {
	// 	t.Errorf("incorrect status code: %d, expected 401", respInvalid.StatusCode)
	// }
}
