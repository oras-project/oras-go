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
