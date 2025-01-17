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

package oci

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/docker"
)

func TestReadOnlyStorage_Exists(t *testing.T) {
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("", blob)
	fsys := fstest.MapFS{
		strings.Join([]string{"blobs", dgst.Algorithm().String(), dgst.Encoded()}, "/"): {},
	}
	s := NewStorageFromFS(fsys)
	ctx := context.Background()

	// test sha256
	got, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if want := true; got != want {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", got, want)
	}

	// test not found
	blob = []byte("whaterver")
	desc = content.NewDescriptorFromBytes("", blob)
	got, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if want := false; got != want {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", got, want)
	}

	// test invalid digest
	desc = ocispec.Descriptor{Digest: "not a digest"}
	_, err = s.Exists(ctx, desc)
	if err == nil {
		t.Fatalf("ReadOnlyStorage.Exists() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStorage_Fetch(t *testing.T) {
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("", blob)
	fsys := fstest.MapFS{
		strings.Join([]string{"blobs", dgst.Algorithm().String(), dgst.Encoded()}, "/"): {
			Data: blob,
		},
	}

	s := NewStorageFromFS(fsys)
	ctx := context.Background()

	// test fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("ReadOnlyStorage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("ReadOnlyStorage.Fetch() = %v, want %v", got, blob)
	}

	// test not found
	anotherBlob := []byte("whatever")
	desc = content.NewDescriptorFromBytes("", anotherBlob)
	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("ReadOnlyStorage.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test invalid digest
	desc = ocispec.Descriptor{Digest: "not a digest"}
	_, err = s.Fetch(ctx, desc)
	if err == nil {
		t.Fatalf("ReadOnlyStorage.Fetch() error = %v, wantErr %v", err, true)
	}
}

func TestReadOnlyStorage_DirFS(t *testing.T) {
	tempDir := t.TempDir()
	blob := []byte("test")
	dgst := digest.FromBytes(blob)
	desc := content.NewDescriptorFromBytes("test", blob)
	// write blob to disk
	path := filepath.Join(tempDir, "blobs", dgst.Algorithm().String(), dgst.Encoded())
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}
	if err := os.WriteFile(path, blob, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s := NewStorageFromFS(os.DirFS(tempDir))
	ctx := context.Background()

	// test Exists
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", exists, true)
	}

	// test Fetch
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("ReadOnlyStorage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("ReadOnlyStorage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("ReadOnlyStorage.Fetch() = %v, want %v", got, blob)
	}
}

/*
=== Contents of testdata/hello-world.tar ===

blobs/
	blobs/sha256/
		blobs/sha256/2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54
		blobs/sha256/f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4
		blobs/sha256/faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af
		blobs/sha256/feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412
index.json
manifest.json
oci-layout

=== Contents of testdata/hello-world-prefixed-path.tar ===

./
./blobs/
	./blobs/sha256/
		./blobs/sha256/2db29710123e3e53a794f2694094b9b4338aa9ee5c40b930cb8063a1be392c54
		./blobs/sha256/f54a58bc1aac5ea1a25d796ae155dc228b3f0e11d046ae276b39c4bf2f13d8c4
		./blobs/sha256/faa03e786c97f07ef34423fccceeec2398ec8a5759259f94d99078f264e9d7af
		./blobs/sha256/feb5d9fea6a5e9606aa995e879d862b825965ba48de054caab5ef356dc6b3412
./index.json
./manifest.json
./oci-layout

*/

func TestReadOnlyStorage_TarFS(t *testing.T) {
	tarPaths := []string{
		"testdata/hello-world.tar",
		"testdata/hello-world-prefixed-path.tar",
	}
	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			s, err := NewStorageFromTar(tarPath)
			if err != nil {
				t.Fatal("NewStorageFromTar() error =", err)
			}
			ctx := context.Background()

			// test data in testdata/hello-world.tar and testdata/hello-world-prefixed-path.tar
			blob := []byte(`{"architecture":"amd64","config":{"Hostname":"","Domainname":"","User":"","AttachStdin":false,"AttachStdout":false,"AttachStderr":false,"Tty":false,"OpenStdin":false,"StdinOnce":false,"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],"Cmd":["/hello"],"Image":"sha256:b9935d4e8431fb1a7f0989304ec86b3329a99a25f5efdc7f09f3f8c41434ca6d","Volumes":null,"WorkingDir":"","Entrypoint":null,"OnBuild":null,"Labels":null},"container":"8746661ca3c2f215da94e6d3f7dfdcafaff5ec0b21c9aff6af3dc379a82fbc72","container_config":{"Hostname":"8746661ca3c2","Domainname":"","User":"","AttachStdin":false,"AttachStdout":false,"AttachStderr":false,"Tty":false,"OpenStdin":false,"StdinOnce":false,"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],"Cmd":["/bin/sh","-c","#(nop) ","CMD [\"/hello\"]"],"Image":"sha256:b9935d4e8431fb1a7f0989304ec86b3329a99a25f5efdc7f09f3f8c41434ca6d","Volumes":null,"WorkingDir":"","Entrypoint":null,"OnBuild":null,"Labels":{}},"created":"2021-09-23T23:47:57.442225064Z","docker_version":"20.10.7","history":[{"created":"2021-09-23T23:47:57.098990892Z","created_by":"/bin/sh -c #(nop) COPY file:50563a97010fd7ce1ceebd1fa4f4891ac3decdf428333fb2683696f4358af6c2 in / "},{"created":"2021-09-23T23:47:57.442225064Z","created_by":"/bin/sh -c #(nop)  CMD [\"/hello\"]","empty_layer":true}],"os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:e07ee1baac5fae6a26f30cabfe54a36d3402f96afda318fe0a96cec4ca393359"]}}`)
			desc := content.NewDescriptorFromBytes(docker.MediaTypeManifest, blob)

			// test Exists
			exists, err := s.Exists(ctx, desc)
			if err != nil {
				t.Fatal("ReadOnlyStorage.Exists() error =", err)
			}
			if want := true; exists != want {
				t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", exists, want)
			}

			// test Fetch
			rc, err := s.Fetch(ctx, desc)
			if err != nil {
				t.Fatal("ReadOnlyStorage.Fetch() error =", err)
			}
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal("ReadOnlyStorage.Fetch().Read() error =", err)
			}
			err = rc.Close()
			if err != nil {
				t.Error("ReadOnlyStorage.Fetch().Close() error =", err)
			}
			if !bytes.Equal(got, blob) {
				t.Errorf("ReadOnlyStorage.Fetch() = %v, want %v", got, blob)
			}

			// test Exists against a non-existing content
			blob = []byte("whatever")
			desc = content.NewDescriptorFromBytes("", blob)
			exists, err = s.Exists(ctx, desc)
			if err != nil {
				t.Fatal("ReadOnlyStorage.Exists() error =", err)
			}
			if want := false; exists != want {
				t.Errorf("ReadOnlyStorage.Exists() = %v, want %v", exists, want)
			}

			// test Fetch against a non-existing content
			_, err = s.Fetch(ctx, desc)
			if want := errdef.ErrNotFound; !errors.Is(err, want) {
				t.Errorf("ReadOnlyStorage.Fetch() error = %v, wantErr %v", err, want)
			}
		})
	}
}
