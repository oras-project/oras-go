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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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

const (
	// maxSignatures bounds the signature index scan. The enumeration is driven
	// by the store's own responses, so without a ceiling a server that never
	// answers "not found" keeps the loop running and the accumulated slice
	// growing without limit.
	maxSignatures = 32

	// maxSignatureBytes bounds a single signature body. A simple signing
	// payload plus its OpenPGP envelope is on the order of a kilobyte.
	maxSignatureBytes = 4 << 20 // 4 MiB

	// lookasideTimeout bounds a single lookaside HTTP request.
	lookasideTimeout = 30 * time.Second
)

// ErrTooManySignatures is returned when a lookaside store offers more
// signatures for one image than will be read.
var ErrTooManySignatures = fmt.Errorf("lookaside store returned more than %d signatures", maxSignatures)

// NewLookasideStore creates a LookasideStore with explicit read and write URLs.
func NewLookasideStore(readURL, writeURL string) *LookasideStore {
	return &LookasideStore{
		readURL:  strings.TrimRight(readURL, "/"),
		writeURL: strings.TrimRight(writeURL, "/"),
		// Not http.DefaultClient: it has no timeout, and these requests go to
		// a host named by configuration.
		client: &http.Client{Timeout: lookasideTimeout},
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

	for i := 1; i <= maxSignatures; i++ {
		sigURL := fmt.Sprintf("%s/signature-%d", basePath, i)

		data, err := s.fetch(ctx, sigURL)
		if err != nil {
			// Not found means no more signatures.
			if isNotFound(err) {
				return signatures, nil
			}
			return nil, fmt.Errorf("failed to fetch signature %d for %s@%s: %w", i, ref, dgst, err)
		}
		if len(data) == 0 {
			return signatures, nil
		}
		signatures = append(signatures, data)
	}

	// Reached the ceiling without the store saying it was done. Fail loudly
	// rather than silently verifying against an arbitrary prefix.
	return nil, fmt.Errorf("%w for %s@%s", ErrTooManySignatures, ref, dgst)
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
	for ; index <= maxSignatures; index++ {
		sigURL := fmt.Sprintf("%s/signature-%d", basePath, index)
		_, err := s.fetch(ctx, sigURL)
		if err != nil {
			if isNotFound(err) {
				break
			}
			return fmt.Errorf("failed to probe signature index %d: %w", index, err)
		}
	}
	if index > maxSignatures {
		return fmt.Errorf("%w for %s@%s", ErrTooManySignatures, ref, dgst)
	}

	sigURL := fmt.Sprintf("%s/signature-%d", basePath, index)
	return s.store(ctx, sigURL, sig)
}

// signaturePath computes the signature base path for an image reference and digest.
// Format: {baseURL}/{ref}@{algo}={hash}
//
// ref is the full image scope including the registry host, e.g.
// "registry.example.com/namespace/repo". That matches the layout
// containers/image writes, which derives the path from the reference's full
// name — the host is part of the path, not stripped from it.
func signaturePath(baseURL, ref string, dgst digest.Digest) string {
	return fmt.Sprintf("%s/%s@%s=%s", baseURL, ref, dgst.Algorithm(), dgst.Hex())
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
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &errNotFound{err: err}
		}
		return nil, err
	}
	defer f.Close()
	return readLimited(f, path)
}

// readLimited reads at most maxSignatureBytes from r, reporting an error rather
// than truncating if the source has more to give.
func readLimited(r io.Reader, source string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSignatureBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSignatureBytes {
		return nil, fmt.Errorf("signature at %s exceeds the %d byte limit", source, maxSignatureBytes)
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

	return readLimited(resp.Body, rawURL)
}

// storeHTTP stores content via HTTP PUT.
func (s *LookasideStore) storeHTTP(ctx context.Context, rawURL string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(data))
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

func (e *errNotFound) Unwrap() error {
	return e.err
}

// isNotFound returns true if the error indicates a not-found condition.
func isNotFound(err error) bool {
	var nf *errNotFound
	return errors.As(err, &nf)
}
