//go:build functional

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

package functional_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/content/oci"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"golang.org/x/sync/errgroup"
)

var registryHost string

func TestMain(m *testing.M) {
	registryHost = os.Getenv("FUNCTIONAL_TEST_REGISTRY")
	if registryHost == "" {
		registryHost = "localhost:5000"
	}

	ctx := context.Background()
	reg, err := remote.NewRegistry(registryHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create registry client: %v\n", err)
		os.Exit(1)
	}
	reg.PlainHTTP = true

	if err := reg.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping registry at %s: %v\n", registryHost, err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// layerData represents a layer with its media type and content bytes.
type layerData struct {
	MediaType string
	Content   []byte
}

// newRepoName generates a unique repository name based on the test name.
func newRepoName(t *testing.T) string {
	t.Helper()
	sanitized := strings.ReplaceAll(t.Name(), "/", "-")
	sanitized = strings.ReplaceAll(sanitized, " ", "-")
	sanitized = strings.ToLower(sanitized)
	return fmt.Sprintf("functional/%s-%d", sanitized, time.Now().UnixNano())
}

// newRepository creates a new remote.Repository configured for PlainHTTP.
func newRepository(t *testing.T, repoName string) *remote.Repository {
	t.Helper()
	ref := fmt.Sprintf("%s/%s", registryHost, repoName)
	repo, err := remote.NewRepository(ref)
	if err != nil {
		t.Fatalf("failed to create repository %s: %v", ref, err)
	}
	repo.Registry.PlainHTTP = true
	return repo
}

// pushBlob pushes a blob with the given media type and data to the repository
// and returns its descriptor.
func pushBlob(t *testing.T, ctx context.Context, repo *remote.Repository, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc := content.NewDescriptorFromBytes(mediaType, data)
	if err := repo.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to push blob: %v", err)
	}
	return desc
}

// pushManifest builds an OCI manifest with the given layers, pushes the config,
// layers, and manifest to the repository, and tags it. It returns the manifest
// descriptor and raw manifest bytes.
func pushManifest(t *testing.T, ctx context.Context, repo *remote.Repository, tag string, layers []layerData) (ocispec.Descriptor, []byte) {
	t.Helper()

	// Push config
	configData := []byte("{}")
	configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, configData)
	if err := repo.Push(ctx, configDesc, bytes.NewReader(configData)); err != nil {
		t.Fatalf("failed to push config: %v", err)
	}

	// Push layers and collect descriptors
	var layerDescs []ocispec.Descriptor
	for _, l := range layers {
		desc := pushBlob(t, ctx, repo, l.MediaType, l.Content)
		layerDescs = append(layerDescs, desc)
	}
	if layerDescs == nil {
		layerDescs = []ocispec.Descriptor{}
	}

	// Build manifest
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    layerDescs,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBytes)
	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), tag); err != nil {
		t.Fatalf("failed to push manifest with tag %s: %v", tag, err)
	}

	return manifestDesc, manifestBytes
}

// fetchAndVerify fetches the content identified by desc and verifies it matches
// the expected bytes.
func fetchAndVerify(t *testing.T, ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, expected []byte) {
	t.Helper()
	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("failed to fetch content: %v", err)
	}
	defer rc.Close()

	got, err := content.ReadAll(rc, desc)
	if err != nil {
		t.Fatalf("failed to read content: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatalf("content mismatch: got %d bytes, want %d bytes", len(got), len(expected))
	}
}

func TestRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("Ping", func(t *testing.T) {
		reg, err := remote.NewRegistry(registryHost)
		if err != nil {
			t.Fatalf("failed to create registry: %v", err)
		}
		reg.PlainHTTP = true

		if err := reg.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
	})

	t.Run("Repositories", func(t *testing.T) {
		// Create two unique repos by pushing a blob to each.
		repoName1 := newRepoName(t)
		repoName2 := newRepoName(t)

		repo1 := newRepository(t, repoName1)
		repo2 := newRepository(t, repoName2)

		data := []byte("repository-listing-test")
		pushBlob(t, ctx, repo1, "application/octet-stream", data)
		pushBlob(t, ctx, repo2, "application/octet-stream", data)

		reg, err := remote.NewRegistry(registryHost)
		if err != nil {
			t.Fatalf("failed to create registry: %v", err)
		}
		reg.PlainHTTP = true

		var allRepos []string
		if err := reg.Repositories(ctx, "", func(repos []string) error {
			allRepos = append(allRepos, repos...)
			return nil
		}); err != nil {
			t.Fatalf("Repositories failed: %v", err)
		}

		found1 := false
		found2 := false
		for _, r := range allRepos {
			if r == repoName1 {
				found1 = true
			}
			if r == repoName2 {
				found2 = true
			}
		}
		if !found1 {
			t.Errorf("repository %s not found in catalog", repoName1)
		}
		if !found2 {
			t.Errorf("repository %s not found in catalog", repoName2)
		}
	})
}

func TestBlobOperations(t *testing.T) {
	ctx := context.Background()

	t.Run("PushAndFetch", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		data := []byte("hello blob content")
		desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

		fetchAndVerify(t, ctx, repo, desc, data)
	})

	t.Run("Exists", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		data := []byte("exists-test-blob")
		desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

		exists, err := repo.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Fatal("expected blob to exist after push")
		}

		// Check a non-existent digest.
		fakeDesc := ocispec.Descriptor{
			MediaType: "application/octet-stream",
			Digest:    digest.FromBytes([]byte("nonexistent")),
			Size:      11,
		}
		exists, err = repo.Exists(ctx, fakeDesc)
		if err != nil {
			t.Fatalf("Exists for non-existent blob failed: %v", err)
		}
		if exists {
			t.Fatal("expected non-existent blob to return false")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		data := []byte("delete-test-blob")
		desc := pushBlob(t, ctx, repo, "application/octet-stream", data)

		if err := repo.Delete(ctx, desc); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		exists, err := repo.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("Exists after delete failed: %v", err)
		}
		if exists {
			t.Fatal("expected blob to not exist after delete")
		}
	})

	t.Run("PushDuplicate", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		data := []byte("duplicate-push-blob")
		desc := content.NewDescriptorFromBytes("application/octet-stream", data)

		if err := repo.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("first push failed: %v", err)
		}
		// Push the same blob again; should not error.
		if err := repo.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("duplicate push failed: %v", err)
		}
	})
}

func TestManifestOperations(t *testing.T) {
	ctx := context.Background()
	layers := []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("layer-content-1")},
	}

	t.Run("PushAndFetchByTag", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, manifestBytes := pushManifest(t, ctx, repo, "v1", layers)

		desc, rc, err := repo.FetchReference(ctx, "v1")
		if err != nil {
			t.Fatalf("FetchReference by tag failed: %v", err)
		}
		defer rc.Close()

		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}

		got, err := content.ReadAll(rc, desc)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if !bytes.Equal(got, manifestBytes) {
			t.Fatal("manifest content mismatch")
		}
	})

	t.Run("FetchByDigest", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, manifestBytes := pushManifest(t, ctx, repo, "v1", layers)

		desc, rc, err := repo.FetchReference(ctx, manifestDesc.Digest.String())
		if err != nil {
			t.Fatalf("FetchReference by digest failed: %v", err)
		}
		defer rc.Close()

		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}

		got, err := content.ReadAll(rc, desc)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if !bytes.Equal(got, manifestBytes) {
			t.Fatal("manifest content mismatch by digest fetch")
		}
	})

	t.Run("Resolve", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		desc, err := repo.Resolve(ctx, "v1")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if desc.MediaType != ocispec.MediaTypeImageManifest {
			t.Fatalf("unexpected media type: got %s, want %s", desc.MediaType, ocispec.MediaTypeImageManifest)
		}
		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}
		if desc.Size != manifestDesc.Size {
			t.Fatalf("size mismatch: got %d, want %d", desc.Size, manifestDesc.Size)
		}
	})

	t.Run("Tag", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		if err := repo.Tag(ctx, manifestDesc, "latest"); err != nil {
			t.Fatalf("Tag failed: %v", err)
		}

		desc, err := repo.Resolve(ctx, "latest")
		if err != nil {
			t.Fatalf("Resolve latest failed: %v", err)
		}
		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch after tag: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}
	})

	t.Run("Tags", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		if err := repo.Tag(ctx, manifestDesc, "v2"); err != nil {
			t.Fatalf("Tag v2 failed: %v", err)
		}
		if err := repo.Tag(ctx, manifestDesc, "latest"); err != nil {
			t.Fatalf("Tag latest failed: %v", err)
		}

		var allTags []string
		if err := repo.Tags(ctx, "", func(tags []string) error {
			allTags = append(allTags, tags...)
			return nil
		}); err != nil {
			t.Fatalf("Tags failed: %v", err)
		}

		expectedTags := map[string]bool{"v1": false, "v2": false, "latest": false}
		for _, tag := range allTags {
			if _, ok := expectedTags[tag]; ok {
				expectedTags[tag] = true
			}
		}
		for tag, found := range expectedTags {
			if !found {
				t.Errorf("expected tag %s not found in tags list", tag)
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		if err := repo.Delete(ctx, manifestDesc); err != nil {
			t.Fatalf("Delete manifest failed: %v", err)
		}

		exists, err := repo.Exists(ctx, manifestDesc)
		if err != nil {
			t.Fatalf("Exists after delete failed: %v", err)
		}
		if exists {
			t.Fatal("expected manifest to not exist after delete")
		}
	})

	t.Run("Exists", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		exists, err := repo.Exists(ctx, manifestDesc)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Fatal("expected manifest to exist after push")
		}
	})
}

func TestReferrers(t *testing.T) {
	ctx := context.Background()

	t.Run("PushAndList", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		// Push a subject manifest.
		subjectDesc, _ := pushManifest(t, ctx, repo, "subject", []layerData{
			{MediaType: "application/vnd.test.layer.v1", Content: []byte("subject-layer")},
		})

		// Build a referrer manifest with Subject pointing to the subject.
		referrerArtifactType := "application/vnd.test.referrer.v1"
		configData := []byte("{}")
		configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeEmptyJSON, configData)
		if err := repo.Push(ctx, configDesc, bytes.NewReader(configData)); err != nil {
			t.Fatalf("failed to push referrer config: %v", err)
		}

		referrerManifest := ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2,
			},
			MediaType:    ocispec.MediaTypeImageManifest,
			Config:       configDesc,
			Layers:       []ocispec.Descriptor{},
			Subject:      &subjectDesc,
			ArtifactType: referrerArtifactType,
		}
		referrerBytes, err := json.Marshal(referrerManifest)
		if err != nil {
			t.Fatalf("failed to marshal referrer manifest: %v", err)
		}

		referrerDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, referrerBytes)
		// Push the referrer manifest by its digest (no tag needed).
		if err := repo.PushReference(ctx, referrerDesc, bytes.NewReader(referrerBytes), referrerDesc.Digest.String()); err != nil {
			t.Fatalf("failed to push referrer manifest: %v", err)
		}

		// List referrers of the subject.
		var referrers []ocispec.Descriptor
		if err := repo.Referrers(ctx, subjectDesc, "", func(refs []ocispec.Descriptor) error {
			referrers = append(referrers, refs...)
			return nil
		}); err != nil {
			t.Fatalf("Referrers failed: %v", err)
		}

		if len(referrers) == 0 {
			t.Fatal("expected at least one referrer, got none")
		}

		found := false
		for _, r := range referrers {
			if r.Digest == referrerDesc.Digest {
				found = true
				if r.ArtifactType != referrerArtifactType {
					t.Errorf("unexpected artifact type: got %s, want %s", r.ArtifactType, referrerArtifactType)
				}
			}
		}
		if !found {
			t.Error("referrer manifest not found in referrers list")
		}
	})

	t.Run("FilterByArtifactType", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		// Push a subject manifest.
		subjectDesc, _ := pushManifest(t, ctx, repo, "subject", []layerData{
			{MediaType: "application/vnd.test.layer.v1", Content: []byte("subject-layer-filter")},
		})

		artifactTypeA := "application/vnd.test.typeA"
		artifactTypeB := "application/vnd.test.typeB"

		// Helper to push a referrer with a given artifactType.
		pushReferrer := func(artifactType string, uniqueID string) ocispec.Descriptor {
			configData := []byte("{}")
			configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeEmptyJSON, configData)
			// Config is deduped by the registry, ignore already-exists errors.
			_ = repo.Push(ctx, configDesc, bytes.NewReader(configData))

			layerContent := []byte("referrer-layer-" + uniqueID)
			layerDesc := pushBlob(t, ctx, repo, "application/octet-stream", layerContent)

			m := ocispec.Manifest{
				Versioned: specs.Versioned{
					SchemaVersion: 2,
				},
				MediaType:    ocispec.MediaTypeImageManifest,
				Config:       configDesc,
				Layers:       []ocispec.Descriptor{layerDesc},
				Subject:      &subjectDesc,
				ArtifactType: artifactType,
			}
			mb, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("failed to marshal referrer: %v", err)
			}
			desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, mb)
			if err := repo.PushReference(ctx, desc, bytes.NewReader(mb), desc.Digest.String()); err != nil {
				t.Fatalf("failed to push referrer: %v", err)
			}
			return desc
		}

		referrerA := pushReferrer(artifactTypeA, "a")
		_ = pushReferrer(artifactTypeB, "b")

		// Filter by artifactTypeA.
		var filtered []ocispec.Descriptor
		if err := repo.Referrers(ctx, subjectDesc, artifactTypeA, func(refs []ocispec.Descriptor) error {
			filtered = append(filtered, refs...)
			return nil
		}); err != nil {
			t.Fatalf("Referrers with filter failed: %v", err)
		}

		// Depending on registry support for server-side filtering, we may get
		// all referrers or only the filtered ones. Verify at least the correct
		// one is present.
		found := false
		for _, r := range filtered {
			if r.Digest == referrerA.Digest {
				found = true
			}
		}
		if !found {
			t.Error("expected referrer with artifactTypeA in filtered results")
		}
	})
}

func TestCopyMemoryToRemote(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)

	memStore := memory.New()

	// Pack a manifest into memory.
	artifactType := "application/vnd.test.copy.memory-to-remote"
	layerContent := []byte("memory-to-remote-layer")
	layerDesc, err := oras.PushBytes(ctx, memStore, "application/octet-stream", layerContent)
	if err != nil {
		t.Fatalf("PushBytes to memory failed: %v", err)
	}

	packOpts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layerDesc},
	}
	manifestDesc, err := oras.PackManifest(ctx, memStore, oras.PackManifestVersion1_1, artifactType, packOpts)
	if err != nil {
		t.Fatalf("PackManifest failed: %v", err)
	}

	if err := memStore.Tag(ctx, manifestDesc, "v1"); err != nil {
		t.Fatalf("Tag in memory failed: %v", err)
	}

	// Copy from memory to remote.
	desc, err := oras.Copy(ctx, memStore, "v1", repo, "v1", oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy memory to remote failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Fatalf("root digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
	}

	// Verify in remote.
	fetchedDesc, rc, err := repo.FetchReference(ctx, "v1")
	if err != nil {
		t.Fatalf("FetchReference after copy failed: %v", err)
	}
	rc.Close()

	if fetchedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("fetched digest mismatch: got %s, want %s", fetchedDesc.Digest, manifestDesc.Digest)
	}
}

func TestCopyRemoteToMemory(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)

	layers := []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("remote-to-memory-layer")},
	}
	manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

	memStore := memory.New()

	desc, err := oras.Copy(ctx, repo, "v1", memStore, "v1", oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy remote to memory failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Fatalf("root digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
	}

	// Verify in memory store.
	resolvedDesc, err := memStore.Resolve(ctx, "v1")
	if err != nil {
		t.Fatalf("Resolve in memory failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("resolved digest mismatch: got %s, want %s", resolvedDesc.Digest, manifestDesc.Digest)
	}
}

func TestCopyRemoteToRemote(t *testing.T) {
	ctx := context.Background()
	repoNameA := newRepoName(t)
	repoNameB := newRepoName(t)
	repoA := newRepository(t, repoNameA)
	repoB := newRepository(t, repoNameB)

	layers := []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("remote-to-remote-layer")},
	}
	manifestDesc, manifestBytes := pushManifest(t, ctx, repoA, "v1", layers)

	desc, err := oras.Copy(ctx, repoA, "v1", repoB, "v1", oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy remote to remote failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Fatalf("root digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
	}

	// Verify in repoB.
	fetchedDesc, rc, err := repoB.FetchReference(ctx, "v1")
	if err != nil {
		t.Fatalf("FetchReference in repoB failed: %v", err)
	}
	defer rc.Close()

	got, err := content.ReadAll(rc, fetchedDesc)
	if err != nil {
		t.Fatalf("ReadAll from repoB failed: %v", err)
	}
	if !bytes.Equal(got, manifestBytes) {
		t.Fatal("manifest content mismatch in repoB")
	}
}

func TestCopyRemoteToOCILayout(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	repo := newRepository(t, repoName)

	layers := []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("remote-to-oci-layer")},
	}
	manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

	ociStore, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create OCI store: %v", err)
	}

	desc, err := oras.Copy(ctx, repo, "v1", ociStore, "v1", oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy remote to OCI layout failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Fatalf("root digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
	}

	// Verify OCI store can resolve the tag.
	resolvedDesc, err := ociStore.Resolve(ctx, "v1")
	if err != nil {
		t.Fatalf("Resolve in OCI store failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("resolved digest mismatch: got %s, want %s", resolvedDesc.Digest, manifestDesc.Digest)
	}
}

func TestExtendedCopyWithReferrers(t *testing.T) {
	ctx := context.Background()
	repoNameA := newRepoName(t)
	repoNameB := newRepoName(t)
	repoA := newRepository(t, repoNameA)
	repoB := newRepository(t, repoNameB)

	// Push a subject manifest to repoA.
	subjectDesc, _ := pushManifest(t, ctx, repoA, "subject", []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("extended-copy-subject-layer")},
	})

	// Push a referrer manifest to repoA.
	referrerArtifactType := "application/vnd.test.referrer.extended"
	configData := []byte("{}")
	configDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeEmptyJSON, configData)
	_ = repoA.Push(ctx, configDesc, bytes.NewReader(configData))

	referrerManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{},
		Subject:      &subjectDesc,
		ArtifactType: referrerArtifactType,
	}
	referrerBytes, err := json.Marshal(referrerManifest)
	if err != nil {
		t.Fatalf("failed to marshal referrer manifest: %v", err)
	}
	referrerDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, referrerBytes)
	if err := repoA.PushReference(ctx, referrerDesc, bytes.NewReader(referrerBytes), referrerDesc.Digest.String()); err != nil {
		t.Fatalf("failed to push referrer to repoA: %v", err)
	}

	// ExtendedCopy subject from repoA to repoB.
	desc, err := oras.ExtendedCopy(ctx, repoA, "subject", repoB, "subject", oras.DefaultExtendedCopyOptions)
	if err != nil {
		t.Fatalf("ExtendedCopy failed: %v", err)
	}
	if desc.Digest != subjectDesc.Digest {
		t.Fatalf("root digest mismatch: got %s, want %s", desc.Digest, subjectDesc.Digest)
	}

	// Verify the subject exists in repoB.
	exists, err := repoB.Exists(ctx, subjectDesc)
	if err != nil {
		t.Fatalf("Exists for subject in repoB failed: %v", err)
	}
	if !exists {
		t.Fatal("expected subject manifest to exist in repoB")
	}

	// Verify the referrer exists in repoB.
	exists, err = repoB.Exists(ctx, referrerDesc)
	if err != nil {
		t.Fatalf("Exists for referrer in repoB failed: %v", err)
	}
	if !exists {
		t.Fatal("expected referrer manifest to exist in repoB")
	}

	// Verify referrers list in repoB.
	var referrers []ocispec.Descriptor
	if err := repoB.Referrers(ctx, subjectDesc, "", func(refs []ocispec.Descriptor) error {
		referrers = append(referrers, refs...)
		return nil
	}); err != nil {
		t.Fatalf("Referrers in repoB failed: %v", err)
	}

	found := false
	for _, r := range referrers {
		if r.Digest == referrerDesc.Digest {
			found = true
		}
	}
	if !found {
		t.Error("referrer not found in repoB referrers list")
	}
}

func TestPackManifest(t *testing.T) {
	ctx := context.Background()

	t.Run("V1_1_WithLayers", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		// Push a layer blob first.
		layerContent := []byte("pack-manifest-layer-content")
		layerDesc := pushBlob(t, ctx, repo, "application/vnd.test.layer.v1", layerContent)

		artifactType := "application/vnd.test.pack.v1"
		packOpts := oras.PackManifestOptions{
			Layers: []ocispec.Descriptor{layerDesc},
		}

		manifestDesc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
		if err != nil {
			t.Fatalf("PackManifest V1.1 with layers failed: %v", err)
		}

		// Fetch the manifest and unmarshal it to verify structure.
		rc, err := repo.Fetch(ctx, manifestDesc)
		if err != nil {
			t.Fatalf("Fetch packed manifest failed: %v", err)
		}
		defer rc.Close()

		gotBytes, err := content.ReadAll(rc, manifestDesc)
		if err != nil {
			t.Fatalf("ReadAll packed manifest failed: %v", err)
		}

		var m ocispec.Manifest
		if err := json.Unmarshal(gotBytes, &m); err != nil {
			t.Fatalf("failed to unmarshal manifest: %v", err)
		}

		if m.ArtifactType != artifactType {
			t.Errorf("artifactType mismatch: got %s, want %s", m.ArtifactType, artifactType)
		}
		if len(m.Layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(m.Layers))
		}
		if m.Layers[0].Digest != layerDesc.Digest {
			t.Errorf("layer digest mismatch: got %s, want %s", m.Layers[0].Digest, layerDesc.Digest)
		}
	})

	t.Run("V1_1_WithAnnotations", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		artifactType := "application/vnd.test.pack.annotated"
		annotations := map[string]string{
			"com.example.key":              "value123",
			ocispec.AnnotationCreated:       time.Now().UTC().Format(time.RFC3339),
		}
		packOpts := oras.PackManifestOptions{
			ManifestAnnotations: annotations,
		}

		manifestDesc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
		if err != nil {
			t.Fatalf("PackManifest V1.1 with annotations failed: %v", err)
		}

		rc, err := repo.Fetch(ctx, manifestDesc)
		if err != nil {
			t.Fatalf("Fetch annotated manifest failed: %v", err)
		}
		defer rc.Close()

		gotBytes, err := content.ReadAll(rc, manifestDesc)
		if err != nil {
			t.Fatalf("ReadAll annotated manifest failed: %v", err)
		}

		var m ocispec.Manifest
		if err := json.Unmarshal(gotBytes, &m); err != nil {
			t.Fatalf("failed to unmarshal manifest: %v", err)
		}

		if m.Annotations == nil {
			t.Fatal("expected annotations to be present")
		}
		if v, ok := m.Annotations["com.example.key"]; !ok || v != "value123" {
			t.Errorf("annotation mismatch: got %q, want %q", v, "value123")
		}
	})
}

func TestHighLevelAPIs(t *testing.T) {
	ctx := context.Background()
	layers := []layerData{
		{MediaType: "application/vnd.test.layer.v1", Content: []byte("high-level-layer")},
	}

	t.Run("Tag", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "v1", layers)

		tagDesc, err := oras.Tag(ctx, repo, manifestDesc.Digest.String(), "newtag")
		if err != nil {
			t.Fatalf("oras.Tag failed: %v", err)
		}
		if tagDesc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", tagDesc.Digest, manifestDesc.Digest)
		}

		resolvedDesc, err := repo.Resolve(ctx, "newtag")
		if err != nil {
			t.Fatalf("Resolve newtag failed: %v", err)
		}
		if resolvedDesc.Digest != manifestDesc.Digest {
			t.Fatalf("resolved digest mismatch: got %s, want %s", resolvedDesc.Digest, manifestDesc.Digest)
		}
	})

	t.Run("TagN", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, _ := pushManifest(t, ctx, repo, "src", layers)

		tagNDesc, err := oras.TagN(ctx, repo, "src", []string{"v1", "v2", "latest"}, oras.DefaultTagNOptions)
		if err != nil {
			t.Fatalf("oras.TagN failed: %v", err)
		}
		if tagNDesc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", tagNDesc.Digest, manifestDesc.Digest)
		}

		for _, tag := range []string{"v1", "v2", "latest"} {
			desc, err := repo.Resolve(ctx, tag)
			if err != nil {
				t.Fatalf("Resolve %s failed: %v", tag, err)
			}
			if desc.Digest != manifestDesc.Digest {
				t.Errorf("Resolve %s: digest mismatch: got %s, want %s", tag, desc.Digest, manifestDesc.Digest)
			}
		}
	})

	t.Run("Fetch", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, manifestBytes := pushManifest(t, ctx, repo, "v1", layers)

		desc, rc, err := oras.Fetch(ctx, repo, "v1", oras.DefaultFetchOptions)
		if err != nil {
			t.Fatalf("oras.Fetch failed: %v", err)
		}
		defer rc.Close()

		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}

		got, err := content.ReadAll(rc, desc)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if !bytes.Equal(got, manifestBytes) {
			t.Fatal("content mismatch from oras.Fetch")
		}
	})

	t.Run("FetchBytes", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		manifestDesc, manifestBytes := pushManifest(t, ctx, repo, "v1", layers)

		desc, gotBytes, err := oras.FetchBytes(ctx, repo, "v1", oras.DefaultFetchBytesOptions)
		if err != nil {
			t.Fatalf("oras.FetchBytes failed: %v", err)
		}

		if desc.Digest != manifestDesc.Digest {
			t.Fatalf("digest mismatch: got %s, want %s", desc.Digest, manifestDesc.Digest)
		}
		if !bytes.Equal(gotBytes, manifestBytes) {
			t.Fatal("bytes mismatch from oras.FetchBytes")
		}
	})

	t.Run("PushBytes", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		data := []byte("push-bytes-content")
		desc, err := oras.PushBytes(ctx, repo, "application/octet-stream", data)
		if err != nil {
			t.Fatalf("oras.PushBytes failed: %v", err)
		}

		exists, err := repo.Exists(ctx, desc)
		if err != nil {
			t.Fatalf("Exists after PushBytes failed: %v", err)
		}
		if !exists {
			t.Fatal("expected blob to exist after PushBytes")
		}
	})
}

func TestMount(t *testing.T) {
	ctx := context.Background()
	repoNameA := newRepoName(t)
	repoNameB := newRepoName(t)
	repoA := newRepository(t, repoNameA)
	repoB := newRepository(t, repoNameB)

	// Push a blob to repoA.
	data := []byte("mount-test-blob-content")
	desc := pushBlob(t, ctx, repoA, "application/octet-stream", data)

	// Mount from repoA to repoB. The fromRepo parameter is just the
	// repository name without the host.
	err := repoB.Mount(ctx, desc, repoNameA, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	// Verify blob exists in repoB.
	exists, err := repoB.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists in repoB after mount failed: %v", err)
	}
	if !exists {
		t.Fatal("expected blob to exist in repoB after mount")
	}
}

func TestConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("ParallelPush", func(t *testing.T) {
		repoName := newRepoName(t)
		repo := newRepository(t, repoName)

		const count = 10
		descs := make([]ocispec.Descriptor, count)
		blobs := make([][]byte, count)

		for i := 0; i < count; i++ {
			blobs[i] = []byte(fmt.Sprintf("parallel-push-blob-%d", i))
			descs[i] = content.NewDescriptorFromBytes("application/octet-stream", blobs[i])
		}

		g, gCtx := errgroup.WithContext(ctx)
		for i := 0; i < count; i++ {
			g.Go(func() error {
				return repo.Push(gCtx, descs[i], bytes.NewReader(blobs[i]))
			})
		}

		if err := g.Wait(); err != nil {
			t.Fatalf("parallel push failed: %v", err)
		}

		// Verify all blobs exist.
		for i := 0; i < count; i++ {
			exists, err := repo.Exists(ctx, descs[i])
			if err != nil {
				t.Fatalf("Exists check for blob %d failed: %v", i, err)
			}
			if !exists {
				t.Errorf("blob %d does not exist after parallel push", i)
			}
		}
	})

	t.Run("ParallelCopy", func(t *testing.T) {
		repoNameA := newRepoName(t)
		repoNameB := newRepoName(t)
		repoA := newRepository(t, repoNameA)
		repoB := newRepository(t, repoNameB)

		const count = 5
		manifestDescs := make([]ocispec.Descriptor, count)
		tags := make([]string, count)

		for i := 0; i < count; i++ {
			tags[i] = fmt.Sprintf("v%d", i)
			layers := []layerData{
				{
					MediaType: "application/vnd.test.layer.v1",
					Content:   []byte(fmt.Sprintf("parallel-copy-layer-%d", i)),
				},
			}
			manifestDescs[i], _ = pushManifest(t, ctx, repoA, tags[i], layers)
		}

		g, gCtx := errgroup.WithContext(ctx)
		for i := 0; i < count; i++ {
			g.Go(func() error {
				_, err := oras.Copy(gCtx, repoA, tags[i], repoB, tags[i], oras.DefaultCopyOptions)
				return err
			})
		}

		if err := g.Wait(); err != nil {
			t.Fatalf("parallel copy failed: %v", err)
		}

		// Verify all manifests in repoB.
		for i := 0; i < count; i++ {
			desc, err := repoB.Resolve(ctx, tags[i])
			if err != nil {
				t.Fatalf("Resolve %s in repoB failed: %v", tags[i], err)
			}
			if desc.Digest != manifestDescs[i].Digest {
				t.Errorf("digest mismatch for %s: got %s, want %s", tags[i], desc.Digest, manifestDescs[i].Digest)
			}
		}
	})
}
