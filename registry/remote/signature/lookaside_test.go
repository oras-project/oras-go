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

package signature

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
)

func TestLookasideStore_FileBackend_GetSignatures(t *testing.T) {
	tmpDir := t.TempDir()

	ref := "registry.example.com/namespace/repo"
	dgst := digest.FromString("test content")

	// Write some test signatures.
	sigDir := filepath.Join(tmpDir, ref+"@"+string(dgst.Algorithm())+"="+dgst.Hex())
	if err := os.MkdirAll(sigDir, 0755); err != nil {
		t.Fatal(err)
	}
	sig1 := []byte("signature-1-data")
	sig2 := []byte("signature-2-data")
	if err := os.WriteFile(filepath.Join(sigDir, "signature-1"), sig1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sigDir, "signature-2"), sig2, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewLookasideStore("file://"+tmpDir, "file://"+tmpDir)
	ctx := context.Background()

	sigs, err := store.GetSignatures(ctx, ref, dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("GetSignatures() returned %d signatures, want 2", len(sigs))
	}
	if !bytes.Equal(sigs[0], sig1) {
		t.Errorf("signature[0] = %s, want %s", sigs[0], sig1)
	}
	if !bytes.Equal(sigs[1], sig2) {
		t.Errorf("signature[1] = %s, want %s", sigs[1], sig2)
	}
}

func TestLookasideStore_FileBackend_GetSignatures_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	store := NewLookasideStore("file://"+tmpDir, "file://"+tmpDir)
	ctx := context.Background()

	sigs, err := store.GetSignatures(ctx, "nonexistent/repo", digest.FromString("none"))
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 0 {
		t.Errorf("GetSignatures() returned %d signatures, want 0", len(sigs))
	}
}

func TestLookasideStore_FileBackend_PutSignature(t *testing.T) {
	tmpDir := t.TempDir()

	store := NewLookasideStore("file://"+tmpDir, "file://"+tmpDir)
	ctx := context.Background()

	ref := "registry.example.com/namespace/repo"
	dgst := digest.FromString("test content")
	sig := []byte("my-signature-data")

	if err := store.PutSignature(ctx, ref, dgst, sig); err != nil {
		t.Fatalf("PutSignature() error: %v", err)
	}

	// Verify it was written.
	sigs, err := store.GetSignatures(ctx, ref, dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("GetSignatures() returned %d signatures, want 1", len(sigs))
	}
	if !bytes.Equal(sigs[0], sig) {
		t.Errorf("signature = %s, want %s", sigs[0], sig)
	}

	// Write another signature.
	sig2 := []byte("another-signature")
	if err := store.PutSignature(ctx, ref, dgst, sig2); err != nil {
		t.Fatalf("PutSignature() second error: %v", err)
	}

	sigs, err = store.GetSignatures(ctx, ref, dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("GetSignatures() returned %d signatures, want 2", len(sigs))
	}
}

func TestLookasideStore_FileBackend_EmptyReadURL(t *testing.T) {
	store := NewLookasideStore("", "file:///tmp/test")
	ctx := context.Background()

	sigs, err := store.GetSignatures(ctx, "test", digest.FromString("test"))
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if sigs != nil {
		t.Errorf("GetSignatures() with empty readURL = %v, want nil", sigs)
	}
}

func TestLookasideStore_FileBackend_EmptyWriteURL(t *testing.T) {
	store := NewLookasideStore("file:///tmp/test", "")
	ctx := context.Background()

	err := store.PutSignature(ctx, "test", digest.FromString("test"), []byte("sig"))
	if err == nil {
		t.Fatal("PutSignature() with empty writeURL should return error")
	}
}

func TestLookasideStore_HTTPBackend_GetSignatures(t *testing.T) {
	var mu sync.Mutex
	signatures := map[string][]byte{
		"/repo@sha256=abcdef/signature-1": []byte("http-sig-1"),
		"/repo@sha256=abcdef/signature-2": []byte("http-sig-2"),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			if data, ok := signatures[r.URL.Path]; ok {
				w.Write(data)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodPut:
			data, _ := os.ReadFile(r.URL.Path)
			if data == nil {
				body := make([]byte, r.ContentLength)
				r.Body.Read(body)
				signatures[r.URL.Path] = body
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	store := NewLookasideStore(ts.URL, ts.URL)
	ctx := context.Background()

	dgst := digest.NewDigestFromHex("sha256", "abcdef")

	sigs, err := store.GetSignatures(ctx, "repo", dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("GetSignatures() returned %d signatures, want 2", len(sigs))
	}
	if !bytes.Equal(sigs[0], []byte("http-sig-1")) {
		t.Errorf("signature[0] = %s, want http-sig-1", sigs[0])
	}
}

func TestLookasideStore_HTTPBackend_PutSignature(t *testing.T) {
	var mu sync.Mutex
	signatures := map[string][]byte{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			if data, ok := signatures[r.URL.Path]; ok {
				w.Write(data)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodPut:
			body, _ := readBody(r)
			signatures[r.URL.Path] = body
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	store := NewLookasideStore(ts.URL, ts.URL)
	ctx := context.Background()

	dgst := digest.NewDigestFromHex("sha256", "abcdef")
	sig := []byte("new-http-signature")

	if err := store.PutSignature(ctx, "repo", dgst, sig); err != nil {
		t.Fatalf("PutSignature() error: %v", err)
	}

	// Verify through GET.
	sigs, err := store.GetSignatures(ctx, "repo", dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("GetSignatures() returned %d signatures, want 1", len(sigs))
	}
	if !bytes.Equal(sigs[0], sig) {
		t.Errorf("signature = %s, want %s", sigs[0], sig)
	}
}

func TestLookasideStore_HTTPBackend_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	store := NewLookasideStore(ts.URL, ts.URL)
	ctx := context.Background()

	dgst := digest.FromString("test")

	_, err := store.GetSignatures(ctx, "repo", dgst)
	if err == nil {
		t.Fatal("GetSignatures() should return error for server error")
	}
}

func TestNewLookasideStoreFromConfig(t *testing.T) {
	cfg := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{
			"registry.example.com": {
				Lookaside:        "https://sigstore.example.com/sigs",
				LookasideStaging: "https://sigstore-staging.example.com/sigs",
			},
		},
	}

	store := NewLookasideStoreFromConfig(cfg, "registry.example.com/repo")
	if store == nil {
		t.Fatal("NewLookasideStoreFromConfig() returned nil")
	}
	if store.readURL != "https://sigstore.example.com/sigs" {
		t.Errorf("readURL = %v, want https://sigstore.example.com/sigs", store.readURL)
	}
	if store.writeURL != "https://sigstore-staging.example.com/sigs" {
		t.Errorf("writeURL = %v, want https://sigstore-staging.example.com/sigs", store.writeURL)
	}
}

func TestNewLookasideStoreFromConfig_NoMatch(t *testing.T) {
	cfg := &config.RegistriesDConfig{
		Docker: map[string]config.RegistriesDDockerConfig{},
	}

	store := NewLookasideStoreFromConfig(cfg, "unknown.example.com")
	if store != nil {
		t.Fatal("NewLookasideStoreFromConfig() should return nil for no match")
	}
}

func TestSignaturePath(t *testing.T) {
	dgst := digest.NewDigestFromHex("sha256", "abc123")
	got := signaturePath("https://sigstore.example.com", "registry.example.com/repo", dgst)
	want := fmt.Sprintf("https://sigstore.example.com/registry.example.com/repo@sha256=abc123")
	if got != want {
		t.Errorf("signaturePath() = %v, want %v", got, want)
	}
}

func TestLookasideStore_SetHTTPClient(t *testing.T) {
	var used bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		used = true
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	store := NewLookasideStore(ts.URL, ts.URL)
	client := &http.Client{}
	store.SetHTTPClient(client)
	if store.client != client {
		t.Fatal("SetHTTPClient() did not set the client")
	}

	if _, err := store.GetSignatures(context.Background(), "repo", digest.FromString("test")); err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if !used {
		t.Error("custom client was not used")
	}
}

func TestLookasideStore_GetSignatures_EmptySignatureFile(t *testing.T) {
	tmpDir := t.TempDir()

	ref := "registry.example.com/namespace/repo"
	dgst := digest.FromString("test content")

	sigDir := filepath.Join(tmpDir, ref+"@"+string(dgst.Algorithm())+"="+dgst.Hex())
	if err := os.MkdirAll(sigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// An empty signature file terminates enumeration.
	if err := os.WriteFile(filepath.Join(sigDir, "signature-1"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewLookasideStore("file://"+tmpDir, "file://"+tmpDir)
	sigs, err := store.GetSignatures(context.Background(), ref, dgst)
	if err != nil {
		t.Fatalf("GetSignatures() error: %v", err)
	}
	if len(sigs) != 0 {
		t.Errorf("GetSignatures() returned %d signatures, want 0", len(sigs))
	}
}

func TestLookasideStore_GetSignatures_UnsupportedScheme(t *testing.T) {
	store := NewLookasideStore("ftp://example.com/sigs", "")
	_, err := store.GetSignatures(context.Background(), "repo", digest.FromString("test"))
	if err == nil {
		t.Fatal("GetSignatures() should return error for unsupported scheme")
	}
}

func TestLookasideStore_PutSignature_ProbeError(t *testing.T) {
	store := NewLookasideStore("", "ftp://example.com/sigs")
	err := store.PutSignature(context.Background(), "repo", digest.FromString("test"), []byte("sig"))
	if err == nil {
		t.Fatal("PutSignature() should return error when probing fails")
	}
}

func TestLookasideStore_FetchAndStore_InvalidURL(t *testing.T) {
	store := NewLookasideStore("", "")
	ctx := context.Background()
	badURL := "http://example.com/\x7f"

	if _, err := store.fetch(ctx, badURL); err == nil {
		t.Error("fetch() should return error for invalid URL")
	}
	if err := store.store(ctx, badURL, []byte("sig")); err == nil {
		t.Error("store() should return error for invalid URL")
	}
}

func TestLookasideStore_Store_UnsupportedScheme(t *testing.T) {
	store := NewLookasideStore("", "")
	if err := store.store(context.Background(), "ftp://example.com/sig", []byte("sig")); err == nil {
		t.Fatal("store() should return error for unsupported scheme")
	}
}

func TestLookasideStore_FetchFile_ReadError(t *testing.T) {
	store := NewLookasideStore("", "")
	// Reading a directory is an error that is not "not found".
	_, err := store.fetchFile(t.TempDir())
	if err == nil {
		t.Fatal("fetchFile() should return error when path is a directory")
	}
	if isNotFound(err) {
		t.Error("fetchFile() error should not be a not-found error")
	}
}

func TestLookasideStore_StoreFile_MkdirError(t *testing.T) {
	tmpDir := t.TempDir()
	// A regular file cannot be a parent directory.
	blocker := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blocker, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewLookasideStore("", "")
	if err := store.storeFile(filepath.Join(blocker, "sub", "signature-1"), []byte("sig")); err == nil {
		t.Fatal("storeFile() should return error when parent cannot be created")
	}
}

func TestLookasideStore_HTTPBackend_RequestError(t *testing.T) {
	store := NewLookasideStore("http://example.com", "http://example.com")

	// A nil context makes request construction fail.
	//nolint:staticcheck // deliberately passing a nil context to exercise the error path
	if _, err := store.fetchHTTP(nil, "http://example.com/signature-1"); err == nil {
		t.Error("fetchHTTP() should return error for nil context")
	}
	//nolint:staticcheck // deliberately passing a nil context to exercise the error path
	if err := store.storeHTTP(nil, "http://example.com/signature-1", []byte("sig")); err == nil {
		t.Error("storeHTTP() should return error for nil context")
	}
}

func TestLookasideStore_HTTPBackend_ConnectionError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close() // no listener from here on

	store := NewLookasideStore(url, url)
	ctx := context.Background()

	if _, err := store.fetchHTTP(ctx, url+"/signature-1"); err == nil {
		t.Error("fetchHTTP() should return error when the server is unreachable")
	}
	if err := store.storeHTTP(ctx, url+"/signature-1", []byte("sig")); err == nil {
		t.Error("storeHTTP() should return error when the server is unreachable")
	}
}

func TestLookasideStore_HTTPBackend_PutSignature_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	store := NewLookasideStore(ts.URL, ts.URL)
	err := store.PutSignature(context.Background(), "repo", digest.FromString("test"), []byte("sig"))
	if err == nil {
		t.Fatal("PutSignature() should return error for a failed PUT")
	}
}

func TestErrNotFound_Error(t *testing.T) {
	err := &errNotFound{err: fmt.Errorf("missing signature")}
	if err.Error() != "missing signature" {
		t.Errorf("Error() = %v, want missing signature", err.Error())
	}
	if !isNotFound(err) {
		t.Error("isNotFound() = false, want true")
	}
	if isNotFound(fmt.Errorf("other")) {
		t.Error("isNotFound() = true for unrelated error, want false")
	}
}

// readBody reads the full request body.
func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	data, err := os.ReadFile(r.URL.Path)
	if err != nil {
		// This is an HTTP request, read from body instead.
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if readErr != nil {
				break
			}
		}
		return body, nil
	}
	return data, nil
}
