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
	"context"
	"fmt"
	"testing"

	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// The tests in this file pin how signedBy applies to a multi-platform image.
// A signature covers one manifest digest, so signing an index does not vouch
// for the platform manifests it lists: every manifest that is fetched is
// checked against its own signature. This matches containers/image, where
// signing a manifest list signs each instance as well as the list.

// TestSignatureMultiArch_AllManifestsSigned signs an index and both of its
// platform manifests, and verifies that selecting a platform and copying the
// whole index both succeed through a signedBy policy.
func TestSignatureMultiArch_AllManifestsSigned(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "multi-arch"

	indexDesc, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, newRepository(t, repoName), tag)

	entity := newGPGEntity(t, "Multi-Arch Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, indexDesc, tag, entity)
	signImage(t, ctx, store, scope, amd64Desc, tag, entity)
	signImage(t, ctx, store, scope, arm64Desc, tag, entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))

	opts := oras.DefaultResolveOptions
	opts.TargetPlatform = &platformLinuxARM64
	desc, err := oras.Resolve(ctx, repo, tag, opts)
	if err != nil {
		t.Fatalf("Resolve with target platform error = %v", err)
	}
	if desc.Digest != arm64Desc.Digest {
		t.Fatalf("resolved digest = %s, want the arm64 manifest %s", desc.Digest, arm64Desc.Digest)
	}

	copyOpts := oras.CopyOptions{}
	copyOpts.WithTargetPlatform(&platformLinuxARM64)
	dst := memory.New()
	copied, err := oras.Copy(ctx, repo, tag, dst, tag, copyOpts)
	if err != nil {
		t.Fatalf("Copy with target platform error = %v", err)
	}
	if copied.Digest != arm64Desc.Digest {
		t.Fatalf("copied digest = %s, want the arm64 manifest %s", copied.Digest, arm64Desc.Digest)
	}

	dst = memory.New()
	copied, err = oras.Copy(ctx, repo, tag, dst, tag, oras.DefaultCopyOptions)
	if err != nil {
		t.Fatalf("Copy of the whole index error = %v", err)
	}
	if copied.Digest != indexDesc.Digest {
		t.Fatalf("copied digest = %s, want the index %s", copied.Digest, indexDesc.Digest)
	}
}

// TestSignatureMultiArch_OnlyIndexSigned signs the index but not the platform
// manifests. The tag resolves, but fetching a platform manifest is denied, so
// a platform-selecting copy fails rather than pulling unsigned content.
func TestSignatureMultiArch_OnlyIndexSigned(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "multi-arch"

	indexDesc, amd64Desc, _ := pushMultiPlatformIndex(t, ctx, newRepository(t, repoName), tag)

	entity := newGPGEntity(t, "Multi-Arch Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, indexDesc, tag, entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))

	got, err := repo.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v, want the signed index to resolve", tag, err)
	}
	if got.Digest != indexDesc.Digest {
		t.Fatalf("Resolve(%q) digest = %s, want the index %s", tag, got.Digest, indexDesc.Digest)
	}

	_, err = repo.Fetch(ctx, amd64Desc)
	requirePolicyDenied(t, err, scope+"@"+amd64Desc.Digest.String())

	copyOpts := oras.CopyOptions{}
	copyOpts.WithTargetPlatform(&platformLinuxAMD64)
	_, err = oras.Copy(ctx, repo, tag, memory.New(), tag, copyOpts)
	requirePolicyDenied(t, err, scope+"@"+amd64Desc.Digest.String())
}

// TestSignatureMultiArch_OnlyPlatformManifestsSigned signs the platform
// manifests but not the index. The tag points at the index, so it is denied
// before any platform is selected.
func TestSignatureMultiArch_OnlyPlatformManifestsSigned(t *testing.T) {
	ctx := context.Background()
	repoName := newRepoName(t)
	scope := fmt.Sprintf("%s/%s", registryHost, repoName)
	tag := "multi-arch"

	_, amd64Desc, arm64Desc := pushMultiPlatformIndex(t, ctx, newRepository(t, repoName), tag)

	entity := newGPGEntity(t, "Multi-Arch Signer")
	lookasideURL := "file://" + t.TempDir()
	store := signature.NewLookasideStore(lookasideURL, lookasideURL)
	signImage(t, ctx, store, scope, amd64Desc, tag, entity)
	signImage(t, ctx, store, scope, arm64Desc, tag, entity)

	repo := newSignedByRepository(t, repoName, store, writeGPGKeyFile(t, entity))

	opts := oras.DefaultResolveOptions
	opts.TargetPlatform = &platformLinuxAMD64
	_, err := oras.Resolve(ctx, repo, tag, opts)
	requirePolicyDenied(t, err, scope+":"+tag)

	// The signed platform manifest is still reachable by digest.
	if _, err := repo.Resolve(ctx, amd64Desc.Digest.String()); err != nil {
		t.Errorf("Resolve(amd64 digest) error = %v, want the signed platform manifest to resolve", err)
	}
}
