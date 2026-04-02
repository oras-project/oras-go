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

//go:build functional

package functional_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// mirrorRegistryHost returns the mirror registry host from the environment,
// skipping the test if it is not configured. When running via make
// test-functional, setup.sh always sets FUNCTIONAL_TEST_MIRROR_REGISTRY so
// mirror tests run automatically. The skip only fires when tests are run
// by hand without the full setup.
func mirrorRegistryHost(t *testing.T) string {
	t.Helper()
	host := os.Getenv("FUNCTIONAL_TEST_MIRROR_REGISTRY")
	if host == "" {
		t.Skip("skipping mirror test: run 'make test-functional' or set FUNCTIONAL_TEST_MIRROR_REGISTRY")
	}
	return host
}

// mirrorTransport returns a properties.Transport configured for the mirror
// registry. If FUNCTIONAL_TEST_CERTS_DIR is set it discovers the CA
// certificate from the containers-certs.d directory tree placed there by
// setup.sh, exercising the LoadCertsDirFromPaths code path. Otherwise it
// falls back to plain HTTP (for ad-hoc use without the full setup).
func mirrorTransport(t *testing.T, mirrorHost string) properties.Transport {
	t.Helper()
	certsDir := os.Getenv("FUNCTIONAL_TEST_CERTS_DIR")
	if certsDir == "" {
		return properties.Transport{PlainHTTP: true}
	}

	// Discover the CA cert written by setup.sh into:
	//   $FUNCTIONAL_TEST_CERTS_DIR/<mirrorHost>/ca.crt
	// This validates the containers-certs.d discovery mechanism end-to-end.
	certs, err := config.LoadCertsDirFromPaths(mirrorHost, []string{certsDir})
	if err != nil {
		t.Fatalf("config.LoadCertsDirFromPaths(%q, %q): %v", mirrorHost, certsDir, err)
	}
	if certs == nil || len(certs.CACertPaths) == 0 {
		t.Fatalf("no CA cert found in certs.d at %s/%s", certsDir, mirrorHost)
	}

	var transport properties.Transport
	certs.ApplyToTransport(&transport)
	return transport
}

// newMirrorSetupRepo creates a repository pointing at the mirror registry for
// pushing test content. It configures TLS via the certs.d directory so the
// same certificate path exercised by newMirroredRepository is used.
func newMirrorSetupRepo(t *testing.T, mirrorHost, repoName string) *remote.Repository {
	t.Helper()
	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   mirrorHost,
			Repository: repoName,
		},
		Transport: mirrorTransport(t, mirrorHost),
	}
	builder := remote.NewClientBuilder()
	repo, err := remote.NewRepositoryWithProperties(props, builder)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties (mirror setup, %s/%s): %v", mirrorHost, repoName, err)
	}
	return repo
}

// newMirroredRepository creates a repository pointing at registryHost/repoName
// with a single mirror at mirrorHost. The mirror transport is configured via
// the containers-certs.d directory discovered by mirrorTransport, validating
// that the certs.d integration works end-to-end.
func newMirroredRepository(t *testing.T, repoName, mirrorHost, pullPolicy string) *remote.Repository {
	t.Helper()
	props := &properties.Registry{
		Reference: properties.Reference{
			Registry:   registryHost,
			Repository: repoName,
		},
		Transport: properties.Transport{
			PlainHTTP: true,
		},
		Mirrors: []properties.Mirror{
			{
				Location:       mirrorHost,
				Transport:      mirrorTransport(t, mirrorHost),
				PullFromMirror: pullPolicy,
			},
		},
	}
	builder := remote.NewClientBuilder()
	repo, err := remote.NewRepositoryWithProperties(props, builder)
	if err != nil {
		t.Fatalf("NewRepositoryWithProperties (primary-with-mirror, %s): %v", repoName, err)
	}
	return repo
}

// TestMirror_PullByTag_FallsBackToMirror verifies that a tag resolve falls
// back to the TLS mirror when the primary registry has no content.
func TestMirror_PullByTag_FallsBackToMirror(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	// Push a tagged manifest to the mirror registry.
	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror content for tag pull test")
	manifestDesc, _ := pushManifest(t, ctx, mirrorRepo, "latest", []layerData{
		{MediaType: "application/octet-stream", Content: data},
	})

	// Primary has no content. Resolve via primary-with-mirror should fall
	// back to the TLS mirror and return the correct manifest descriptor.
	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorAll)

	desc, err := primaryWithMirror.Resolve(ctx, "latest")
	if err != nil {
		t.Fatalf("Resolve via TLS mirror fallback failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Errorf("resolved digest = %v, want %v", desc.Digest, manifestDesc.Digest)
	}
}

// TestMirror_PullByDigest_FallsBackToMirror verifies that a blob fetch falls
// back to the TLS mirror when the primary registry has no content, and that
// the content returned matches what was pushed.
func TestMirror_PullByDigest_FallsBackToMirror(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror blob for digest pull test")
	desc := pushBlob(t, ctx, mirrorRepo, "application/octet-stream", data)

	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorAll)

	rc, err := primaryWithMirror.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("Fetch via TLS mirror fallback failed: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("fetched content mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

// TestMirror_DigestOnly_SkipsTagPull verifies that a PullFromMirrorDigestOnly
// mirror is bypassed for tag-based resolves so the primary (which is empty) is
// tried directly and the call fails.
func TestMirror_DigestOnly_SkipsTagPull(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror content for digest-only policy test")
	pushManifest(t, ctx, mirrorRepo, "v1.0", []layerData{
		{MediaType: "application/octet-stream", Content: data},
	})

	// Mirror is digest-only: tag resolves bypass it and go to the primary.
	// Primary is empty so the resolve must fail.
	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorDigestOnly)

	_, err := primaryWithMirror.Resolve(ctx, "v1.0")
	if err == nil {
		t.Fatal("tag Resolve should fail when mirror is digest-only and primary is empty")
	}
}

// TestMirror_DigestOnly_DigestPullUsesMirror verifies that a
// PullFromMirrorDigestOnly mirror is used for digest-based fetches.
func TestMirror_DigestOnly_DigestPullUsesMirror(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror blob for digest-only pull test")
	desc := pushBlob(t, ctx, mirrorRepo, "application/octet-stream", data)

	// Mirror is digest-only: digest fetches should use the mirror.
	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorDigestOnly)

	rc, err := primaryWithMirror.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("digest Fetch via digest-only TLS mirror failed: %v", err)
	}
	rc.Close()
}

// TestMirror_TagOnly_SkipsDigestPull verifies that a PullFromMirrorTagOnly
// mirror is bypassed for digest-based fetches so the primary (which is empty)
// is tried directly and the call fails.
func TestMirror_TagOnly_SkipsDigestPull(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror blob for tag-only policy test")
	desc := pushBlob(t, ctx, mirrorRepo, "application/octet-stream", data)

	// Mirror is tag-only: digest fetches bypass it and go to the primary.
	// Primary is empty so the fetch must fail.
	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorTagOnly)

	_, err := primaryWithMirror.Fetch(ctx, desc)
	if err == nil {
		t.Fatal("digest Fetch should fail when mirror is tag-only and primary is empty")
	}
}

// TestMirror_TagOnly_TagPullUsesMirror verifies that a PullFromMirrorTagOnly
// mirror is used for tag-based resolves.
func TestMirror_TagOnly_TagPullUsesMirror(t *testing.T) {
	mirrorHost := mirrorRegistryHost(t)
	ctx := context.Background()
	repoName := newRepoName(t)

	mirrorRepo := newMirrorSetupRepo(t, mirrorHost, repoName)
	data := []byte("mirror content for tag-only pull test")
	manifestDesc, _ := pushManifest(t, ctx, mirrorRepo, "stable", []layerData{
		{MediaType: "application/octet-stream", Content: data},
	})

	// Mirror is tag-only: tag resolves should use the mirror.
	primaryWithMirror := newMirroredRepository(t, repoName, mirrorHost, remote.PullFromMirrorTagOnly)

	desc, err := primaryWithMirror.Resolve(ctx, "stable")
	if err != nil {
		t.Fatalf("tag Resolve via tag-only TLS mirror failed: %v", err)
	}
	if desc.Digest != manifestDesc.Digest {
		t.Errorf("resolved digest = %v, want %v", desc.Digest, manifestDesc.Digest)
	}
}
