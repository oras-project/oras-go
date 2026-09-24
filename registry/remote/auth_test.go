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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configtest"
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
		name     string
		ctx      context.Context
		registry *Registry
		cred     credentials.Credential
		wantErr  bool
	}{
		{
			name:    "login succeeds",
			ctx:     context.Background(),
			cred:    credentials.Credential{Username: testUsername, Password: testPassword},
			wantErr: false,
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
		"localhost:2333":              {Username: "test_user", Password: "test_word"},
		"https://index.docker.io/v1/": {Username: "user", Password: "word"},
	}
	tests := []struct {
		name         string
		ctx          context.Context
		store        credentials.Store
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
			if s.storage[tt.registryName] != credentials.EmptyCredential {
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

// matchingStore is a credential store that performs its own longest-prefix
// namespace matching, as the hierarchical file store does. It records the keys
// it was asked for.
type matchingStore struct {
	testStore
	queried []string
}

func (s *matchingStore) Get(_ context.Context, serverAddress string) (credentials.Credential, error) {
	s.queried = append(s.queried, serverAddress)
	var bestMatch string
	var found bool
	for addr := range s.storage {
		if addr != serverAddress &&
			(!strings.HasPrefix(serverAddress, addr) || serverAddress[len(addr)] != '/') {
			continue
		}
		if !found || len(addr) > len(bestMatch) {
			bestMatch, found = addr, true
		}
	}
	if !found {
		return credentials.EmptyCredential, nil
	}
	return s.storage[bestMatch], nil
}

func (s *matchingStore) MatchesNamespace(string) bool {
	return true
}

func TestNewCredentialFunc_NamespaceMatchingStore(t *testing.T) {
	hostCred := credentials.Credential{Username: "host", Password: "host_pass"}
	nsCred := credentials.Credential{Username: "ns", Password: "ns_pass"}

	tests := []struct {
		name        string
		storage     map[string]credentials.Credential
		resource    properties.Resource
		want        credentials.Credential
		wantQueried []string
	}{
		{
			// The store owns the walk, so an entry that is present but holds
			// no credential means anonymous access for that namespace instead
			// of falling back to the host.
			name: "empty namespace entry wins over the host credential",
			storage: map[string]credentials.Credential{
				"localhost:5000":        hostCred,
				"localhost:5000/public": credentials.EmptyCredential,
			},
			resource:    properties.Resource{Registry: "localhost:5000", Path: "public/img"},
			want:        credentials.EmptyCredential,
			wantQueried: []string{"localhost:5000/public/img"},
		},
		{
			name: "namespace credential is matched by the store in one lookup",
			storage: map[string]credentials.Credential{
				"localhost:5000":      hostCred,
				"localhost:5000/team": nsCred,
			},
			resource:    properties.Resource{Registry: "localhost:5000", Path: "team/app"},
			want:        nsCred,
			wantQueried: []string{"localhost:5000/team/app"},
		},
		{
			name: "host credential is matched by the store in one lookup",
			storage: map[string]credentials.Credential{
				"localhost:5000": hostCred,
			},
			resource:    properties.Resource{Registry: "localhost:5000", Path: "team/app"},
			want:        hostCred,
			wantQueried: []string{"localhost:5000/team/app"},
		},
		{
			// Without a path there is no namespace to match, so the host is
			// used as the key directly.
			name: "resource without a path uses the host key",
			storage: map[string]credentials.Credential{
				"localhost:5000": hostCred,
			},
			resource:    properties.Resource{Registry: "localhost:5000"},
			want:        hostCred,
			wantQueried: []string{"localhost:5000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &matchingStore{testStore: testStore{storage: tt.storage}}
			got, err := NewCredentialFunc(s)(context.Background(), tt.resource)
			if err != nil {
				t.Fatalf("NewCredentialFunc() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("NewCredentialFunc() = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(s.queried, tt.wantQueried) {
				t.Errorf("store queried for %v, want %v", s.queried, tt.wantQueried)
			}
		})
	}
}

// nonMatchingStore reports that it does not match namespaces, so the caller
// walks on its behalf.
type nonMatchingStore struct {
	testStore
	queried []string
}

func (s *nonMatchingStore) Get(_ context.Context, serverAddress string) (credentials.Credential, error) {
	s.queried = append(s.queried, serverAddress)
	return s.storage[serverAddress], nil
}

func (s *nonMatchingStore) MatchesNamespace(string) bool {
	return false
}

func TestNewCredentialFunc_NonMatchingStoreWalks(t *testing.T) {
	hostCred := credentials.Credential{Username: "host", Password: "host_pass"}
	s := &nonMatchingStore{testStore: testStore{storage: map[string]credentials.Credential{
		"localhost:5000":        hostCred,
		"localhost:5000/public": credentials.EmptyCredential,
	}}}

	// An empty entry is indistinguishable from an absent one here, so the walk
	// continues past it to the host.
	got, err := NewCredentialFunc(s)(context.Background(), properties.Resource{
		Registry: "localhost:5000",
		Path:     "public/img",
	})
	if err != nil {
		t.Fatalf("NewCredentialFunc() error = %v", err)
	}
	if got != hostCred {
		t.Errorf("NewCredentialFunc() = %v, want %v", got, hostCred)
	}
	wantQueried := []string{
		"localhost:5000/public/img",
		"localhost:5000/public",
		"localhost:5000",
	}
	if !reflect.DeepEqual(s.queried, wantQueried) {
		t.Errorf("store queried for %v, want %v", s.queried, wantQueried)
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

// TestNewCredentialFunc_HierarchicalDynamicStore exercises the layered rule
// end to end against a real hierarchical DynamicStore, which is the case the
// rule exists for: the store owns the namespace walk, so an entry that is
// present but carries no credential means anonymous access for that namespace
// rather than a fallback to the host credential.
func TestNewCredentialFunc_HierarchicalDynamicStore(t *testing.T) {
	cfg := configtest.Config{
		AuthConfigs: map[string]configtest.AuthConfig{
			"localhost:5000": {
				// base64("host:host_pass")
				Auth: "aG9zdDpob3N0X3Bhc3M=",
			},
			// Present but carrying no credential: anonymous for this
			// namespace.
			"localhost:5000/public": {},
			"localhost:5000/team": {
				// base64("team:team_pass")
				Auth: "dGVhbTp0ZWFtX3Bhc3M=",
			},
		},
	}
	jsonStr, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, jsonStr, 0666); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	store, err := credentials.NewStore(configPath, credentials.StoreOptions{
		Hierarchical: true,
		// Keep the plaintext file store in play regardless of the platform
		// default native store.
		IgnoreDefaultNativeStore: true,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	fn := NewCredentialFunc(store)

	tests := []struct {
		name     string
		resource properties.Resource
		want     credentials.Credential
	}{
		{
			name:     "empty namespace entry means anonymous, not the host credential",
			resource: properties.Resource{Registry: "localhost:5000", Path: "public/img"},
			want:     credentials.EmptyCredential,
		},
		{
			name:     "namespace credential is used for a repository under it",
			resource: properties.Resource{Registry: "localhost:5000", Path: "team/app"},
			want:     credentials.Credential{Username: "team", Password: "team_pass"},
		},
		{
			name:     "unmatched namespace falls back to the host credential",
			resource: properties.Resource{Registry: "localhost:5000", Path: "other/app"},
			want:     credentials.Credential{Username: "host", Password: "host_pass"},
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
