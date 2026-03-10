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

//go:build k8sfunctional

package functional

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

// newRepo creates a remote.Repository pointing at registryEndpoint/<name> with PlainHTTP=true.
func newRepo(t *testing.T, name string) *remote.Repository {
	t.Helper()
	ref := fmt.Sprintf("%s/%s", registryEndpoint, name)
	repo, err := remote.NewRepository(ref)
	if err != nil {
		t.Fatalf("Failed to create repository %s: %v", ref, err)
	}
	repo.Registry.PlainHTTP = true
	return repo
}

// newAuthRepo creates a remote.Repository pointing at authRegistryEndpoint with basic auth credentials.
func newAuthRepo(t *testing.T, name, username, password string) *remote.Repository {
	t.Helper()
	ref := fmt.Sprintf("%s/%s", authRegistryEndpoint, name)
	repo, err := remote.NewRepository(ref)
	if err != nil {
		t.Fatalf("Failed to create auth repository %s: %v", ref, err)
	}
	repo.Registry.PlainHTTP = true
	repo.Registry.Client = &auth.Client{
		CredentialFunc: credentials.StaticCredentialFunc(authRegistryEndpoint, credentials.Credential{
			Username: username,
			Password: password,
		}),
	}
	return repo
}

// generateContent generates random bytes of the given size, returns (content, descriptor).
func generateContent(t *testing.T, size int) ([]byte, ocispec.Descriptor) {
	t.Helper()
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("Failed to generate random content: %v", err)
	}
	dgst := digest.FromBytes(content)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    dgst,
		Size:      int64(size),
	}
	return content, desc
}

// pushBlob pushes a blob to the repository and returns its descriptor.
func pushBlob(t *testing.T, ctx context.Context, repo *remote.Repository, content []byte) ocispec.Descriptor {
	t.Helper()
	dgst := digest.FromBytes(content)
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    dgst,
		Size:      int64(len(content)),
	}
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Failed to push blob: %v", err)
	}
	return desc
}

// packAndPush creates a simple OCI manifest with the given layers and pushes it with the tag.
func packAndPush(t *testing.T, ctx context.Context, repo *remote.Repository, tag string, layers []ocispec.Descriptor) ocispec.Descriptor {
	t.Helper()

	// Push a config blob.
	configBytes := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: "application/vnd.oci.image.config.v1+json",
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}
	if err := repo.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil {
		t.Fatalf("Failed to push config: %v", err)
	}

	if layers == nil {
		layers = []ocispec.Descriptor{}
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    layers,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Failed to marshal manifest: %v", err)
	}

	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	if err := repo.PushReference(ctx, manifestDesc, bytes.NewReader(manifestBytes), tag); err != nil {
		t.Fatalf("Failed to push manifest with tag %s: %v", tag, err)
	}

	return manifestDesc
}

// buildCredHelper compiles the docker-credential-oras-test binary into a
// temporary directory, adds that directory to PATH for the duration of t, and
// returns a Store backed by the helper together with the path of the credential
// store file the helper uses.
//
// Callers must set ORAS_TEST_CRED_STORE in the environment before the helper
// binary is invoked (the returned storePath can be used for this).
func buildCredHelper(t *testing.T) (store credentials.Store, storePath string) {
	t.Helper()

	// Build the helper binary. Go tests set the working directory to the
	// package directory (test/functional/), so the source is at:
	abs, err := filepath.Abs(filepath.Join("testdata", "docker-credential-oras-test"))
	if err != nil {
		t.Fatalf("resolving helper source path: %v", err)
	}

	binDir := t.TempDir()
	helperBin := filepath.Join(binDir, "docker-credential-oras-test")
	cmd := exec.Command("go", "build", "-o", helperBin, abs)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building credential helper: %v", err)
	}

	// Prepend binDir to PATH so NewNativeStore("oras-test") finds the binary.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath)

	// Create an empty credential store file and expose its path via env.
	credFile, err := os.CreateTemp(t.TempDir(), "creds-*.json")
	if err != nil {
		t.Fatalf("creating credential store file: %v", err)
	}
	if _, err := credFile.WriteString("{}"); err != nil {
		t.Fatalf("initialising credential store file: %v", err)
	}
	credFile.Close()
	storePath = credFile.Name()
	t.Setenv("ORAS_TEST_CRED_STORE", storePath)

	return credentials.NewNativeStore("oras-test"), storePath
}

// newCredHelperRepo creates a remote.Repository pointing at authRegistryEndpoint
// whose auth client uses the provided credential store to look up credentials.
func newCredHelperRepo(t *testing.T, name string, store credentials.Store) *remote.Repository {
	t.Helper()
	ref := fmt.Sprintf("%s/%s", authRegistryEndpoint, name)
	repo, err := remote.NewRepository(ref)
	if err != nil {
		t.Fatalf("Failed to create credential-helper repository %s: %v", ref, err)
	}
	repo.Registry.PlainHTTP = true
	repo.Registry.Client = &auth.Client{
		CredentialFunc: store.Get,
	}
	return repo
}

// uniqueRepoName returns a unique repository name based on the test name.
func uniqueRepoName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "-")
	// Add a random suffix to avoid collisions in parallel runs.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("Failed to generate random suffix: %v", err)
	}
	return fmt.Sprintf("%s-%x", name, suffix)
}
