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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
)

// LookasideStore implements SignatureStore using a lookaside storage backend.
// Signatures are stored at predictable paths derived from the image reference
// and digest.
//
// Path format: {base}/{namespace}@{algo}={hash}/signature-{index}
//
// Supported URL schemes:
//   - file:// — filesystem read/write
//   - http(s):// — HTTP GET (read), PUT (write)
type LookasideStore struct {
	readURL  string
	writeURL string
	client   *http.Client
}

// NewLookasideStore creates a LookasideStore with explicit read and write URLs.
func NewLookasideStore(readURL, writeURL string) *LookasideStore {
	return &LookasideStore{
		readURL:  strings.TrimRight(readURL, "/"),
		writeURL: strings.TrimRight(writeURL, "/"),
		client:   http.DefaultClient,
	}
}

// NewLookasideStoreFromConfig creates a LookasideStore from registries.d
// configuration for the given image scope.
// Returns nil if no lookaside URL is configured for the scope.
func NewLookasideStoreFromConfig(cfg *config.RegistriesDConfig, scope string) *LookasideStore {
	readURL, writeURL := cfg.GetLookasideURLs(scope)
	if readURL == "" {
		return nil
	}
	return NewLookasideStore(readURL, writeURL)
}

// SetHTTPClient sets a custom HTTP client for HTTP(S) lookaside operations.
func (s *LookasideStore) SetHTTPClient(client *http.Client) {
	s.client = client
}

// GetSignatures returns all signatures for the given image reference and digest.
// It enumerates signatures by incrementing the index until no more are found.
func (s *LookasideStore) GetSignatures(ctx context.Context, ref string, dgst digest.Digest) ([][]byte, error) {
	if s.readURL == "" {
		return nil, nil
	}

	basePath := signaturePath(s.readURL, ref, dgst)
	var signatures [][]byte

	for i := 1; ; i++ {
		sigURL := fmt.Sprintf("%s/signature-%d", basePath, i)

		data, err := s.fetch(ctx, sigURL)
		if err != nil {
			// Not found means no more signatures.
			if isNotFound(err) {
				break
			}
			return nil, fmt.Errorf("failed to fetch signature %d for %s@%s: %w", i, ref, dgst, err)
		}
		if len(data) == 0 {
			break
		}
		signatures = append(signatures, data)
	}

	return signatures, nil
}

// PutSignature stores a signature for the given image reference and digest.
// It finds the next available index and writes the signature there.
func (s *LookasideStore) PutSignature(ctx context.Context, ref string, dgst digest.Digest, sig []byte) error {
	if s.writeURL == "" {
		return fmt.Errorf("no write URL configured for lookaside store")
	}

	basePath := signaturePath(s.writeURL, ref, dgst)

	// Find the next available index.
	index := 1
	for ; ; index++ {
		sigURL := fmt.Sprintf("%s/signature-%d", basePath, index)
		_, err := s.fetch(ctx, sigURL)
		if err != nil {
			if isNotFound(err) {
				break
			}
			return fmt.Errorf("failed to probe signature index %d: %w", index, err)
		}
	}

	sigURL := fmt.Sprintf("%s/signature-%d", basePath, index)
	return s.store(ctx, sigURL, sig)
}

// signaturePath computes the signature base path for an image reference and digest.
// Format: {baseURL}/{namespace}@{algo}={hash}
func signaturePath(baseURL, ref string, dgst digest.Digest) string {
	// Extract the namespace part (everything after the registry host).
	// For "registry.example.com/namespace/repo", we want "namespace/repo".
	namespace := ref
	return fmt.Sprintf("%s/%s@%s=%s", baseURL, namespace, dgst.Algorithm(), dgst.Hex())
}

// fetch retrieves content from the given URL.
func (s *LookasideStore) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %s: %w", rawURL, err)
	}

	switch parsed.Scheme {
	case "file", "":
		return s.fetchFile(parsed.Path)
	case "http", "https":
		return s.fetchHTTP(ctx, rawURL)
	default:
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
}

// store writes content to the given URL.
func (s *LookasideStore) store(ctx context.Context, rawURL string, data []byte) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %s: %w", rawURL, err)
	}

	switch parsed.Scheme {
	case "file", "":
		return s.storeFile(parsed.Path, data)
	case "http", "https":
		return s.storeHTTP(ctx, rawURL, data)
	default:
		return fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
}

// fetchFile reads a file from the filesystem.
func (s *LookasideStore) fetchFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &errNotFound{err: err}
		}
		return nil, err
	}
	return data, nil
}

// storeFile writes data to a file, creating parent directories as needed.
func (s *LookasideStore) storeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return os.WriteFile(path, data, 0644)
}

// fetchHTTP fetches content via HTTP GET.
func (s *LookasideStore) fetchHTTP(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &errNotFound{err: fmt.Errorf("HTTP 404: %s", rawURL)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d for %s", resp.StatusCode, rawURL)
	}

	return io.ReadAll(resp.Body)
}

// storeHTTP stores content via HTTP PUT.
func (s *LookasideStore) storeHTTP(ctx context.Context, rawURL string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %d for PUT %s", resp.StatusCode, rawURL)
	}
	return nil
}

// errNotFound indicates that a signature was not found.
type errNotFound struct {
	err error
}

func (e *errNotFound) Error() string {
	return e.err.Error()
}

// isNotFound returns true if the error indicates a not-found condition.
func isNotFound(err error) bool {
	_, ok := err.(*errNotFound)
	return ok
}
