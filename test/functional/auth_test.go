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
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestBasicAuthPushPull(t *testing.T) {
	ctx := context.Background()
	repo := newAuthRepo(t, uniqueRepoName(t), "testuser", "testpass")

	// Push a blob.
	content, desc := generateContent(t, 64)
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Authenticated push failed: %v", err)
	}

	// Push a manifest.
	tag := "auth-test"
	manifestDesc := packAndPush(t, ctx, repo, tag, []ocispec.Descriptor{desc})

	// Pull the manifest.
	resolvedDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Authenticated resolve failed: %v", err)
	}
	if resolvedDesc.Digest != manifestDesc.Digest {
		t.Fatalf("Digest mismatch")
	}
}

func TestUnauthenticatedPushFails(t *testing.T) {
	ctx := context.Background()

	// Create a repo without credentials pointing to the auth endpoint.
	repo := newAuthRepo(t, uniqueRepoName(t), "", "")

	content, desc := generateContent(t, 64)
	err := repo.Push(ctx, desc, bytes.NewReader(content))
	if err == nil {
		t.Fatal("Expected unauthenticated push to fail, but it succeeded")
	}
}

func TestWrongCredentialsFail(t *testing.T) {
	ctx := context.Background()

	// Create a repo with wrong credentials.
	repo := newAuthRepo(t, uniqueRepoName(t), "testuser", "wrongpassword")

	content, desc := generateContent(t, 64)
	err := repo.Push(ctx, desc, bytes.NewReader(content))
	if err == nil {
		t.Fatal("Expected push with wrong credentials to fail, but it succeeded")
	}
}
