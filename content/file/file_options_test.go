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

package file

import (
	"bytes"
	"context"
	_ "crypto/sha512"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/errdef"
)

func TestNewWithOptions_Defaults(t *testing.T) {
	s, err := NewWithOptions(t.TempDir(), StoreOptions{})
	if err != nil {
		t.Fatal("NewWithOptions() error =", err)
	}
	defer s.Close()
	if s.digestAlgorithm != digest.Canonical {
		t.Errorf("digest algorithm = %v, want %v", s.digestAlgorithm, digest.Canonical)
	}

	// the default fallback storage is limited to 4 MiB.
	ctx := context.Background()
	blob := make([]byte, defaultFallbackPushSizeLimit+1)
	desc := content.NewDescriptorFromBytes("test", blob)
	if err := s.Push(ctx, desc, bytes.NewReader(blob)); !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrSizeExceedsLimit)
	}
}

func TestNew_MatchesZeroOptions(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal("New() error =", err)
	}
	defer s.Close()
	if s.digestAlgorithm != digest.Canonical {
		t.Errorf("digest algorithm = %v, want %v", s.digestAlgorithm, digest.Canonical)
	}
}

func TestNewWithOptions_UnsupportedDigestAlgorithm(t *testing.T) {
	for _, alg := range []digest.Algorithm{"md5", "sha1", "blake3", "SHA256"} {
		t.Run(string(alg), func(t *testing.T) {
			_, err := NewWithOptions(t.TempDir(), StoreOptions{DigestAlgorithm: alg})
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Errorf("NewWithOptions() error = %v, want %v", err, errdef.ErrUnsupported)
			}
		})
	}
}

func TestNewWithOptions_InvalidFallbackOptions(t *testing.T) {
	tests := []struct {
		name string
		opts StoreOptions
	}{
		{"both FallbackStorage and FallbackLimit", StoreOptions{FallbackStorage: memory.New(), FallbackLimit: 1}},
		{"negative FallbackLimit", StoreOptions{FallbackLimit: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewWithOptions(t.TempDir(), tt.opts)
			if !errors.Is(err, ErrInvalidStoreOptions) {
				t.Errorf("NewWithOptions() error = %v, want %v", err, ErrInvalidStoreOptions)
			}
		})
	}
}

func TestNewWithOptions_FallbackLimit(t *testing.T) {
	s, err := NewWithOptions(t.TempDir(), StoreOptions{FallbackLimit: 4})
	if err != nil {
		t.Fatal("NewWithOptions() error =", err)
	}
	defer s.Close()
	ctx := context.Background()

	small := []byte("abcd")
	if err := s.Push(ctx, content.NewDescriptorFromBytes("test", small), bytes.NewReader(small)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	big := []byte("abcde")
	if err := s.Push(ctx, content.NewDescriptorFromBytes("test", big), bytes.NewReader(big)); !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrSizeExceedsLimit)
	}
}

func TestNewWithOptions_FallbackStorage_Unlimited(t *testing.T) {
	s, err := NewWithOptions(t.TempDir(), StoreOptions{FallbackStorage: memory.New()})
	if err != nil {
		t.Fatal("NewWithOptions() error =", err)
	}
	defer s.Close()
	ctx := context.Background()

	// larger than the default limit: accepted by an unlimited fallback.
	blob := make([]byte, defaultFallbackPushSizeLimit+1)
	desc := content.NewDescriptorFromBytes("test", blob)
	if err := s.Push(ctx, desc, bytes.NewReader(blob)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Error("Store.Exists() = false, want true")
	}
}

func TestStore_File_Add_DigestAlgorithm(t *testing.T) {
	for _, alg := range []digest.Algorithm{digest.SHA256, digest.SHA384, digest.SHA512} {
		t.Run(string(alg), func(t *testing.T) {
			tempDir := t.TempDir()
			blob := []byte("hello world")
			path := filepath.Join(tempDir, "test.txt")
			if err := os.WriteFile(path, blob, 0444); err != nil {
				t.Fatal("WriteFile() error =", err)
			}

			s, err := NewWithOptions(tempDir, StoreOptions{DigestAlgorithm: alg})
			if err != nil {
				t.Fatal("NewWithOptions() error =", err)
			}
			defer s.Close()
			ctx := context.Background()

			desc, err := s.Add(ctx, "test.txt", "", path)
			if err != nil {
				t.Fatal("Store.Add() error =", err)
			}
			if want := alg.FromBytes(blob); desc.Digest != want {
				t.Errorf("Store.Add() digest = %v, want %v", desc.Digest, want)
			}

			rc, err := s.Fetch(ctx, desc)
			if err != nil {
				t.Fatal("Store.Fetch() error =", err)
			}
			defer rc.Close()
			got, err := content.ReadAll(rc, desc) // verifies the digest
			if err != nil {
				t.Fatal("content.ReadAll() error =", err)
			}
			if !bytes.Equal(got, blob) {
				t.Errorf("Store.Fetch() = %q, want %q", got, blob)
			}
		})
	}
}

func TestStore_Dir_Add_DigestAlgorithm(t *testing.T) {
	for _, alg := range []digest.Algorithm{digest.SHA256, digest.SHA384, digest.SHA512} {
		t.Run(string(alg), func(t *testing.T) {
			tempDir := t.TempDir()
			dirName := "testdir"
			dirPath := filepath.Join(tempDir, dirName)
			if err := os.MkdirAll(dirPath, 0777); err != nil {
				t.Fatal("MkdirAll() error =", err)
			}
			blob := []byte("hello world")
			if err := os.WriteFile(filepath.Join(dirPath, "test.txt"), blob, 0444); err != nil {
				t.Fatal("WriteFile() error =", err)
			}

			src, err := NewWithOptions(tempDir, StoreOptions{DigestAlgorithm: alg})
			if err != nil {
				t.Fatal("NewWithOptions() error =", err)
			}
			defer src.Close()
			ctx := context.Background()

			desc, err := src.Add(ctx, dirName, "", dirPath)
			if err != nil {
				t.Fatal("Store.Add() error =", err)
			}
			if got := desc.Digest.Algorithm(); got != alg {
				t.Errorf("gzip digest algorithm = %v, want %v", got, alg)
			}
			tarDigest, err := digest.Parse(desc.Annotations[AnnotationDigest])
			if err != nil {
				t.Fatalf("invalid %s annotation: %v", AnnotationDigest, err)
			}
			if got := tarDigest.Algorithm(); got != alg {
				t.Errorf("tar digest algorithm = %v, want %v", got, alg)
			}

			// round trip: pack with the same algorithm, copy into a default
			// file store and check the directory is unpacked and verified.
			root, err := oras.PackManifest(ctx, src, oras.PackManifestVersion1_1, "application/vnd.test", oras.PackManifestOptions{
				Layers:          []ocispec.Descriptor{desc},
				DigestAlgorithm: alg,
			})
			if err != nil {
				t.Fatal("oras.PackManifest() error =", err)
			}
			if got := root.Digest.Algorithm(); got != alg {
				t.Errorf("manifest digest algorithm = %v, want %v", got, alg)
			}

			dstDir := t.TempDir()
			dst, err := New(dstDir) // DigestAlgorithm governs Add only, not Push
			if err != nil {
				t.Fatal("New() error =", err)
			}
			defer dst.Close()
			if err := oras.CopyGraph(ctx, src, dst, root, oras.DefaultCopyGraphOptions); err != nil {
				t.Fatal("oras.CopyGraph() error =", err)
			}
			got, err := os.ReadFile(filepath.Join(dstDir, dirName, "test.txt"))
			if err != nil {
				t.Fatal("ReadFile() error =", err)
			}
			if !bytes.Equal(got, blob) {
				t.Errorf("unpacked content = %q, want %q", got, blob)
			}
		})
	}
}

func TestStore_Dir_Push_DigestAlgorithm_TarMismatch(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("MkdirAll() error =", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "test.txt"), []byte("hello world"), 0444); err != nil {
		t.Fatal("WriteFile() error =", err)
	}

	src, err := NewWithOptions(tempDir, StoreOptions{DigestAlgorithm: digest.SHA512})
	if err != nil {
		t.Fatal("NewWithOptions() error =", err)
	}
	defer src.Close()
	ctx := context.Background()
	desc, err := src.Add(ctx, dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}
	rc, err := src.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	gz, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal("ReadAll() error =", err)
	}

	// tamper with the SHA-512 tar digest: extraction must fail.
	desc.Annotations[AnnotationDigest] = digest.SHA512.FromString("tampered").String()
	dst, err := New(t.TempDir())
	if err != nil {
		t.Fatal("New() error =", err)
	}
	defer dst.Close()
	if err := dst.Push(ctx, desc, bytes.NewReader(gz)); err == nil {
		t.Error("Store.Push() error = nil, want digest mismatch")
	}
}
