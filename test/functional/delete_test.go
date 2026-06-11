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
)

func TestDeleteBlob(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	content, desc := generateContent(t, 128)

	// Push blob.
	if err := repo.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatalf("Push blob failed: %v", err)
	}

	// Verify it exists.
	exists, err := repo.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatal("Blob should exist after push")
	}

	// Delete blob.
	if err := repo.Delete(ctx, desc); err != nil {
		t.Fatalf("Delete blob failed: %v", err)
	}

	// Verify it no longer exists.
	exists, err = repo.Exists(ctx, desc)
	if err != nil {
		t.Fatalf("Exists check after delete failed: %v", err)
	}
	if exists {
		t.Fatal("Blob should not exist after delete")
	}
}

func TestDeleteManifest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, uniqueRepoName(t))

	tag := "delete-me"
	manifestDesc := packAndPush(t, ctx, repo, tag, nil)

	// Verify it resolves.
	_, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve before delete failed: %v", err)
	}

	// Delete manifest.
	if err := repo.Delete(ctx, manifestDesc); err != nil {
		t.Fatalf("Delete manifest failed: %v", err)
	}

	// Verify resolve fails.
	_, err = repo.Resolve(ctx, tag)
	if err == nil {
		t.Fatal("Resolve should fail after manifest delete")
	}
}
