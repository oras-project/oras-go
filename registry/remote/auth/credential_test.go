package auth_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		t.Error("unexecuted attempt of authorization service")
		w.WriteHeader(http.StatusUnauthorized)
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
	testAccessToken := "test/access/token"
	testRefreshToken := "test/refresh/token"

	// create an authorization server
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
		if got := r.PostForm.Get("service"); got != host {
			t.Errorf("unexpected service: %v, want %v", got, service)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scope := strings.Join(scopes, " ")
		if got := r.PostForm.Get("scope"); got != scope {
			t.Errorf("unexpected scope: %v, want %v", got, scope)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != testRefreshToken {
			// t.Errorf("unexpected refresh token: %v, want %v", got, testRefreshToken)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if _, err := fmt.Fprintf(w, `{"access_token":%q}`, testAccessToken); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
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
				challenge := fmt.Sprintf("Bearer realm=%q,service=%q,scope=%q", as.URL, host, strings.Join(scopes, " "))
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
	expectedHostAddress := uri.Host
	refreshTokenTargetURL = fmt.Sprintf("%s/refreshToken", host)

	// correct client
	client := &auth.Client{
		Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
			RefreshToken: testRefreshToken,
		}),
	}
	req, err := http.NewRequest(http.MethodGet, refreshTokenTargetURL, nil)
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

	// incorrect client
	// client2 := &auth.Client{
	// 	Credential: auth.StaticCredential(expectedHostAddress, auth.Credential{
	// 		RefreshToken: "bad",
	// 	}),
	// }
	// resp2, err := client2.Do(req)
	// if err != nil {
	// 	t.Error("Bad111")
	// }

	// if resp2.StatusCode != 401 {
	// 	t.Error("Bad")
	// }
}
