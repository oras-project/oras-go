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

package credentials

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// testStore implements the Store interface, used for testing purpose.
type testStore struct {
	storage map[string]auth.Credential
}

func (t *testStore) Get(ctx context.Context, serverAddress string) (auth.Credential, error) {
	return t.storage[serverAddress], nil
}

func (t *testStore) Put(ctx context.Context, serverAddress string, cred auth.Credential) error {
	if len(t.storage) == 0 {
		t.storage = make(map[string]auth.Credential)
	}
	t.storage[serverAddress] = cred
	return nil
}

func (t *testStore) Delete(ctx context.Context, serverAddress string) error {
	delete(t.storage, serverAddress)
	return nil
}

func TestLogin(t *testing.T) {
	// create a test registry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantedAuthHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(testUsername+":"+testPassword))
		authHeader := r.Header.Get("Authorization")
		if authHeader != wantedAuthHeader {
			w.Header().Set("Www-Authenticate", `Basic realm="Test Server"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ts.Close()
	uri, _ := url.Parse(ts.URL)
	reg, err := remote.NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("cannot create test registry: %v", err)
	}
	reg.PlainHTTP = true
	// create a test store
	s := &testStore{}
	tests := []struct {
		name     string
		ctx      context.Context
		registry *remote.Registry
		cred     auth.Credential
		wantErr  bool
	}{
		{
			name:    "login succeeds",
			ctx:     context.Background(),
			cred:    auth.Credential{Username: testUsername, Password: testPassword},
			wantErr: false,
		},
		{
			name:    "login fails (incorrect password)",
			ctx:     context.Background(),
			cred:    auth.Credential{Username: testUsername, Password: "whatever"},
			wantErr: true,
		},
		{
			name:    "login fails (nil context makes remote.Ping fails)",
			ctx:     nil,
			cred:    auth.Credential{Username: testUsername, Password: testPassword},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// login to test registry
			err := Login(tt.ctx, s, reg, tt.cred)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Login() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := s.storage[reg.Reference.Registry]; !reflect.DeepEqual(got, tt.cred) {
				t.Fatalf("Stored credential = %v, want %v", got, tt.cred)
			}
			s.Delete(tt.ctx, reg.Reference.Registry)
		})
	}
}

func TestLogin_unsupportedClient(t *testing.T) {
	var testClient http.Client
	reg, err := remote.NewRegistry("whatever")
	if err != nil {
		t.Fatalf("cannot create test registry: %v", err)
	}
	reg.PlainHTTP = true
	reg.Client = &testClient
	ctx := context.Background()

	s := &testStore{}
	cred := auth.EmptyCredential
	err = Login(ctx, s, reg, cred)
	if wantErr := ErrClientTypeUnsupported; !errors.Is(err, wantErr) {
		t.Errorf("Login() error = %v, wantErr %v", err, wantErr)
	}
}

func TestLogout(t *testing.T) {
	// create a test store
	s := &testStore{}
	s.storage = map[string]auth.Credential{
		"localhost:2333":              {Username: "test_user", Password: "test_word"},
		"https://index.docker.io/v1/": {Username: "user", Password: "word"},
	}
	tests := []struct {
		name         string
		ctx          context.Context
		store        Store
		registryName string
		wantErr      bool
	}{
		{
			name:         "logout of regular registry",
			ctx:          context.Background(),
			registryName: "localhost:2333",
			wantErr:      false,
		},
		{
			name:         "logout of docker.io",
			ctx:          context.Background(),
			registryName: "docker.io",
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Logout(tt.ctx, s, tt.registryName); (err != nil) != tt.wantErr {
				t.Fatalf("Logout() error = %v, wantErr %v", err, tt.wantErr)
			}
			if s.storage[tt.registryName] != auth.EmptyCredential {
				t.Error("Credentials are not deleted")
			}
		})
	}
}

func Test_mapHostname(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{
			name: "map docker.io to https://index.docker.io/v1/",
			host: "docker.io",
			want: "https://index.docker.io/v1/",
		},
		{
			name: "do not map other host names",
			host: "localhost:2333",
			want: "localhost:2333",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ServerAddressFromRegistry(tt.host); got != tt.want {
				t.Errorf("mapHostname() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCredential(t *testing.T) {
	// create a test store
	s := &testStore{}
	s.storage = map[string]auth.Credential{
		"localhost:2333":              {Username: "test_user", Password: "test_word"},
		"https://index.docker.io/v1/": {Username: "user", Password: "word"},
	}
	// create a test client using Credential
	testClient := &auth.Client{}
	testClient.Credential = Credential(s)
	tests := []struct {
		name           string
		registry       string
		wantCredential auth.Credential
	}{
		{
			name:           "get credentials for localhost:2333",
			registry:       "localhost:2333",
			wantCredential: auth.Credential{Username: "test_user", Password: "test_word"},
		},
		{
			name:           "get credentials for registry-1.docker.io",
			registry:       "registry-1.docker.io",
			wantCredential: auth.Credential{Username: "user", Password: "word"},
		},
		{
			name:           "get credentials for a registry not stored",
			registry:       "localhost:6666",
			wantCredential: auth.EmptyCredential,
		},
		{
			name:           "get credentials for an empty string",
			registry:       "",
			wantCredential: auth.EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testClient.Credential(context.Background(), tt.registry)
			if err != nil {
				t.Errorf("could not get credential: %v", err)
			}
			if !reflect.DeepEqual(got, tt.wantCredential) {
				t.Errorf("Credential() = %v, want %v", got, tt.wantCredential)
			}
		})
	}
}
