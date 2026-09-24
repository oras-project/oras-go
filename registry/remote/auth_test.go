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

package remote

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func testRegistryResource(registry string) properties.Resource {
	return properties.Resource{Registry: registry}
}

var testUsername = "username"
var testPassword = "password"

// testStore implements the Store interface, used for testing purpose.
type testStore struct {
	storage map[string]credentials.Credential
}

func (t *testStore) Get(ctx context.Context, serverAddress string) (credentials.Credential, error) {
	return t.storage[serverAddress], nil
}

func (t *testStore) Put(ctx context.Context, serverAddress string, cred credentials.Credential) error {
	if len(t.storage) == 0 {
		t.storage = make(map[string]credentials.Credential)
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
	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("cannot create test registry: %v", err)
	}
	reg.PlainHTTP = true
	// create a test store
	s := &testStore{}
	tests := []struct {
		name       string
		ctx        context.Context
		repository string
		cred       credentials.Credential
		wantKey    string
		wantErr    bool
	}{
		{
			name:    "registry login succeeds",
			ctx:     context.Background(),
			cred:    credentials.Credential{Username: testUsername, Password: testPassword},
			wantKey: uri.Host,
		},
		{
			name:       "repository login succeeds",
			ctx:        context.Background(),
			repository: "team/app",
			cred:       credentials.Credential{Username: testUsername, Password: testPassword},
			wantKey:    uri.Host + "/team/app",
		},
		{
			name:    "login fails (incorrect password)",
			ctx:     context.Background(),
			cred:    credentials.Credential{Username: testUsername, Password: "whatever"},
			wantErr: true,
		},
		{
			name:    "login fails (nil context makes remote.Ping fails)",
			ctx:     nil,
			cred:    credentials.Credential{Username: testUsername, Password: testPassword},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg.Reference.Repository = tt.repository
			// login to test registry
			err := Login(tt.ctx, s, reg, tt.cred)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Login() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := s.storage[tt.wantKey]; !reflect.DeepEqual(got, tt.cred) {
				t.Fatalf("Stored credential = %v, want %v", got, tt.cred)
			}
			s.Delete(tt.ctx, tt.wantKey)
		})
	}
}

func TestLogin_InvalidResource(t *testing.T) {
	tests := []struct {
		name    string
		ref     properties.Reference
		wantErr error
	}{
		{
			name:    "tag is rejected",
			ref:     properties.Reference{Registry: "example.com", Repository: "team/app", Tag: "latest"},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "digest is rejected",
			ref:     properties.Reference{Registry: "example.com", Repository: "team/app", Digest: "sha256:abcd"},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "uppercase repository is rejected",
			ref:     properties.Reference{Registry: "example.com", Repository: "Team/App"},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "empty repository segment is rejected",
			ref:     properties.Reference{Registry: "example.com", Repository: "team//app"},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "trailing repository slash is rejected",
			ref:     properties.Reference{Registry: "example.com", Repository: "team/app/"},
			wantErr: errdef.ErrInvalidReference,
		},
		{
			name:    "docker.io path is rejected",
			ref:     properties.Reference{Registry: "docker.io", Repository: "library/alpine"},
			wantErr: errdef.ErrUnsupported,
		},
		{
			name:    "registry-1.docker.io path is rejected",
			ref:     properties.Reference{Registry: "registry-1.docker.io", Repository: "library/alpine"},
			wantErr: errdef.ErrUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &Registry{Reference: tt.ref}
			err := Login(context.Background(), &testStore{}, reg, credentials.EmptyCredential)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Login() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestLogin_customClientNotMutated exercises the branch of Login that reuses
// an existing *auth.Client (reg.Client set to a non-nil *auth.Client), and
// verifies that Login's local client is independent of the original: the
// original's CredentialFunc must be unchanged after Login runs, matching
// Login's documented "will not modify the original client" contract.
func TestLogin_customClientNotMutated(t *testing.T) {
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
	reg, err := NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("cannot create test registry: %v", err)
	}
	reg.PlainHTTP = true

	originalCredentialFunc := func(context.Context, properties.Resource) (credentials.Credential, error) {
		return credentials.EmptyCredential, nil
	}
	customClient := &auth.Client{
		Header:         http.Header{"X-Test": {"v"}},
		CredentialFunc: originalCredentialFunc,
	}
	reg.Client = customClient

	s := &testStore{}
	cred := credentials.Credential{Username: testUsername, Password: testPassword}
	if err := Login(context.Background(), s, reg, cred); err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if got := s.storage[reg.Reference.Registry]; !reflect.DeepEqual(got, cred) {
		t.Fatalf("Stored credential = %v, want %v", got, cred)
	}
	// The registry's client must be unchanged, and its CredentialFunc must
	// still be the one set before Login, not the login credential Login
	// attached to its own local clone.
	if reg.Client != customClient {
		t.Fatal("Login replaced reg.Client instead of leaving it untouched")
	}
	gotCred, err := customClient.CredentialFunc(context.Background(), properties.Resource{})
	if err != nil {
		t.Fatalf("customClient.CredentialFunc() error = %v", err)
	}
	if gotCred != credentials.EmptyCredential {
		t.Error("Login mutated the original client's CredentialFunc")
	}
}

func TestLogin_unsupportedClient(t *testing.T) {
	var testClient http.Client
	reg, err := NewRegistry("whatever")
	if err != nil {
		t.Fatalf("cannot create test registry: %v", err)
	}
	reg.PlainHTTP = true
	reg.Client = &testClient
	ctx := context.Background()

	s := &testStore{}
	cred := credentials.EmptyCredential
	err = Login(ctx, s, reg, cred)
	if wantErr := ErrClientTypeUnsupported; !errors.Is(err, wantErr) {
		t.Errorf("Login() error = %v, wantErr %v", err, wantErr)
	}
}

func TestLogout(t *testing.T) {
	// create a test store
	s := &testStore{}
	s.storage = map[string]credentials.Credential{
		"localhost:2333/team/app":     {Username: "test_user", Password: "test_word"},
		"https://index.docker.io/v1/": {Username: "user", Password: "word"},
	}
	tests := []struct {
		name     string
		resource properties.Resource
		wantKey  string
		wantErr  error
	}{
		{
			name:     "logout of repository",
			resource: properties.Resource{Registry: "localhost:2333", Path: "team/app"},
			wantKey:  "localhost:2333/team/app",
		},
		{
			name:     "logout of docker.io",
			resource: properties.Resource{Registry: "docker.io"},
			wantKey:  "https://index.docker.io/v1/",
		},
		{
			name:     "docker.io path is rejected",
			resource: properties.Resource{Registry: "docker.io", Path: "library/alpine"},
			wantErr:  errdef.ErrUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Logout(context.Background(), s, tt.resource)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Logout() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && s.storage[tt.wantKey] != credentials.EmptyCredential {
				t.Error("Credentials are not deleted")
			}
		})
	}
}

func TestLogout_InvalidResource(t *testing.T) {
	stored := credentials.Credential{Username: "test_user", Password: "test_word"}
	s := &testStore{storage: map[string]credentials.Credential{
		"example.com/team/app": stored,
	}}
	tests := []struct {
		name string
		path string
	}{
		{name: "uppercase repository is rejected", path: "Team/App"},
		{name: "empty repository segment is rejected", path: "team//app"},
		{name: "trailing repository slash is rejected", path: "team/app/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Logout(context.Background(), s, properties.Resource{
				Registry: "example.com",
				Path:     tt.path,
			})
			if !errors.Is(err, errdef.ErrInvalidReference) {
				t.Fatalf("Logout() error = %v, want %v", err, errdef.ErrInvalidReference)
			}
			if s.storage["example.com/team/app"] != stored {
				t.Error("Logout removed a credential stored under a different key")
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
			name: "map registry-1.docker.io to https://index.docker.io/v1/",
			host: "registry-1.docker.io",
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

func TestNewCredentialFunc_NilStore(t *testing.T) {
	fn := NewCredentialFunc(nil)
	got, err := fn(context.Background(), testRegistryResource("localhost:5000"))
	if err != nil {
		t.Fatalf("NewCredentialFunc(nil) returned error: %v", err)
	}
	if got != credentials.EmptyCredential {
		t.Errorf("NewCredentialFunc(nil) = %v, want EmptyCredential", got)
	}
}

func TestCredential(t *testing.T) {
	// create a test store
	s := &testStore{}
	s.storage = map[string]credentials.Credential{
		"localhost:2333":              {Username: "test_user", Password: "test_word"},
		"https://index.docker.io/v1/": {Username: "user", Password: "word"},
	}
	// create a test client using NewCredentialFunc
	testClient := &auth.Client{}
	testClient.CredentialFunc = NewCredentialFunc(s)
	tests := []struct {
		name           string
		registry       string
		wantCredential credentials.Credential
	}{
		{
			name:           "get credentials for localhost:2333",
			registry:       "localhost:2333",
			wantCredential: credentials.Credential{Username: "test_user", Password: "test_word"},
		},
		{
			name:           "get credentials for registry-1.docker.io",
			registry:       "registry-1.docker.io",
			wantCredential: credentials.Credential{Username: "user", Password: "word"},
		},
		{
			name:           "get credentials for a registry not stored",
			registry:       "localhost:6666",
			wantCredential: credentials.EmptyCredential,
		},
		{
			name:           "get credentials for an empty string",
			registry:       "",
			wantCredential: credentials.EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testClient.CredentialFunc(context.Background(), testRegistryResource(tt.registry))
			if err != nil {
				t.Errorf("could not get credential: %v", err)
			}
			if !reflect.DeepEqual(got, tt.wantCredential) {
				t.Errorf("NewCredentialFunc() = %v, want %v", got, tt.wantCredential)
			}
		})
	}
}

// errorStore is a credential store whose Get always fails.
type errorStore struct {
	testStore
	err error
}

func (s *errorStore) Get(context.Context, string) (credentials.Credential, error) {
	return credentials.EmptyCredential, s.err
}

func TestNewCredentialFunc_NamespacedResource(t *testing.T) {
	repoCred := credentials.Credential{Username: "repo", Password: "repo_pass"}
	nsCred := credentials.Credential{Username: "ns", Password: "ns_pass"}
	hostCred := credentials.Credential{Username: "host", Password: "host_pass"}
	dockerCred := credentials.Credential{Username: "docker", Password: "docker_pass"}
	s := &testStore{storage: map[string]credentials.Credential{
		"localhost:5000/team/app":     repoCred,
		"localhost:5000/team":         nsCred,
		"localhost:5000":              hostCred,
		"https://index.docker.io/v1/": dockerCred,
	}}
	fn := NewCredentialFunc(s)

	tests := []struct {
		name     string
		resource properties.Resource
		want     credentials.Credential
	}{
		{
			name:     "exact repository match",
			resource: properties.Resource{Registry: "localhost:5000", Path: "team/app"},
			want:     repoCred,
		},
		{
			name:     "falls back to namespace",
			resource: properties.Resource{Registry: "localhost:5000", Path: "team/other"},
			want:     nsCred,
		},
		{
			name:     "falls back to namespace for nested repository",
			resource: properties.Resource{Registry: "localhost:5000", Path: "team/sub/app"},
			want:     nsCred,
		},
		{
			name:     "falls back to host",
			resource: properties.Resource{Registry: "localhost:5000", Path: "other/app"},
			want:     hostCred,
		},
		{
			name:     "single segment path falls back to host",
			resource: properties.Resource{Registry: "localhost:5000", Path: "app"},
			want:     hostCred,
		},
		{
			name:     "docker.io uses the server address without the path",
			resource: properties.Resource{Registry: "docker.io", Path: "library/alpine"},
			want:     dockerCred,
		},
		{
			name:     "unknown host",
			resource: properties.Resource{Registry: "localhost:6666", Path: "team/app"},
			want:     credentials.EmptyCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fn(context.Background(), tt.resource)
			if err != nil {
				t.Fatalf("NewCredentialFunc() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("NewCredentialFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewCredentialFunc_StoreError(t *testing.T) {
	wantErr := errors.New("store failure")
	fn := NewCredentialFunc(&errorStore{err: wantErr})

	for _, resource := range []properties.Resource{
		{Registry: "localhost:5000", Path: "team/app"},
		{Registry: "localhost:5000"},
	} {
		t.Run(resource.String(), func(t *testing.T) {
			got, err := fn(context.Background(), resource)
			if !errors.Is(err, wantErr) {
				t.Fatalf("NewCredentialFunc() error = %v, want %v", err, wantErr)
			}
			if got != credentials.EmptyCredential {
				t.Errorf("NewCredentialFunc() = %v, want EmptyCredential", got)
			}
		})
	}
}
