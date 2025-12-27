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

package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/internal/cas"
	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestNew(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// create source and push content
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}

	// create cache
	cache := cas.NewMemory()

	// wrap source with cache
	cachedTarget := New(source, cache)

	// verify it returns a ReadOnlyTarget
	if _, ok := cachedTarget.(oras.ReadOnlyTarget); !ok {
		t.Error("New() did not return oras.ReadOnlyTarget")
	}
}

func TestTarget_Fetch(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// create source and push content
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}

	// create cache
	cache := cas.NewMemory()

	// wrap source with cache
	cachedTarget := New(source, cache)

	// first fetch - should cache the content
	rc, err := cachedTarget.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Fetch().Read() error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Fetch() = %v, want %v", got, content)
	}

	// verify content is now in cache
	exists, err := cache.Exists(ctx, desc)
	if err != nil {
		t.Fatal("cache.Exists() error =", err)
	}
	if !exists {
		t.Error("content was not cached")
	}

	// second fetch - should come from cache
	rc, err = cachedTarget.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("second Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("second Fetch().Read() error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("second Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("second Fetch() = %v, want %v", got, content)
	}
}

func TestTarget_FetchCached(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// create source and push content
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}

	// create cache and pre-populate it
	cache := cas.NewMemory()
	if err := cache.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to cache:", err)
	}

	// wrap source with cache
	cachedTarget := New(source, cache)

	// fetch should come from cache without hitting source
	rc, err := cachedTarget.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Fetch().Read() error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Fetch() = %v, want %v", got, content)
	}
}

func TestTarget_Exists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// create source and push content
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}

	// create empty cache
	cache := cas.NewMemory()

	// wrap source with cache
	cachedTarget := New(source, cache)

	// content should exist (from source)
	exists, err := cachedTarget.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Exists() error =", err)
	}
	if !exists {
		t.Error("Exists() = false, want true")
	}

	// content not in source but in cache
	content2 := []byte("cached only")
	desc2 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content2),
		Size:      int64(len(content2)),
	}
	if err := cache.Push(ctx, desc2, bytes.NewReader(content2)); err != nil {
		t.Fatal("failed to push content to cache:", err)
	}

	exists, err = cachedTarget.Exists(ctx, desc2)
	if err != nil {
		t.Fatal("Exists() for cached content error =", err)
	}
	if !exists {
		t.Error("Exists() for cached content = false, want true")
	}

	// content not in source or cache
	content3 := []byte("nonexistent")
	desc3 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content3),
		Size:      int64(len(content3)),
	}

	exists, err = cachedTarget.Exists(ctx, desc3)
	if err != nil {
		t.Fatal("Exists() for nonexistent content error =", err)
	}
	if exists {
		t.Error("Exists() for nonexistent content = true, want false")
	}
}

func TestTarget_Resolve(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "latest"

	// create source and push content with tag
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}
	if err := source.Tag(ctx, desc, ref); err != nil {
		t.Fatal("failed to tag content:", err)
	}

	// create empty cache
	cache := cas.NewMemory()

	// wrap source with cache
	cachedTarget := New(source, cache)

	// resolve should work (delegates to source)
	gotDesc, err := cachedTarget.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Resolve() = %v, want %v", gotDesc, desc)
	}
}

func TestReferenceTarget_FetchReference(t *testing.T) {
	// setup test data
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes([]byte("{}")),
			Size:      2,
		},
		Layers: []ocispec.Descriptor{},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// setup mock registry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/manifests/latest":
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Header().Set("Content-Length", "0")
			} else if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Write(manifestBytes)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal("failed to parse test server URL:", err)
	}

	// create remote repository
	repo, err := remote.NewRepository(u.Host + "/test")
	if err != nil {
		t.Fatal("failed to create repository:", err)
	}
	repo.PlainHTTP = true

	// create cache
	cache := cas.NewMemory()
	ctx := context.Background()

	// wrap repository with cache
	cachedTarget := New(repo, cache)

	// verify it implements ReferenceFetcher
	refFetcher, ok := cachedTarget.(registry.ReferenceFetcher)
	if !ok {
		t.Fatal("cached target does not implement registry.ReferenceFetcher")
	}

	// first fetch - should cache the content
	gotDesc, rc, err := refFetcher.FetchReference(ctx, "latest")
	if err != nil {
		t.Fatal("FetchReference() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("FetchReference().Read() error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("FetchReference().Close() error =", err)
	}
	if gotDesc.Digest != manifestDesc.Digest {
		t.Errorf("FetchReference() descriptor digest = %v, want %v", gotDesc.Digest, manifestDesc.Digest)
	}
	if !bytes.Equal(got, manifestBytes) {
		t.Errorf("FetchReference() content = %v, want %v", got, manifestBytes)
	}

	// verify content is now in cache
	exists, err := cache.Exists(ctx, manifestDesc)
	if err != nil {
		t.Fatal("cache.Exists() error =", err)
	}
	if !exists {
		t.Error("content was not cached")
	}

	// second FetchReference - should still fetch from source but content comes from cache
	gotDesc, rc, err = refFetcher.FetchReference(ctx, "latest")
	if err != nil {
		t.Fatal("second FetchReference() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("second FetchReference().Read() error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("second FetchReference().Close() error =", err)
	}
	if gotDesc.Digest != manifestDesc.Digest {
		t.Errorf("second FetchReference() descriptor digest = %v, want %v", gotDesc.Digest, manifestDesc.Digest)
	}
	if !bytes.Equal(got, manifestBytes) {
		t.Errorf("second FetchReference() content = %v, want %v", got, manifestBytes)
	}
}

func TestNew_WithReferenceFetcher(t *testing.T) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes([]byte("{}")),
			Size:      2,
		},
		Layers: []ocispec.Descriptor{},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// setup mock registry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/manifests/latest":
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Header().Set("Content-Length", "0")
			} else if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Write(manifestBytes)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal("failed to parse test server URL:", err)
	}

	// create remote repository (implements ReferenceFetcher)
	repo, err := remote.NewRepository(u.Host + "/test")
	if err != nil {
		t.Fatal("failed to create repository:", err)
	}
	repo.PlainHTTP = true

	// create cache
	cache := cas.NewMemory()

	// wrap repository with cache
	cachedTarget := New(repo, cache)

	// verify it returns a referenceTarget (implements ReferenceFetcher)
	if _, ok := cachedTarget.(registry.ReferenceFetcher); !ok {
		t.Error("New() with ReferenceFetcher source did not return registry.ReferenceFetcher")
	}
}

func TestNew_WithoutReferenceFetcher(t *testing.T) {
	// memory.Store does not implement ReferenceFetcher
	source := memory.New()

	// create cache
	cache := cas.NewMemory()

	// wrap source with cache
	cachedTarget := New(source, cache)

	// verify it does NOT return a referenceTarget
	if _, ok := cachedTarget.(registry.ReferenceFetcher); ok {
		t.Error("New() with non-ReferenceFetcher source returned registry.ReferenceFetcher")
	}
}

// errorStorage is a mock storage that returns errors for testing
type errorStorage struct {
	fetchErr  error
	pushErr   error
	existsErr error
	exists    bool
}

func (e *errorStorage) Fetch(_ context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	if e.fetchErr != nil {
		return nil, e.fetchErr
	}
	return io.NopCloser(bytes.NewReader([]byte("cached"))), nil
}

func (e *errorStorage) Push(_ context.Context, _ ocispec.Descriptor, _ io.Reader) error {
	return e.pushErr
}

func (e *errorStorage) Exists(_ context.Context, _ ocispec.Descriptor) (bool, error) {
	if e.existsErr != nil {
		return false, e.existsErr
	}
	return e.exists, nil
}

// errorReadCloser is a reader that returns an error on Close
type errorReadCloser struct {
	io.Reader
	closeErr error
}

func (e *errorReadCloser) Close() error {
	return e.closeErr
}

// errorSource is a mock source that returns errors for testing
type errorSource struct {
	fetchErr   error
	resolveErr error
	existsErr  error
}

func (e *errorSource) Fetch(_ context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, e.fetchErr
}

func (e *errorSource) Resolve(_ context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, e.resolveErr
}

func (e *errorSource) Exists(_ context.Context, _ ocispec.Descriptor) (bool, error) {
	return false, e.existsErr
}

func TestTarget_Fetch_SourceError(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	expectedErr := errors.New("source fetch error")
	source := &errorSource{fetchErr: expectedErr}
	cache := cas.NewMemory()

	cachedTarget := New(source, cache)
	ctx := context.Background()

	_, err := cachedTarget.Fetch(ctx, desc)
	if err == nil {
		t.Error("Fetch() expected error, got nil")
	}
	if err != expectedErr {
		t.Errorf("Fetch() error = %v, want %v", err, expectedErr)
	}
}

func TestReferenceTarget_FetchReference_Error(t *testing.T) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes([]byte("{}")),
			Size:      2,
		},
		Layers: []ocispec.Descriptor{},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// setup mock registry that returns error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal("failed to parse test server URL:", err)
	}

	repo, err := remote.NewRepository(u.Host + "/test")
	if err != nil {
		t.Fatal("failed to create repository:", err)
	}
	repo.PlainHTTP = true

	cache := cas.NewMemory()
	ctx := context.Background()

	cachedTarget := New(repo, cache)
	refFetcher := cachedTarget.(registry.ReferenceFetcher)

	_, _, err = refFetcher.FetchReference(ctx, "latest")
	if err == nil {
		t.Error("FetchReference() expected error, got nil")
	}

	// Test with cache.Exists error
	errCache := &errorStorage{existsErr: errors.New("exists error")}

	// setup working mock registry for this test
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/manifests/latest":
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Header().Set("Content-Length", "0")
			} else if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Write(manifestBytes)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts2.Close()

	u2, err := url.Parse(ts2.URL)
	if err != nil {
		t.Fatal("failed to parse test server URL:", err)
	}

	repo2, err := remote.NewRepository(u2.Host + "/test")
	if err != nil {
		t.Fatal("failed to create repository:", err)
	}
	repo2.PlainHTTP = true

	cachedTarget2 := New(repo2, errCache)
	refFetcher2 := cachedTarget2.(registry.ReferenceFetcher)

	_, _, err = refFetcher2.FetchReference(ctx, "latest")
	if err == nil {
		t.Error("FetchReference() with cache.Exists error expected error, got nil")
	}
}

func TestTarget_Fetch_CachePushError(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	// create source and push content
	source := memory.New()
	ctx := context.Background()
	if err := source.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("failed to push content to source:", err)
	}

	// cache that fails on push
	errCache := &errorStorage{
		fetchErr: errors.New("not in cache"),
		pushErr:  errors.New("push error"),
	}

	cachedTarget := New(source, errCache)

	// fetch - cache push will fail
	rc, err := cachedTarget.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Fetch() error =", err)
	}

	// Read all content - the push error causes pr.CloseWithError which
	// propagates the error through the pipe to the TeeReader
	_, err = io.ReadAll(rc)
	if err == nil {
		t.Error("Fetch().Read() expected error from failed push, got nil")
	}

	// Close should also fail due to the push error
	_ = rc.Close()
}

func TestTarget_Fetch_ReaderCloseError(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	closeErr := errors.New("close error")

	// create a source that returns a reader that errors on close
	source := &mockSourceWithCloseError{
		content:  content,
		closeErr: closeErr,
	}

	cache := cas.NewMemory()
	cachedTarget := New(source, cache)
	ctx := context.Background()

	// fetch
	rc, err := cachedTarget.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Fetch() error =", err)
	}

	// Read all content
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Fetch().Read() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Fetch() = %v, want %v", got, content)
	}

	// Close should return the reader close error
	err = rc.Close()
	if err == nil {
		t.Error("Fetch().Close() expected error, got nil")
	}
	if err != closeErr {
		t.Errorf("Fetch().Close() error = %v, want %v", err, closeErr)
	}
}

// mockSourceWithCloseError is a source that returns readers that error on close
type mockSourceWithCloseError struct {
	content  []byte
	closeErr error
}

func (m *mockSourceWithCloseError) Fetch(_ context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	return &errorReadCloser{
		Reader:   bytes.NewReader(m.content),
		closeErr: m.closeErr,
	}, nil
}

func (m *mockSourceWithCloseError) Resolve(_ context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errors.New("not implemented")
}

func (m *mockSourceWithCloseError) Exists(_ context.Context, _ ocispec.Descriptor) (bool, error) {
	return true, nil
}

func TestReferenceTarget_FetchReference_CacheExistsButFetchFails(t *testing.T) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes([]byte("{}")),
			Size:      2,
		},
		Layers: []ocispec.Descriptor{},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// setup mock registry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/manifests/latest":
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Header().Set("Content-Length", "0")
			} else if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
				w.Write(manifestBytes)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal("failed to parse test server URL:", err)
	}

	repo, err := remote.NewRepository(u.Host + "/test")
	if err != nil {
		t.Fatal("failed to create repository:", err)
	}
	repo.PlainHTTP = true

	// Cache that says content exists but fails to fetch
	errCache := &errorStorage{
		exists:   true,
		fetchErr: errors.New("cache fetch error"),
	}

	ctx := context.Background()
	cachedTarget := New(repo, errCache)
	refFetcher := cachedTarget.(registry.ReferenceFetcher)

	_, _, err = refFetcher.FetchReference(ctx, "latest")
	if err == nil {
		t.Error("FetchReference() with cache fetch error expected error, got nil")
	}
}
