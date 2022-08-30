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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
)

func TestTag_Memory(t *testing.T) {
	target := memory.New()
	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Tag
	err = oras.Tag(ctx, target, ref, "myTestingTag")
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}

	// verify tag
	gotDesc, err := target.Resolve(ctx, "myTestingTag")
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}
}

func TestTag_Repository(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	src := "foobar"
	dst := "myTag"
	var gotIndex []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() || r.URL.Path == "/v2/test/manifests/"+src):
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+dst:
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotIndex = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test with manifest tag
	err = oras.Tag(ctx, repo, src, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest digest
	err = oras.Tag(ctx, repo, indexDesc.Digest.String(), dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest tag@digest
	tagDigestRef := src + "@" + indexDesc.Digest.String()
	err = oras.Tag(ctx, repo, tagDigestRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}

	// test with manifest FQDN
	fqdnRef := repoName + ":" + tagDigestRef
	err = oras.Tag(ctx, repo, fqdnRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}
}

func TestResolve_WithOptions(t *testing.T) {
	target := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Resolve with default resolve options
	resolveOptions := oras.DefaultResolveOptions
	gotDesc, err := oras.Resolve(ctx, target, ref, resolveOptions)

	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}
}

func TestResolve_Memory_WithTargetPlatformOptions(t *testing.T) {
	target := memory.New()
	arc_1 := "test-arc-1"
	os_1 := "test-os-1"
	variant_1 := "v1"
	variant_2 := "v2"

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	appendManifest := func(arc, os, variant string, mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Platform: &ocispec.Platform{
				Architecture: arc,
				OS:           os,
				Variant:      variant,
			},
		})
	}
	generateManifest := func(arc, os, variant string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendManifest(arc, os, variant, ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))            // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))            // Blob 2
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1:3]...) // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Resolve with TargetPlatform
	resolveOptions := oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
	}
	gotDesc, err := oras.Resolve(ctx, target, ref, resolveOptions)

	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with TargetPlatform but there is no matching node
	// Should return not found error
	resolveOptions = oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
			Variant:      variant_2,
		},
	}
	_, err = oras.Resolve(ctx, target, ref, resolveOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.Resolve() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestResolve_Repository_WithTargetPlatformOptions(t *testing.T) {
	arc_1 := "test-arc-1"
	arc_2 := "test-arc-2"
	os_1 := "test-os-1"
	var digest_1 digest.Digest = "sha256:11ec3af9dfeb49c89ef71877ba85249be527e4dda9d1d74d99dc618d1a5fa151"

	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest_1,
		Size:      484,
		Platform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
	}

	index := []byte(`{"manifests":[{
"mediaType":"application/vnd.oci.image.manifest.v1+json",
"digest":"sha256:11ec3af9dfeb49c89ef71877ba85249be527e4dda9d1d74d99dc618d1a5fa151",
"size":484,
"platform":{"architecture":"test-arc-1","os":"test-os-1"}},{
"mediaType":"application/vnd.oci.image.manifest.v1+json",
"digest":"sha256:b955aefa63749f07fad84ab06a45a951368e3ac79799bc44a158fac1bb8ca208",
"size":337,
"platform":{"architecture":"test-arc-2","os":"test-os-2"}}]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	src := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() || r.URL.Path == "/v2/test/manifests/"+src):
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test Resolve with TargetPlatform
	resolveOptions := oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
	}
	gotDesc, err := oras.Resolve(ctx, repo, src, resolveOptions)
	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with TargetPlatform but there is no matching node
	// Should return not found error
	resolveOptions = oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           arc_2,
		},
	}
	_, err = oras.Resolve(ctx, repo, src, resolveOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.Resolve() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestFetch_Memory(t *testing.T) {
	target := memory.New()

	// generate test content
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ref := "foobar"
	ctx := context.Background()

	err := target.Push(ctx, desc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("memory.Push() error = %v", err)
	}

	err = target.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Fetch with valid tag
	gotDesc, rc, err := oras.Fetch(ctx, target, ref)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, desc)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("oras.Fetch() = %v, want %v", got, blob)
	}

	// test FetchManifest with wrong reference
	randomRef := "whatever"
	_, _, err = oras.Fetch(ctx, target, randomRef)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestFetch_Repository(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + manifestDesc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifest); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("remote.NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test Fetch manifest with valid tag
	gotDesc, rc, err := oras.Fetch(ctx, repo.Manifests(), ref)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, manifestDesc)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, manifest) {
		t.Errorf("oras.Fetch() = %v, want %v", got, manifest)
	}

	// test Fetch blob with digest
	gotDesc, rc, err = oras.Fetch(ctx, repo.Blobs(), blobDesc.Digest.String())
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, blobDesc)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("oras.Fetch() = %v, want %v", got, blob)
	}
}

func TestFetchManifest_Memory_WithOptions(t *testing.T) {
	target := memory.New()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	generateManifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	generateManifest(descs[0], descs[1:3]...)                  // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test FetchManifest with default FetchManifest options
	gotDesc, gotBytes, err := oras.FetchManifest(ctx, target, ref, oras.DefaultFetchManifestOptions)
	if err != nil {
		t.Fatal("oras.FetchManifest() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchManifest with wrong reference
	randomRef := "whatever"
	_, _, err = oras.FetchManifest(ctx, target, randomRef, oras.DefaultFetchManifestOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test FetchManifest with size limit: 1
	_, _, err = oras.FetchManifest(ctx, target, ref, oras.FetchManifestOptions{SizeLimit: 1})
	if !errors.Is(err, content.ErrSizeExceedLimit) {
		t.Fatalf("oras.FetchManifest() error = %v, wantErr %v", err, content.ErrSizeExceedLimit)
	}

	// test FetchManifest with size limit: 0
	// should be no limit
	gotDesc, gotBytes, err = oras.FetchManifest(ctx, target, ref, oras.FetchManifestOptions{})
	if err != nil {
		t.Fatal("oras.FetchManifest() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchManifest with size limit: -1
	// should be no limit
	gotDesc, gotBytes, err = oras.FetchManifest(ctx, target, ref, oras.FetchManifestOptions{SizeLimit: -1})
	if err != nil {
		t.Fatal("oras.FetchManifest() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotBytes, blobs[3])
	}
}

func TestFetchManifest_Memory_WithTargetPlatformOptions(t *testing.T) {
	target := memory.New()
	arc_1 := "test-arc-1"
	os_1 := "test-os-1"
	variant_1 := "v1"
	variant_2 := "v2"

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
	}
	appendManifest := func(arc, os, variant string, mediaType string, blob []byte) {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Platform: &ocispec.Platform{
				Architecture: arc,
				OS:           os,
				Variant:      variant,
			},
		})
	}
	generateManifest := func(arc, os, variant string, config ocispec.Descriptor, layers ...ocispec.Descriptor) {
		manifest := ocispec.Manifest{
			Config: config,
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendManifest(arc, os, variant, ocispec.MediaTypeImageManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte(`{"mediaType":"application/vnd.oci.image.config.v1+json",
"created":"2022-07-29T08:13:55Z",
"author":"test author",
"architecture":"test-arc-1",
"os":"test-os-1",
"variant":"v1"}`)) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))            // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))            // Blob 2
	generateManifest(arc_1, os_1, variant_1, descs[0], descs[1:3]...) // Blob 3

	ctx := context.Background()
	for i := range blobs {
		err := target.Push(ctx, descs[i], bytes.NewReader(blobs[i]))
		if err != nil {
			t.Fatalf("failed to push test content to src: %d: %v", i, err)
		}
	}

	manifestDesc := descs[3]
	ref := "foobar"
	err := target.Tag(ctx, manifestDesc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test FetchManifest with TargetPlatform
	opts := oras.FetchManifestOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}
	gotDesc, gotBytes, err := oras.FetchManifest(ctx, target, ref, opts)
	if err != nil {
		t.Fatal("oras.FetchManifest() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchManifest with TargetPlatform but there is no matching node
	// Should return not found error
	opts = oras.FetchManifestOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
				Variant:      variant_2,
			},
		},
	}
	_, _, err = oras.FetchManifest(ctx, target, ref, opts)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestFetchManifest_Repository(t *testing.T) {
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/manifests/" + manifestDesc.Digest.String(),
			"/v2/test/manifests/" + ref:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifest); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("remote.NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test FetchManifest with valid tag
	gotDesc, got, err := oras.FetchManifest(ctx, repo, ref, oras.DefaultFetchManifestOptions)
	if err != nil {
		t.Fatalf("oras.FetchManifest() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(got, manifest) {
		t.Errorf("oras.FetchManifest() = %v, want %v", got, manifest)
	}

	// test FetchManifest with wrong reference
	randomRef := "whatever"
	_, _, err = oras.FetchManifest(ctx, repo, randomRef, oras.DefaultFetchManifestOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestFetchManifest_Repository_WithTargetPlatformOptions(t *testing.T) {
	arc_1 := "test-arc-1"
	arc_2 := "test-arc-2"
	os_1 := "test-os-1"
	os_2 := "test-os-2"
	manifest := []byte(`{"layers":[]}`)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
		Platform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
	}
	manifest2 := []byte("test manifest")
	manifestDesc2 := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest2),
		Size:      int64(len(manifest2)),
		Platform: &ocispec.Platform{
			Architecture: arc_2,
			OS:           os_2,
		},
	}
	indexContent := ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			manifestDesc,
			manifestDesc2,
		},
	}

	index, err := json.Marshal(indexContent)
	if err != nil {
		t.Fatal("failed to marshal index", err)
	}
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	ref := "foobar"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() || r.URL.Path == "/v2/test/manifests/"+ref):
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			if _, err := w.Write(index); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		case r.URL.Path == "/v2/test/manifests/"+manifestDesc.Digest.String():
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, manifestDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			if _, err := w.Write(manifest); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test FetchManifest with TargetPlatform
	opts := oras.FetchManifestOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}
	gotDesc, gotBytes, err := oras.FetchManifest(ctx, repo, ref, opts)
	if err != nil {
		t.Fatal("oras.FetchManifest() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, manifest) {
		t.Errorf("oras.FetchManifest() = %v, want %v", gotBytes, manifest)
	}

	// test FetchManifest with TargetPlatform but there is no matching node
	// Should return not found error
	opts = oras.FetchManifestOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           arc_2,
			},
		},
	}
	_, _, err = oras.FetchManifest(ctx, repo, ref, opts)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchManifest() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}
}

func TestFetchBlob_Memory_WithOptions(t *testing.T) {
	target := memory.New()

	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ref := "foobar"
	ctx := context.Background()

	err := target.Push(ctx, desc, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("memory.Push() error = %v", err)
	}

	err = target.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test FetchBlob with tag
	gotDesc, gotBytes, err := oras.FetchBlob(ctx, target, ref, oras.DefaultFetchBlobOptions)
	if err != nil {
		t.Fatal("oras.FetchBlob() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotDesc, desc)
	}
	if !bytes.Equal(gotBytes, blob) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotBytes, blob)
	}

	// test FetchBlob with wrong reference
	randomRef := "whatever"
	_, _, err = oras.FetchBlob(ctx, target, randomRef, oras.DefaultFetchBlobOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchBlob() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test FetchBlob with size limit: 1
	_, _, err = oras.FetchBlob(ctx, target, ref, oras.FetchBlobOptions{SizeLimit: 1})
	if !errors.Is(err, content.ErrSizeExceedLimit) {
		t.Fatalf("oras.FetchBlob() error = %v, wantErr %v", err, content.ErrSizeExceedLimit)
	}

	// test FetchBlob with size limit: 0
	// should be no limit
	gotDesc, gotBytes, err = oras.FetchBlob(ctx, target, ref, oras.FetchBlobOptions{SizeLimit: 0})
	if err != nil {
		t.Fatal("oras.FetchBlob() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotDesc, desc)
	}
	if !bytes.Equal(gotBytes, blob) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotBytes, blob)
	}

	// test FetchBlob with size limit: -1
	// should be no limit
	gotDesc, gotBytes, err = oras.FetchBlob(ctx, target, ref, oras.FetchBlobOptions{SizeLimit: -1})
	if err != nil {
		t.Fatal("oras.FetchBlob() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotDesc, desc)
	}
	if !bytes.Equal(gotBytes, blob) {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotBytes, blob)
	}
}

func TestFetchBlob_Repository(t *testing.T) {
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v2/test/blobs/" + blobDesc.Digest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			if _, err := w.Write(blob); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repoName := uri.Host + "/test"
	repo, err := remote.NewRepository(repoName)
	if err != nil {
		t.Fatalf("remote.NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	gotDesc, got, err := oras.FetchBlob(ctx, repo, blobDesc.Digest.String(), oras.DefaultFetchBlobOptions)
	if err != nil {
		t.Fatalf("oras.FetchBlob() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("oras.FetchBlob() = %v, want %v", gotDesc, blobDesc)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("oras.FetchBlob() = %v, want %v", got, blob)
	}
}
