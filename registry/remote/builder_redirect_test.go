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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// writeTestClientCert writes a self-signed client certificate and key.
func writeTestClientCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "oras-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// TestClientBuilder_CrossOriginRedirect checks that a client built from
// registry properties keeps the registry's custom headers and client
// certificate on that registry: a redirect to another origin (for example a
// blob storage backend) must receive neither.
func TestClientBuilder_CrossOriginRedirect(t *testing.T) {
	var gotHeader string
	var gotCerts int
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Registry-Secret")
		if r.TLS != nil {
			gotCerts = len(r.TLS.PeerCertificates)
		}
		w.WriteHeader(http.StatusOK)
	}))
	other.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	other.StartTLS()
	defer other.Close()

	var registryHeader string
	var registryCerts int
	registry := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registryHeader = r.Header.Get("X-Registry-Secret")
		if r.TLS != nil {
			registryCerts = len(r.TLS.PeerCertificates)
		}
		http.Redirect(w, r, other.URL+"/blob", http.StatusTemporaryRedirect)
	}))
	registry.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	registry.StartTLS()
	defer registry.Close()

	certPath, keyPath := writeTestClientCert(t)
	props := &properties.Registry{
		Reference: properties.Reference{Registry: registry.Listener.Addr().String(), Repository: "test"},
		Transport: properties.Transport{
			Insecure:    true,
			Cert:        certPath,
			Key:         keyPath,
			HeaderFlags: map[string]string{"X-Registry-Secret": "secret"},
		},
	}
	client, err := NewClientBuilder().Build(props)
	if err != nil {
		t.Fatal("Build() error =", err)
	}
	req, err := http.NewRequest(http.MethodGet, registry.URL+"/v2/test/blobs/sha256:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("Do() error =", err)
	}
	resp.Body.Close()

	if registryHeader != "secret" || registryCerts != 1 {
		t.Errorf("registry received X-Registry-Secret = %q and %d client certificate(s), want %q and 1",
			registryHeader, registryCerts, "secret")
	}
	if gotHeader != "" {
		t.Errorf("cross-origin redirect target received X-Registry-Secret = %q, want none", gotHeader)
	}
	if gotCerts != 0 {
		t.Errorf("cross-origin redirect target received %d client certificate(s), want none", gotCerts)
	}
}

// tlsRecorder is a TLS test server that requests a client certificate and
// records the X-Registry-Secret header and the number of client certificates
// of the last request it received.
type tlsRecorder struct {
	*httptest.Server
	header string
	certs  int
}

func newTLSRecorder(t *testing.T, next http.HandlerFunc) *tlsRecorder {
	t.Helper()
	rec := &tlsRecorder{}
	rec.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.header = r.Header.Get("X-Registry-Secret")
		if r.TLS != nil {
			rec.certs = len(r.TLS.PeerCertificates)
		}
		next(w, r)
	}))
	rec.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	rec.StartTLS()
	t.Cleanup(rec.Close)
	return rec
}

// TestClientBuilder_MirrorCrossOriginRedirect checks that a mirror client,
// which ClientBuilder builds from the mirror's own transport settings, keeps
// the mirror's custom headers and client certificate on the mirror: a
// redirect to another origin must receive neither.
func TestClientBuilder_MirrorCrossOriginRedirect(t *testing.T) {
	other := newTLSRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mirror := newTLSRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/blob", http.StatusTemporaryRedirect)
	})

	certPath, keyPath := writeTestClientCert(t)
	props := &properties.Registry{
		Reference: properties.Reference{Registry: "registry.example", Repository: "test"},
		Mirrors: []properties.Mirror{{
			Location: mirror.Listener.Addr().String(),
			Transport: properties.Transport{
				Insecure:    true,
				Cert:        certPath,
				Key:         keyPath,
				HeaderFlags: map[string]string{"X-Registry-Secret": "secret"},
			},
		}},
	}
	repo, err := NewClientBuilder().NewRepository(props)
	if err != nil {
		t.Fatal("NewRepository() error =", err)
	}
	if len(repo.mirrors) != 1 {
		t.Fatalf("got %d mirrors, want 1", len(repo.mirrors))
	}
	req, err := http.NewRequest(http.MethodGet, mirror.URL+"/v2/test/blobs/sha256:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := repo.mirrors[0].client().Do(req)
	if err != nil {
		t.Fatal("Do() error =", err)
	}
	resp.Body.Close()

	if mirror.header != "secret" || mirror.certs != 1 {
		t.Errorf("mirror received X-Registry-Secret = %q and %d client certificate(s), want %q and 1",
			mirror.header, mirror.certs, "secret")
	}
	if other.header != "" {
		t.Errorf("cross-origin redirect target received X-Registry-Secret = %q, want none", other.header)
	}
	if other.certs != 0 {
		t.Errorf("cross-origin redirect target received %d client certificate(s), want none", other.certs)
	}
}

// recordingTransport records whether it was used and the headers it saw.
type recordingTransport struct {
	used   bool
	header http.Header
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.used = true
	rt.header = req.Header
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

// TestOriginScopedTransport_Routing checks which transport each request URL
// is routed to, and that custom headers are removed off-origin only.
func TestOriginScopedTransport_Routing(t *testing.T) {
	tests := []struct {
		origin, target string
		wantRegistry   bool
	}{
		{"https://registry.example", "https://registry.example/v2/", true},
		{"https://registry.example", "https://registry.example:443/v2/", true},
		{"https://registry.example", "https://REGISTRY.example/v2/", true},
		{"http://localhost", "http://localhost:80/v2/", true},
		{"https://[0:0:0:0:0:0:0:1]:5000", "https://[::1]:5000/v2/", true},
		{"https://registry.example", "https://registry.example:5000/v2/", false},
		{"https://registry.example", "http://registry.example/v2/", false},
		{"https://registry.example", "https://blobs.example/blob", false},
		{"https://localhost", "https://127.0.0.1:443/v2/", false},
		{"https://127.0.0.1", "https://localhost/v2/", false},
	}
	for _, tt := range tests {
		t.Run(tt.origin+" "+tt.target, func(t *testing.T) {
			origin, err := url.Parse(tt.origin)
			if err != nil {
				t.Fatal(err)
			}
			registry, other := &recordingTransport{}, &recordingTransport{}
			transport := &originScopedTransport{
				origin:           origin,
				registry:         registry,
				other:            other,
				sensitiveHeaders: []string{"X-Registry-Secret"},
			}
			req, err := http.NewRequest(http.MethodGet, tt.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Registry-Secret", "secret")
			req.Header.Set("User-Agent", "oras-test")
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			if registry.used != tt.wantRegistry || other.used == tt.wantRegistry {
				t.Fatalf("registry transport used = %v, other transport used = %v, want registry = %v",
					registry.used, other.used, tt.wantRegistry)
			}
			if req.Header.Get("X-Registry-Secret") != "secret" {
				t.Error("RoundTrip modified the caller's request headers")
			}
			if tt.wantRegistry {
				if got := registry.header.Get("X-Registry-Secret"); got != "secret" {
					t.Errorf("registry origin received X-Registry-Secret = %q, want %q", got, "secret")
				}
				return
			}
			if got := other.header.Get("X-Registry-Secret"); got != "" {
				t.Errorf("other origin received X-Registry-Secret = %q, want none", got)
			}
			if got := other.header.Get("User-Agent"); got != "oras-test" {
				t.Errorf("other origin received User-Agent = %q, want %q", got, "oras-test")
			}
		})
	}
}

// TestClientBuilder_NoScopingWithoutCredentials checks that a plain client
// keeps the unwrapped transport.
func TestClientBuilder_NoScopingWithoutCredentials(t *testing.T) {
	props := &properties.Registry{Reference: properties.Reference{Registry: "registry.example", Repository: "test"}}
	client, err := NewClientBuilder().Build(props)
	if err != nil {
		t.Fatal("Build() error =", err)
	}
	if _, ok := client.Client.Transport.(*originScopedTransport); ok {
		t.Error("client without certificate or headers got an origin-scoped transport")
	}
}
