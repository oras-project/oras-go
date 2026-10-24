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

package oras_test

import (
	"context"
	_ "crypto/sha512"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestPackManifestN_Memory(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	layer := content.NewDescriptorFromBytes("test", []byte("hello"))
	if err := s.Push(ctx, layer, bytesReader("hello")); err != nil {
		t.Fatal(err)
	}
	opts := oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{layer},
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	}
	refs := []string{"v1", "latest"}

	desc, err := oras.PackManifestN(ctx, s, oras.PackManifestVersion1_1, "application/vnd.test", refs, opts)
	if err != nil {
		t.Fatalf("PackManifestN() error = %v", err)
	}
	for _, ref := range refs {
		got, err := s.Resolve(ctx, ref)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", ref, err)
		}
		if got.Digest != desc.Digest {
			t.Errorf("Resolve(%q) = %v, want %v", ref, got.Digest, desc.Digest)
		}
	}

	// the result is the same as PackManifest
	want, err := oras.PackManifest(ctx, memory.New(), oras.PackManifestVersion1_1, "application/vnd.test", opts)
	if err != nil {
		t.Fatal(err)
	}
	if desc.Digest != want.Digest {
		t.Errorf("PackManifestN() digest = %v, want %v", desc.Digest, want.Digest)
	}

	// re-packing identical content still applies new tags
	if _, err := oras.PackManifestN(ctx, s, oras.PackManifestVersion1_1, "application/vnd.test", []string{"v2"}, opts); err != nil {
		t.Fatalf("PackManifestN() repack error = %v", err)
	}
	if got, err := s.Resolve(ctx, "v2"); err != nil || got.Digest != desc.Digest {
		t.Errorf("Resolve(v2) = %v, %v; want %v", got.Digest, err, desc.Digest)
	}
}

func TestPackManifestN_NoReferences(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	opts := oras.PackManifestOptions{
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	}
	desc, err := oras.PackManifestN(ctx, s, oras.PackManifestVersion1_0, "application/vnd.test", nil, opts)
	if err != nil {
		t.Fatalf("PackManifestN() error = %v", err)
	}
	if exists, err := s.Exists(ctx, desc); err != nil || !exists {
		t.Errorf("Exists() = %v, %v; want true", exists, err)
	}
}

func TestPackIndexN_Memory(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	opts := oras.PackIndexOptions{
		IndexAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	}
	desc, err := oras.PackIndexN(ctx, s, "application/vnd.test", []string{"a", "b"}, opts)
	if err != nil {
		t.Fatalf("PackIndexN() error = %v", err)
	}
	for _, ref := range []string{"a", "b"} {
		got, err := s.Resolve(ctx, ref)
		if err != nil || got.Digest != desc.Digest {
			t.Errorf("Resolve(%q) = %v, %v; want %v", ref, got.Digest, err, desc.Digest)
		}
	}
}

func TestPackManifestN_Repository(t *testing.T) {
	layer := content.NewDescriptorFromBytes("test", []byte("hello"))
	config := content.NewDescriptorFromBytes("application/vnd.test.config", []byte("{}"))
	opts := oras.PackManifestOptions{
		ConfigDescriptor:    &config,
		Layers:              []ocispec.Descriptor{layer},
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	}

	var mu sync.Mutex
	var puts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPut {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		puts = append(puts, r.URL.Path)
		if r.URL.Path == "/v2/test/manifests/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	t.Run("one request per tag and no push by digest", func(t *testing.T) {
		mu.Lock()
		puts = nil
		mu.Unlock()
		if _, err := oras.PackManifestN(ctx, repo, oras.PackManifestVersion1_1, "application/vnd.test", []string{"v1", "latest"}, opts); err != nil {
			t.Fatalf("PackManifestN() error = %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		slices.Sort(puts)
		want := []string{"/v2/test/manifests/latest", "/v2/test/manifests/v1"}
		if !slices.Equal(puts, want) {
			t.Errorf("PUT requests = %v, want %v", puts, want)
		}
	})

	t.Run("index", func(t *testing.T) {
		mu.Lock()
		puts = nil
		mu.Unlock()
		if _, err := oras.PackIndexN(ctx, repo, "application/vnd.test", []string{"idx"}, oras.PackIndexOptions{
			IndexAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
		}); err != nil {
			t.Fatalf("PackIndexN() error = %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"/v2/test/manifests/idx"}; !slices.Equal(puts, want) {
			t.Errorf("PUT requests = %v, want %v", puts, want)
		}
	})

	t.Run("failed tag is named in the error", func(t *testing.T) {
		_, err := oras.PackManifestN(ctx, repo, oras.PackManifestVersion1_1, "application/vnd.test", []string{"bad"}, opts)
		if err == nil {
			t.Fatal("PackManifestN() error = nil, want error")
		}
		if got := err.Error(); !contains(got, "failed to tag bad") {
			t.Errorf("error %q does not name the failed tag", got)
		}
	})
}

func TestPackManifestN_RepositorySubjectFallback(t *testing.T) {
	subject := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte("subject"))
	referrersPath := "/v2/test/manifests/" + strings.Replace(subject.Digest.String(), ":", "-", 1)
	var mu sync.Mutex
	var manifest ocispec.Manifest
	var index ocispec.Index
	var manifestBytes []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/v1":
			var err error
			manifestBytes, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read manifest: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
				t.Errorf("decode manifest: %v", err)
			}
			// Omitting OCI-Subject makes the client maintain the fallback index.
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == referrersPath:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == referrersPath:
			if err := json.NewDecoder(r.Body).Decode(&index); err != nil {
				t.Errorf("decode referrers index: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	repo, err := remote.NewRepository(strings.TrimPrefix(ts.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	config := ocispec.DescriptorEmptyJSON
	desc, err := oras.PackManifestN(context.Background(), repo, oras.PackManifestVersion1_1, "application/vnd.test", []string{"v1"}, oras.PackManifestOptions{
		Subject:             &subject,
		ConfigDescriptor:    &config,
		Layers:              []ocispec.Descriptor{content.NewDescriptorFromBytes("test", []byte("hello"))},
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if manifest.Subject == nil || manifest.Subject.Digest != subject.Digest {
		t.Errorf("manifest subject = %v, want %v", manifest.Subject, subject)
	}
	if digest.FromBytes(manifestBytes) != desc.Digest {
		t.Error("returned descriptor does not match the pushed manifest")
	}
	if len(index.Manifests) != 1 || index.Manifests[0].Digest != desc.Digest || index.Manifests[0].ArtifactType != "application/vnd.test" {
		t.Errorf("fallback referrers = %v, want the packed manifest", index.Manifests)
	}
}

func TestPackManifestN_RepositorySHA512(t *testing.T) {
	var mu sync.Mutex
	var pushed []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v2/test/manifests/v1" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read manifest: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		pushed = body
		mu.Unlock()
		w.Header().Set("Docker-Content-Digest", digest.SHA512.FromBytes(body).String())
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	repo, err := remote.NewRepository(strings.TrimPrefix(ts.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	config := ocispec.DescriptorEmptyJSON
	desc, err := oras.PackManifestN(context.Background(), repo, oras.PackManifestVersion1_1, "application/vnd.test", []string{"v1"}, oras.PackManifestOptions{
		DigestAlgorithm:     digest.SHA512,
		ConfigDescriptor:    &config,
		Layers:              []ocispec.Descriptor{content.NewDescriptorFromBytes("test", []byte("hello"))},
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2000-01-01T00:00:00Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := digest.SHA512.FromBytes(pushed); desc.Digest != want || desc.Size != int64(len(pushed)) {
		t.Errorf("descriptor = %v, want digest %v and size %d", desc, want, len(pushed))
	}
}

func bytesReader(s string) *strings.Reader { return strings.NewReader(s) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
