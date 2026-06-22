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

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func TestLoadCertsDirFromPaths_CACerts(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	caCertPath := filepath.Join(hostDir, "ca.crt")
	if err := os.WriteFile(caCertPath, []byte("ca-cert-data"), 0644); err != nil {
		t.Fatal(err)
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	if len(cd.CACertPaths) != 1 || cd.CACertPaths[0] != caCertPath {
		t.Errorf("CACertPaths = %v, want [%s]", cd.CACertPaths, caCertPath)
	}
	if cd.ClientCert != "" {
		t.Errorf("ClientCert = %s, want empty", cd.ClientCert)
	}
	if cd.ClientKey != "" {
		t.Errorf("ClientKey = %s, want empty", cd.ClientKey)
	}
}

func TestLoadCertsDirFromPaths_MultipleCACerts(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create multiple .crt files — should be returned sorted.
	crt1 := filepath.Join(hostDir, "beta.crt")
	crt2 := filepath.Join(hostDir, "alpha.crt")
	for _, p := range []string{crt1, crt2} {
		if err := os.WriteFile(p, []byte("cert"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	want := []string{crt2, crt1} // alpha before beta
	if !reflect.DeepEqual(cd.CACertPaths, want) {
		t.Errorf("CACertPaths = %v, want %v", cd.CACertPaths, want)
	}
}

func TestLoadCertsDirFromPaths_ClientCertAndKey(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(hostDir, "client.cert")
	keyPath := filepath.Join(hostDir, "client.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	if cd.ClientCert != certPath {
		t.Errorf("ClientCert = %s, want %s", cd.ClientCert, certPath)
	}
	if cd.ClientKey != keyPath {
		t.Errorf("ClientKey = %s, want %s", cd.ClientKey, keyPath)
	}
}

func TestLoadCertsDirFromPaths_CertWithoutKey(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(hostDir, "client.cert")
	if err := os.WriteFile(certPath, []byte("cert"), 0644); err != nil {
		t.Fatal(err)
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	if cd.ClientCert != certPath {
		t.Errorf("ClientCert = %s, want %s", cd.ClientCert, certPath)
	}
	if cd.ClientKey != "" {
		t.Errorf("ClientKey = %s, want empty", cd.ClientKey)
	}
}

func TestLoadCertsDirFromPaths_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd != nil {
		t.Errorf("LoadCertsDirFromPaths() = %v, want nil", cd)
	}
}

func TestLoadCertsDirFromPaths_NonexistentDir(t *testing.T) {
	cd, err := LoadCertsDirFromPaths("example.com", []string{"/nonexistent/path"})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd != nil {
		t.Errorf("LoadCertsDirFromPaths() = %v, want nil", cd)
	}
}

func TestLoadCertsDirFromPaths_MultipleBaseDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	hostDir1 := filepath.Join(dir1, "example.com")
	hostDir2 := filepath.Join(dir2, "example.com")
	if err := os.MkdirAll(hostDir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hostDir2, 0755); err != nil {
		t.Fatal(err)
	}

	crt1 := filepath.Join(hostDir1, "first.crt")
	crt2 := filepath.Join(hostDir2, "second.crt")
	if err := os.WriteFile(crt1, []byte("cert1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crt2, []byte("cert2"), 0644); err != nil {
		t.Fatal(err)
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{dir1, dir2})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	if len(cd.CACertPaths) != 2 {
		t.Fatalf("CACertPaths length = %d, want 2", len(cd.CACertPaths))
	}
	if cd.CACertPaths[0] != crt1 {
		t.Errorf("CACertPaths[0] = %s, want %s", cd.CACertPaths[0], crt1)
	}
	if cd.CACertPaths[1] != crt2 {
		t.Errorf("CACertPaths[1] = %s, want %s", cd.CACertPaths[1], crt2)
	}
}

func TestCertsDir_ApplyToTransport(t *testing.T) {
	cd := &CertsDir{
		CACertPaths: []string{"/path/to/ca.crt"},
		ClientCert:  "/path/to/client.cert",
		ClientKey:   "/path/to/client.key",
	}

	transport := &properties.Transport{}
	cd.ApplyToTransport(transport)

	if !reflect.DeepEqual(transport.CACerts, cd.CACertPaths) {
		t.Errorf("CACerts = %v, want %v", transport.CACerts, cd.CACertPaths)
	}
	if transport.Cert != cd.ClientCert {
		t.Errorf("Cert = %s, want %s", transport.Cert, cd.ClientCert)
	}
	if transport.Key != cd.ClientKey {
		t.Errorf("Key = %s, want %s", transport.Key, cd.ClientKey)
	}
}

func TestCertsDir_ApplyToTransport_NoOverwrite(t *testing.T) {
	cd := &CertsDir{
		CACertPaths: []string{"/new/ca.crt"},
		ClientCert:  "/new/client.cert",
		ClientKey:   "/new/client.key",
	}

	transport := &properties.Transport{
		CACert: "/existing/ca.crt",
		Cert:   "/existing/client.cert",
		Key:    "/existing/client.key",
	}

	cd.ApplyToTransport(transport)

	// Existing values should not be overwritten.
	if transport.CACert != "/existing/ca.crt" {
		t.Errorf("CACert = %s, want /existing/ca.crt", transport.CACert)
	}
	if len(transport.CACerts) != 0 {
		t.Errorf("CACerts = %v, want empty (CACert already set)", transport.CACerts)
	}
	if transport.Cert != "/existing/client.cert" {
		t.Errorf("Cert = %s, want /existing/client.cert", transport.Cert)
	}
	if transport.Key != "/existing/client.key" {
		t.Errorf("Key = %s, want /existing/client.key", transport.Key)
	}
}

func TestCertsDir_ApplyToTransport_Nil(t *testing.T) {
	var cd *CertsDir
	transport := &properties.Transport{}
	cd.ApplyToTransport(transport) // should not panic
}

func TestLoadCertsDirFromPaths_MatchByBasename(t *testing.T) {
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, "example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create two .cert files; only the first (alphabetically) should be used.
	certA := filepath.Join(hostDir, "aaa.cert")
	certB := filepath.Join(hostDir, "bbb.cert")
	keyA := filepath.Join(hostDir, "aaa.key")
	keyB := filepath.Join(hostDir, "bbb.key")
	for _, p := range []string{certA, certB, keyA, keyB} {
		if err := os.WriteFile(p, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cd, err := LoadCertsDirFromPaths("example.com", []string{tmpDir})
	if err != nil {
		t.Fatalf("LoadCertsDirFromPaths() error: %v", err)
	}
	if cd == nil {
		t.Fatal("LoadCertsDirFromPaths() returned nil")
	}
	if cd.ClientCert != certA {
		t.Errorf("ClientCert = %s, want %s", cd.ClientCert, certA)
	}
	if cd.ClientKey != keyA {
		t.Errorf("ClientKey = %s, want %s", cd.ClientKey, keyA)
	}
}
