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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

func TestNewClientBuilder(t *testing.T) {
	builder := NewClientBuilder()

	if builder.BaseTransport == nil {
		t.Error("BaseTransport should not be nil")
	}
	if builder.RetryPolicy == nil {
		t.Error("RetryPolicy should not be nil")
	}
	if builder.CacheFactory == nil {
		t.Error("CacheFactory should not be nil")
	}
}

func TestClientBuilder_Build(t *testing.T) {
	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if client == nil {
		t.Fatal("Build() returned nil client")
	}
	if client.Client == nil {
		t.Error("Build() returned client with nil HTTP client")
	}
	if client.Cache == nil {
		t.Error("Build() returned client with nil cache")
	}
}

func TestClientBuilder_Build_NilProps(t *testing.T) {
	builder := NewClientBuilder()

	_, err := builder.Build(nil)
	if err == nil {
		t.Error("Build(nil) should return error")
	}
}

func TestClientBuilder_Build_WithCredential(t *testing.T) {
	builder := NewClientBuilder()

	wantUsername := "test-user"
	wantPassword := "test-password"

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Credential: credentials.Credential{
			Username: wantUsername,
			Password: wantPassword,
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	cred, err := client.CredentialFunc(context.Background(), "test-registry.example.com")
	if err != nil {
		t.Fatalf("CredentialFunc() error = %v", err)
	}
	if cred.Username != wantUsername {
		t.Errorf("Username = %s, want %s", cred.Username, wantUsername)
	}
	if cred.Password != wantPassword {
		t.Errorf("Password = %s, want %s", cred.Password, wantPassword)
	}
}

func TestClientBuilder_Build_WithCredentialStore(t *testing.T) {
	wantUsername := "store-user"
	wantPassword := "store-password"
	wantRegistry := "test-registry.example.com"

	store := &mockCredentialStore{
		credentials: map[string]credentials.Credential{
			wantRegistry: {
				Username: wantUsername,
				Password: wantPassword,
			},
		},
	}

	builder := NewClientBuilder()
	builder.CredentialStore = store

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   wantRegistry,
			Repository: "test/repo",
		},
		// No credential in properties, should fall back to store
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	cred, err := client.CredentialFunc(context.Background(), wantRegistry)
	if err != nil {
		t.Fatalf("CredentialFunc() error = %v", err)
	}
	if cred.Username != wantUsername {
		t.Errorf("Username = %s, want %s", cred.Username, wantUsername)
	}
	if cred.Password != wantPassword {
		t.Errorf("Password = %s, want %s", cred.Password, wantPassword)
	}
}

func TestClientBuilder_Build_WithUserAgent(t *testing.T) {
	wantUserAgent := "test-agent/1.0"

	builder := NewClientBuilder()
	builder.UserAgent = wantUserAgent

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if got := client.Header.Get("User-Agent"); got != wantUserAgent {
		t.Errorf("User-Agent = %s, want %s", got, wantUserAgent)
	}
}

func TestClientBuilder_Build_WithCustomHeaders(t *testing.T) {
	wantHeaderKey := "X-Custom-Header"
	wantHeaderValue := "custom-value"

	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			HeaderFlags: map[string]string{
				wantHeaderKey: wantHeaderValue,
			},
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if got := client.Header.Get(wantHeaderKey); got != wantHeaderValue {
		t.Errorf("Header[%s] = %s, want %s", wantHeaderKey, got, wantHeaderValue)
	}
}

func TestClientBuilder_Build_WithCustomRetryPolicy(t *testing.T) {
	customPolicy := &retry.GenericPolicy{
		MaxRetry: 10,
	}

	builder := NewClientBuilder()
	builder.RetryPolicy = func() retry.Policy { return customPolicy }

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
	}

	_, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestClientBuilder_Build_WithCustomCache(t *testing.T) {
	customCache := auth.NewCache()
	cacheFactoryCalled := false

	builder := NewClientBuilder()
	builder.CacheFactory = func(registry string) auth.Cache {
		cacheFactoryCalled = true
		if registry != "test-registry.example.com" {
			t.Errorf("CacheFactory called with registry = %s, want test-registry.example.com", registry)
		}
		return customCache
	}

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if !cacheFactoryCalled {
		t.Error("CacheFactory was not called")
	}
	if client.Cache != customCache {
		t.Error("Client cache does not match custom cache")
	}
}

func TestClientBuilder_Build_WithTokenFetcher(t *testing.T) {
	wantToken := "custom-token"
	customFetcher := &mockTokenFetcher{token: wantToken}

	builder := NewClientBuilder()
	builder.TokenFetcher = customFetcher

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if client.TokenFetcher != customFetcher {
		t.Error("Client TokenFetcher does not match custom fetcher")
	}
}

func TestClientBuilder_Build_InsecureTLS(t *testing.T) {
	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			Insecure: true,
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// Verify the client was built successfully
	if client == nil {
		t.Fatal("Build() returned nil client")
	}
}

func TestClientBuilder_Build_WithCACert(t *testing.T) {
	// Create a temporary CA certificate
	tmpDir := t.TempDir()
	caCertPath := filepath.Join(tmpDir, "ca.crt")

	// Generate a self-signed CA certificate
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertDER,
	})

	if err := os.WriteFile(caCertPath, caCertPEM, 0644); err != nil {
		t.Fatalf("failed to write CA certificate: %v", err)
	}

	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			CACert: caCertPath,
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if client == nil {
		t.Fatal("Build() returned nil client")
	}
}

func TestClientBuilder_Build_WithCACerts(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate two CA certificates.
	var caCertPaths []string
	for i := 0; i < 2; i++ {
		caKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("failed to generate CA key: %v", err)
		}

		caTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(int64(i + 10)),
			Subject: pkix.Name{
				Organization: []string{"Test CA"},
			},
			NotBefore:             time.Now(),
			NotAfter:              time.Now().Add(time.Hour),
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}

		caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("failed to create CA certificate: %v", err)
		}

		caCertPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: caCertDER,
		})

		p := filepath.Join(tmpDir, fmt.Sprintf("ca%d.crt", i))
		if err := os.WriteFile(p, caCertPEM, 0644); err != nil {
			t.Fatalf("failed to write CA certificate: %v", err)
		}
		caCertPaths = append(caCertPaths, p)
	}

	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			CACerts: caCertPaths,
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if client == nil {
		t.Fatal("Build() returned nil client")
	}
}

func TestClientBuilder_Build_WithInvalidCACert(t *testing.T) {
	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			CACert: "/nonexistent/ca.crt",
		},
	}

	_, err := builder.Build(props)
	if err == nil {
		t.Error("Build() should return error for invalid CA cert path")
	}
}

func TestClientBuilder_Build_WithClientCert(t *testing.T) {
	// Create temporary client certificate and key
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "client.crt")
	keyPath := filepath.Join(tmpDir, "client.key")

	// Generate a self-signed client certificate
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Client"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, clientTemplate, &clientKey.PublicKey, clientKey)
	if err != nil {
		t.Fatalf("failed to create client certificate: %v", err)
	}

	clientCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCertDER,
	})

	clientKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	})

	if err := os.WriteFile(certPath, clientCertPEM, 0644); err != nil {
		t.Fatalf("failed to write client certificate: %v", err)
	}
	if err := os.WriteFile(keyPath, clientKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write client key: %v", err)
	}

	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			Cert: certPath,
			Key:  keyPath,
		},
	}

	client, err := builder.Build(props)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if client == nil {
		t.Fatal("Build() returned nil client")
	}
}

func TestClientBuilder_Build_WithInvalidClientCert(t *testing.T) {
	builder := NewClientBuilder()

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "test-registry.example.com",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			Cert: "/nonexistent/client.crt",
			Key:  "/nonexistent/client.key",
		},
	}

	_, err := builder.Build(props)
	if err == nil {
		t.Error("Build() should return error for invalid client cert path")
	}
}

func TestNewRepositoryWithProperties(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	props, err := properties.NewRegistry("example.com/test/repo")
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	repo, err := NewRepositoryWithProperties(props, nil)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	if repo == nil {
		t.Fatal("NewRepositoryWithProperties() returned nil repository")
	}
	if repo.Registry.Reference.Registry != "example.com" {
		t.Errorf("Reference.Registry = %s, want example.com", repo.Registry.Reference.Registry)
	}
	if repo.RepositoryName != "test/repo" {
		t.Errorf("RepositoryName = %s, want test/repo", repo.RepositoryName)
	}
}

func TestNewRepositoryWithProperties_NilProps(t *testing.T) {
	_, err := NewRepositoryWithProperties(nil, nil)
	if err == nil {
		t.Error("NewRepositoryWithProperties(nil, nil) should return error")
	}
}

func TestNewRepositoryWithProperties_PlainHTTP(t *testing.T) {
	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "localhost:5000",
			Repository: "test/repo",
		},
		Transport: properties.Transport{
			PlainHTTP: true,
		},
	}

	repo, err := NewRepositoryWithProperties(props, nil)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	if !repo.Registry.PlainHTTP {
		t.Error("PlainHTTP should be true")
	}
}

func TestNewRepositoryWithProperties_ReferrersAPISupported(t *testing.T) {
	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "example.com",
			Repository: "test/repo",
		},
		Attributes: properties.Attributes{
			ReferrersAPI: properties.ReferrersAPISupported,
		},
	}

	repo, err := NewRepositoryWithProperties(props, nil)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	// Conflicting set is silently ignored; capability should remain supported.
	repo.SetReferrersCapability(false)
	if !repo.getReferrersCapability().IsSupported() {
		t.Error("conflicting SetReferrersCapability(false) should be ignored when already set to supported")
	}
}

func TestNewRepositoryWithProperties_ReferrersAPIUnsupported(t *testing.T) {
	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "example.com",
			Repository: "test/repo",
		},
		Attributes: properties.Attributes{
			ReferrersAPI: properties.ReferrersAPIUnsupported,
		},
	}

	repo, err := NewRepositoryWithProperties(props, nil)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	// Conflicting set is silently ignored; capability should remain unsupported.
	repo.SetReferrersCapability(true)
	if !repo.getReferrersCapability().IsUnsupported() {
		t.Error("conflicting SetReferrersCapability(true) should be ignored when already set to unsupported")
	}
}

func TestNewRepositoryWithProperties_WithBuilder(t *testing.T) {
	builder := NewClientBuilder()
	builder.UserAgent = "test-agent/1.0"

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "example.com",
			Repository: "test/repo",
		},
	}

	repo, err := NewRepositoryWithProperties(props, builder)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	if repo == nil {
		t.Fatal("NewRepositoryWithProperties() returned nil repository")
	}
}

func TestNewRepositoryWithProperties_WithPolicyEvaluator(t *testing.T) {
	pol := policy.NewInsecureAcceptAnythingPolicy()
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	builder := NewClientBuilder()
	builder.PolicyEvaluator = evaluator

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   "example.com",
			Repository: "test/repo",
		},
	}

	repo, err := NewRepositoryWithProperties(props, builder)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties() error = %v", err)
	}

	if repo.Registry.Policy != evaluator {
		t.Error("Repository.Registry.Policy should be set to the builder's PolicyEvaluator")
	}
}

func TestNewRegistryWithProperties_WithPolicyEvaluator(t *testing.T) {
	pol := policy.NewInsecureAcceptAnythingPolicy()
	evaluator, err := policy.NewEvaluator(pol)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	builder := NewClientBuilder()
	builder.PolicyEvaluator = evaluator

	props := &properties.Registry{
		Reference: properties.Reference{
			Registry: "example.com",
		},
	}

	reg, err := NewRegistryWithProperties(props, builder)
	if err != nil {
		t.Fatalf("NewRegistryWithProperties() error = %v", err)
	}

	if reg.Policy != evaluator {
		t.Error("Registry.Policy should be set to the builder's PolicyEvaluator")
	}
}

// mockCredentialStore is a test helper that returns credentials from a map.
type mockCredentialStore struct {
	credentials map[string]credentials.Credential
}

func (m *mockCredentialStore) Get(ctx context.Context, serverAddress string) (credentials.Credential, error) {
	if cred, ok := m.credentials[serverAddress]; ok {
		return cred, nil
	}
	return credentials.EmptyCredential, nil
}

func (m *mockCredentialStore) Put(ctx context.Context, serverAddress string, cred credentials.Credential) error {
	m.credentials[serverAddress] = cred
	return nil
}

func (m *mockCredentialStore) Delete(ctx context.Context, serverAddress string) error {
	delete(m.credentials, serverAddress)
	return nil
}

// mockTokenFetcher is a test helper that returns a fixed token.
type mockTokenFetcher struct {
	token string
	err   error
}

func (m *mockTokenFetcher) FetchToken(ctx context.Context, params auth.TokenParams, cred credentials.Credential) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.token, nil
}
