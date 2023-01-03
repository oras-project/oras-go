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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
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
	gotDesc, err := oras.Tag(ctx, target, ref, "myTestingTag")
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Tag() = %v, want %v", gotDesc, manifestDesc)
	}

	// verify tag
	gotDesc, err = target.Resolve(ctx, "myTestingTag")
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
	gotDesc, err := oras.Tag(ctx, repo, src, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Tag() = %v, want %v", gotDesc, indexDesc)
	}

	// test with manifest digest
	gotDesc, err = oras.Tag(ctx, repo, indexDesc.Digest.String(), dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Tag() = %v, want %v", gotDesc, indexDesc)
	}

	// test with manifest tag@digest
	tagDigestRef := src + "@" + indexDesc.Digest.String()
	gotDesc, err = oras.Tag(ctx, repo, tagDigestRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Tag() = %v, want %v", gotDesc, indexDesc)
	}

	// test with manifest FQDN
	fqdnRef := repoName + ":" + tagDigestRef
	gotDesc, err = oras.Tag(ctx, repo, fqdnRef, dst)
	if err != nil {
		t.Fatalf("Repository.TagReference() error = %v", err)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("Repository.TagReference() = %v, want %v", gotIndex, index)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Tag() = %v, want %v", gotDesc, indexDesc)
	}
}

func TestTagN_Memory(t *testing.T) {
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
	srcRef := "foobar"
	err := target.Tag(ctx, manifestDesc, srcRef)
	if err != nil {
		t.Fatalf("oras.Tag(%s) error = %v", srcRef, err)
	}

	// test TagN with empty dstReferences
	_, err = oras.TagN(ctx, target, srcRef, nil, oras.DefaultTagNOptions)
	if !errors.Is(err, errdef.ErrMissingReference) {
		t.Fatalf("oras.TagN() error = %v, wantErr %v", err, errdef.ErrMissingReference)
	}

	// test TagN with single dstReferences
	dstRef := "single"
	gotDesc, err := oras.TagN(ctx, target, srcRef, []string{dstRef}, oras.DefaultTagNOptions)
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, manifestDesc)
	}

	// verify tag
	gotDesc, err = target.Resolve(ctx, dstRef)
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test TagN with single dstReferences and MaxMetadataBytes = 1
	// should not return error
	dstRef = "single2"
	opts := oras.TagNOptions{
		MaxMetadataBytes: 1,
	}
	gotDesc, err = oras.TagN(ctx, target, srcRef, []string{dstRef}, opts)
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, manifestDesc)
	}

	// verify tag
	gotDesc, err = target.Resolve(ctx, dstRef)
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test TagN with multiple references
	dstRefs := []string{"foo", "bar", "baz"}
	gotDesc, err = oras.TagN(ctx, target, srcRef, dstRefs, oras.DefaultTagNOptions)
	if err != nil {
		t.Fatal("oras.TagN() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, manifestDesc)
	}

	// verify multiple references
	for _, ref := range dstRefs {
		gotDesc, err := target.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("target.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, manifestDesc) {
			t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
		}
	}

	// test TagN with multiple references and MaxMetadataBytes = 1
	// should not return error
	dstRefs = []string{"tag1", "tag2", "tag3"}
	opts = oras.TagNOptions{
		MaxMetadataBytes: 1,
	}
	gotDesc, err = oras.TagN(ctx, target, srcRef, dstRefs, opts)
	if err != nil {
		t.Fatal("oras.TagN() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, manifestDesc)
	}

	// verify multiple references
	for _, ref := range dstRefs {
		gotDesc, err := target.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("target.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, manifestDesc) {
			t.Errorf("target.Resolve() = %v, want %v", gotDesc, manifestDesc)
		}
	}
}

func TestTagN_Repository(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	srcRef := "foobar"
	refFoo := "foo"
	refBar := "bar"
	refTag1 := "tag1"
	refTag2 := "tag2"
	dstRefs := []string{refFoo, refBar}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() ||
			r.URL.Path == "/v2/test/manifests/"+srcRef):
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
		case r.Method == http.MethodHead &&
			(r.URL.Path == "/v2/test/manifests/"+refFoo ||
				r.URL.Path == "/v2/test/manifests/"+refBar ||
				r.URL.Path == "/v2/test/manifests/"+refTag1 ||
				r.URL.Path == "/v2/test/manifests/"+refTag2):
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(indexDesc.Size)))
		case r.Method == http.MethodPut &&
			(r.URL.Path == "/v2/test/manifests/"+refFoo ||
				r.URL.Path == "/v2/test/manifests/"+refBar ||
				r.URL.Path == "/v2/test/manifests/"+refTag1 ||
				r.URL.Path == "/v2/test/manifests/"+refTag2):
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
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

	// test TagN with empty dstReferences
	_, err = oras.TagN(ctx, repo, srcRef, nil, oras.DefaultTagNOptions)
	if !errors.Is(err, errdef.ErrMissingReference) {
		t.Fatalf("oras.TagN() error = %v, wantErr %v", err, errdef.ErrMissingReference)
	}

	// test TagN with single dstReferences
	gotDesc, err := oras.TagN(ctx, repo, srcRef, []string{refTag1}, oras.DefaultTagNOptions)
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, indexDesc)
	}

	// verify tag
	gotDesc, err = repo.Resolve(ctx, refTag1)
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, indexDesc)
	}

	// test TagN with single dstReferences and MaxMetadataBytes = 1
	// should not return error
	opts := oras.TagNOptions{
		MaxMetadataBytes: 1,
	}
	gotDesc, err = oras.TagN(ctx, repo, srcRef, []string{refTag2}, opts)
	if err != nil {
		t.Fatalf("failed to retag using oras.Tag with err: %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, indexDesc)
	}

	// verify tag
	gotDesc, err = repo.Resolve(ctx, refTag2)
	if err != nil {
		t.Fatal("target.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("target.Resolve() = %v, want %v", gotDesc, indexDesc)
	}

	// test TagN with multiple references
	gotDesc, err = oras.TagN(ctx, repo, srcRef, dstRefs, oras.DefaultTagNOptions)
	if err != nil {
		t.Fatal("oras.TagN() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.TagN() = %v, want %v", gotDesc, indexDesc)
	}

	// verify multiple references
	for _, ref := range dstRefs {
		gotDesc, err := repo.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("target.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, indexDesc) {
			t.Errorf("target.Resolve() = %v, want %v", gotDesc, indexDesc)
		}
	}

	// test TagN with multiple references and MaxMetadataBytes = 1
	// should return ErrSizeExceedsLimit
	dstRefs = []string{refTag1, refTag2}
	opts = oras.TagNOptions{
		MaxMetadataBytes: 1,
	}
	_, err = oras.TagN(ctx, repo, srcRef, dstRefs, opts)
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.TagN() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}
}

func TestResolve_Memory(t *testing.T) {
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

	// test Resolve with default resolve options
	resolveOptions := oras.DefaultResolveOptions
	gotDesc, err := oras.Resolve(ctx, target, ref, resolveOptions)

	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with empty resolve options
	gotDesc, err = oras.Resolve(ctx, target, ref, oras.ResolveOptions{})
	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with MaxMetadataBytes = 1
	resolveOptions = oras.ResolveOptions{
		MaxMetadataBytes: 1,
	}
	gotDesc, err = oras.Resolve(ctx, target, ref, resolveOptions)
	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with TargetPlatform
	resolveOptions = oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
	}
	gotDesc, err = oras.Resolve(ctx, target, ref, resolveOptions)
	if err != nil {
		t.Fatal("oras.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Resolve() = %v, want %v", gotDesc, manifestDesc)
	}

	// test Resolve with TargetPlatform and MaxMetadataBytes = 1
	resolveOptions = oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
		MaxMetadataBytes: 1,
	}
	gotDesc, err = oras.Resolve(ctx, target, ref, resolveOptions)
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
	expected := fmt.Sprintf("%s: %v: platform in manifest does not match target platform", manifestDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.Resolve() error = %v, wantErr %v", err, expected)
	}
}

func TestResolve_Repository(t *testing.T) {
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

	// test Resolve with TargetPlatform and MaxMetadataBytes = 1
	resolveOptions = oras.ResolveOptions{
		TargetPlatform: &ocispec.Platform{
			Architecture: arc_1,
			OS:           os_1,
		},
		MaxMetadataBytes: 1,
	}
	_, err = oras.Resolve(ctx, repo, src, resolveOptions)
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.Resolve() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
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
	expected := fmt.Sprintf("%s: %v: no matching manifest was found in the manifest list", indexDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.Resolve() error = %v, wantErr %v", err, expected)
	}
}

func TestFetch_Memory(t *testing.T) {
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
	manifestTag := "foobar"
	err := target.Tag(ctx, manifestDesc, manifestTag)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}
	blobRef := "blob"
	err = target.Tag(ctx, descs[2], blobRef)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test Fetch with empty FetchOptions
	gotDesc, rc, err := oras.Fetch(ctx, target, manifestTag, oras.FetchOptions{})
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
	if !bytes.Equal(got, blobs[3]) {
		t.Errorf("oras.Fetch() = %v, want %v", got, blobs[3])
	}

	// test FetchBytes with default FetchBytes options
	gotDesc, rc, err = oras.Fetch(ctx, target, manifestTag, oras.DefaultFetchOptions)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, manifestDesc)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blobs[3]) {
		t.Errorf("oras.Fetch() = %v, want %v", got, blobs[3])
	}

	// test FetchBytes with wrong reference
	randomRef := "whatever"
	_, _, err = oras.Fetch(ctx, target, randomRef, oras.DefaultFetchOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test Fetch with TargetPlatform
	opts := oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}
	gotDesc, rc, err = oras.Fetch(ctx, target, manifestTag, opts)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, manifestDesc)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, blobs[3]) {
		t.Errorf("oras.Fetch() = %v, want %v", got, blobs[3])
	}

	// test Fetch with TargetPlatform but there is no matching node
	// should return not found error
	opts = oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
				Variant:      variant_2,
			},
		},
	}
	_, _, err = oras.Fetch(ctx, target, manifestTag, opts)
	expected := fmt.Sprintf("%s: %v: platform in manifest does not match target platform", manifestDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, expected)
	}

	// test FetchBytes on blob with TargetPlatform
	// should return unsupported error
	opts = oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}
	_, _, err = oras.Fetch(ctx, target, blobRef, opts)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}
}

func TestFetch_Repository(t *testing.T) {
	arc_1 := "test-arc-1"
	arc_2 := "test-arc-2"
	os_1 := "test-os-1"
	os_2 := "test-os-2"
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
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
		case r.URL.Path == "/v2/test/blobs/"+blobDesc.Digest.String():
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
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test Fetch with empty option by valid manifest tag
	gotDesc, rc, err := oras.Fetch(ctx, repo, ref, oras.FetchOptions{})
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, indexDesc)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, index) {
		t.Errorf("oras.Fetch() = %v, want %v", got, index)
	}

	// test Fetch with DefaultFetchOptions by valid manifest tag
	gotDesc, rc, err = oras.Fetch(ctx, repo, ref, oras.DefaultFetchOptions)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, indexDesc)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("oras.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, index) {
		t.Errorf("oras.Fetch() = %v, want %v", got, index)
	}

	// test Fetch with empty option by blob digest
	gotDesc, rc, err = oras.Fetch(ctx, repo.Blobs(), blobDesc.Digest.String(), oras.FetchOptions{})
	if err != nil {
		t.Fatalf("oras.Fetch() error = %v", err)
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

	// test FetchBytes with DefaultFetchBytesOptions by blob digest
	gotDesc, rc, err = oras.Fetch(ctx, repo.Blobs(), blobDesc.Digest.String(), oras.DefaultFetchOptions)
	if err != nil {
		t.Fatalf("oras.Fetch() error = %v", err)
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

	// test FetchBytes with wrong reference
	randomRef := "whatever"
	_, _, err = oras.Fetch(ctx, repo, randomRef, oras.DefaultFetchOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test FetchBytes with TargetPlatform
	opts := oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}

	gotDesc, rc, err = oras.Fetch(ctx, repo, ref, opts)
	if err != nil {
		t.Fatal("oras.Fetch() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.Fetch() = %v, want %v", gotDesc, manifestDesc)
	}
	got, err = io.ReadAll(rc)
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

	// test FetchBytes with TargetPlatform but there is no matching node
	// Should return not found error
	opts = oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           arc_2,
			},
		},
	}
	_, _, err = oras.Fetch(ctx, repo, ref, opts)
	expected := fmt.Sprintf("%s: %v: no matching manifest was found in the manifest list", indexDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, expected)
	}

	// test FetchBytes on blob with TargetPlatform
	// should return unsupported error
	opts = oras.FetchOptions{
		ResolveOptions: oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{
				Architecture: arc_1,
				OS:           os_1,
			},
		},
	}
	_, _, err = oras.Fetch(ctx, repo.Blobs(), blobDesc.Digest.String(), opts)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("oras.Fetch() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}
}

func TestFetchBytes_Memory(t *testing.T) {
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
	manifestTag := "foobar"
	err := target.Tag(ctx, manifestDesc, manifestTag)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}
	blobRef := "blob"
	err = target.Tag(ctx, descs[2], blobRef)
	if err != nil {
		t.Fatal("fail to tag manifestDesc node", err)
	}

	// test FetchBytes with empty FetchBytes options
	gotDesc, gotBytes, err := oras.FetchBytes(ctx, target, manifestTag, oras.FetchBytesOptions{})
	if err != nil {
		t.Fatal("oras.FetchBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchBytes with default FetchBytes options
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, target, manifestTag, oras.DefaultFetchBytesOptions)
	if err != nil {
		t.Fatal("oras.FetchBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchBytes with wrong reference
	randomRef := "whatever"
	_, _, err = oras.FetchBytes(ctx, target, randomRef, oras.DefaultFetchBytesOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test FetchBytes with MaxBytes = 1
	_, _, err = oras.FetchBytes(ctx, target, manifestTag, oras.FetchBytesOptions{MaxBytes: 1})
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}

	// test FetchBytes with TargetPlatform
	opts := oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
	}
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, target, manifestTag, opts)
	if err != nil {
		t.Fatal("oras.FetchBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, blobs[3]) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, blobs[3])
	}

	// test FetchBytes with TargetPlatform and MaxBytes = 1
	// should return size exceed error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
		MaxBytes: 1,
	}
	_, _, err = oras.FetchBytes(ctx, target, manifestTag, opts)
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}

	// test FetchBytes with TargetPlatform but there is no matching node
	// should return not found error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
					Variant:      variant_2,
				},
			},
		},
	}
	_, _, err = oras.FetchBytes(ctx, target, manifestTag, opts)
	expected := fmt.Sprintf("%s: %v: platform in manifest does not match target platform", manifestDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, expected)
	}

	// test FetchBytes on blob with TargetPlatform
	// should return unsupported error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
	}
	_, _, err = oras.FetchBytes(ctx, target, blobRef, opts)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}
}

func TestFetchBytes_Repository(t *testing.T) {
	arc_1 := "test-arc-1"
	arc_2 := "test-arc-2"
	os_1 := "test-os-1"
	os_2 := "test-os-2"
	blob := []byte("hello world")
	blobDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
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
		case r.URL.Path == "/v2/test/blobs/"+blobDesc.Digest.String():
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
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test FetchBytes with empty option by valid manifest tag
	gotDesc, gotBytes, err := oras.FetchBytes(ctx, repo, ref, oras.FetchBytesOptions{})
	if err != nil {
		t.Fatalf("oras.FetchBytes() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, indexDesc)
	}
	if !bytes.Equal(gotBytes, index) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, index)
	}

	// test FetchBytes with DefaultFetchBytesOptions by valid manifest tag
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, repo, ref, oras.DefaultFetchBytesOptions)
	if err != nil {
		t.Fatalf("oras.FetchBytes() error = %v", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, indexDesc)
	}
	if !bytes.Equal(gotBytes, index) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, index)
	}

	// test FetchBytes with empty option by blob digest
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, repo.Blobs(), blobDesc.Digest.String(), oras.FetchBytesOptions{})
	if err != nil {
		t.Fatalf("oras.FetchBytes() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, blobDesc)
	}
	if !bytes.Equal(gotBytes, blob) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, blob)
	}

	// test FetchBytes with DefaultFetchBytesOptions by blob digest
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, repo.Blobs(), blobDesc.Digest.String(), oras.DefaultFetchBytesOptions)
	if err != nil {
		t.Fatalf("oras.FetchBytes() error = %v", err)
	}
	if gotDesc.Digest != blobDesc.Digest || gotDesc.Size != blobDesc.Size {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, blobDesc)
	}
	if !bytes.Equal(gotBytes, blob) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, blob)
	}

	// test FetchBytes with MaxBytes = 1
	_, _, err = oras.FetchBytes(ctx, repo, ref, oras.FetchBytesOptions{MaxBytes: 1})
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}

	// test FetchBytes with wrong reference
	randomRef := "whatever"
	_, _, err = oras.FetchBytes(ctx, repo, randomRef, oras.DefaultFetchBytesOptions)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrNotFound)
	}

	// test FetchBytes with TargetPlatform
	opts := oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
	}
	gotDesc, gotBytes, err = oras.FetchBytes(ctx, repo, ref, opts)
	if err != nil {
		t.Fatal("oras.FetchBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, manifestDesc) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotDesc, manifestDesc)
	}
	if !bytes.Equal(gotBytes, manifest) {
		t.Errorf("oras.FetchBytes() = %v, want %v", gotBytes, manifest)
	}

	// test FetchBytes with TargetPlatform and MaxBytes = 1
	// should return size exceed error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
		MaxBytes: 1,
	}
	_, _, err = oras.FetchBytes(ctx, repo, ref, opts)
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}

	// test FetchBytes with TargetPlatform but there is no matching node
	// Should return not found error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           arc_2,
				},
			},
		},
	}
	_, _, err = oras.FetchBytes(ctx, repo, ref, opts)
	expected := fmt.Sprintf("%s: %v: no matching manifest was found in the manifest list", indexDesc.Digest, errdef.ErrNotFound)
	if err.Error() != expected {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, expected)
	}

	// test FetchBytes on blob with TargetPlatform
	// should return unsupported error
	opts = oras.FetchBytesOptions{
		FetchOptions: oras.FetchOptions{
			ResolveOptions: oras.ResolveOptions{
				TargetPlatform: &ocispec.Platform{
					Architecture: arc_1,
					OS:           os_1,
				},
			},
		},
	}
	_, _, err = oras.FetchBytes(ctx, repo.Blobs(), blobDesc.Digest.String(), opts)
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("oras.FetchBytes() error = %v, wantErr %v", err, errdef.ErrUnsupported)
	}
}

func TestPushBytes_Memory(t *testing.T) {
	s := cas.NewMemory()

	content := []byte("hello world")
	mediaType := "test"
	descTest := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	descOctet := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	descEmpty := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(nil),
		Size:      0,
	}

	ctx := context.Background()
	// test PushBytes with specified media type
	gotDesc, err := oras.PushBytes(ctx, s, mediaType, content)
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descTest) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotDesc, descTest)
	}
	rc, err := s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}

	// test PushBytes with existing content
	_, err = oras.PushBytes(ctx, s, mediaType, content)
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("oras.PushBytes() error = %v, wantErr %v", err, errdef.ErrAlreadyExists)
	}

	// test PushBytes with empty media type
	gotDesc, err = oras.PushBytes(ctx, s, "", content)
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descOctet) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotDesc, descOctet)
	}
	rc, err = s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}

	// test PushBytes with empty content
	gotDesc, err = oras.PushBytes(ctx, s, mediaType, nil)
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descEmpty) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotDesc, descEmpty)
	}
	rc, err = s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, nil) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, nil)
	}
}

func TestPushBytes_Repository(t *testing.T) {
	blob := []byte("hello world")
	blobMediaType := "test"
	blobDesc := ocispec.Descriptor{
		MediaType: blobMediaType,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	var gotBlob []byte
	index := []byte(`{"manifests":[]}`)
	indexMediaType := ocispec.MediaTypeImageIndex
	indexDesc := ocispec.Descriptor{
		MediaType: indexMediaType,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	var gotIndex []byte
	uuid := "4fd53bc9-565d-4527-ab80-3e051ac4880c"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/test/blobs/uploads/":
			w.Header().Set("Location", "/v2/test/blobs/uploads/"+uuid)
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/blobs/uploads/"+uuid:
			if contentType := r.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			if contentDigest := r.URL.Query().Get("digest"); contentDigest != blobDesc.Digest.String() {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			gotBlob = buf.Bytes()
			w.Header().Set("Docker-Content-Digest", blobDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		case r.Method == http.MethodPut && r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String():
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
			return
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		t.Errorf("unexpected access: %s %s", r.Method, r.URL)
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test PushBytes with blob
	gotDesc, err := oras.PushBytes(ctx, repo.Blobs(), blobMediaType, blob)
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, blobDesc) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotDesc, blobDesc)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotBlob, blob)
	}

	// test PushBytes with manifest
	gotDesc, err = oras.PushBytes(ctx, repo, indexMediaType, index)
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if err != nil {
		t.Fatal("oras.PushBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotDesc, indexDesc)
	}
	if !bytes.Equal(gotIndex, index) {
		t.Errorf("oras.PushBytes() = %v, want %v", gotIndex, index)
	}
}

func TestTagBytesN_Memory(t *testing.T) {
	s := memory.New()

	content := []byte("hello world")
	mediaType := "test"
	descTest := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	descOctet := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	descEmpty := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(nil),
		Size:      0,
	}

	ctx := context.Background()
	// test TagBytesN with no reference
	gotDesc, err := oras.TagBytesN(ctx, s, mediaType, content, nil, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descTest) {
		t.Errorf("oras.TagBytes() = %v, want %v", gotDesc, descTest)
	}
	rc, err := s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}

	// test TagBytesN with multiple references
	refs := []string{"foo", "bar", "baz"}
	gotDesc, err = oras.TagBytesN(ctx, s, mediaType, content, refs, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descTest) {
		t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, descTest)
	}
	for _, ref := range refs {
		gotDesc, err := s.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("Memory.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, descTest) {
			t.Fatalf("oras.PushBytes() = %v, want %v", gotDesc, descTest)
		}
	}
	rc, err = s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}

	// test TagBytesN with empty media type and multiple references
	gotDesc, err = oras.TagBytesN(ctx, s, "", content, refs, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descOctet) {
		t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, descOctet)
	}
	for _, ref := range refs {
		gotDesc, err := s.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("Memory.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, descOctet) {
			t.Fatalf("oras.PushBytes() = %v, want %v", gotDesc, descOctet)
		}
	}
	rc, err = s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}

	// test TagBytesN with empty content and multiple references
	gotDesc, err = oras.TagBytesN(ctx, s, mediaType, nil, refs, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descEmpty) {
		t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, descEmpty)
	}
	for _, ref := range refs {
		gotDesc, err := s.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("Memory.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, descEmpty) {
			t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, descEmpty)
		}
	}
	rc, err = s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, nil) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, nil)
	}
}

func TestTagBytesN_Repository(t *testing.T) {
	index := []byte(`{"manifests":[]}`)
	indexMediaType := ocispec.MediaTypeImageIndex
	indexDesc := ocispec.Descriptor{
		MediaType: indexMediaType,
		Digest:    digest.FromBytes(index),
		Size:      int64(len(index)),
	}
	refFoo := "foo"
	refBar := "bar"
	refs := []string{refFoo, refBar}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			(r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() ||
				r.URL.Path == "/v2/test/manifests/"+refFoo ||
				r.URL.Path == "/v2/test/manifests/"+refBar):
			if contentType := r.Header.Get("Content-Type"); contentType != indexDesc.MediaType {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(r.Body); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.WriteHeader(http.StatusCreated)
			return
		case (r.Method == http.MethodHead || r.Method == http.MethodGet) &&
			(r.URL.Path == "/v2/test/manifests/"+indexDesc.Digest.String() ||
				r.URL.Path == "/v2/test/manifests/"+refFoo ||
				r.URL.Path == "/v2/test/manifests/"+refBar):
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, indexDesc.MediaType) {
				t.Errorf("manifest not convertable: %s", accept)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", indexDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", indexDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(int(indexDesc.Size)))
			if r.Method == http.MethodGet {
				if _, err := w.Write(index); err != nil {
					t.Errorf("failed to write %q: %v", r.URL, err)
				}
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}

	repo, err := remote.NewRepository(uri.Host + "/test")
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	repo.PlainHTTP = true
	ctx := context.Background()

	// test TagBytesN with no reference
	gotDesc, err := oras.TagBytesN(ctx, repo, indexMediaType, index, nil, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Errorf("oras.TagBytes() = %v, want %v", gotDesc, indexDesc)
	}
	rc, err := repo.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Repository.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Repository.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Repository.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, index) {
		t.Errorf("Repository.Fetch() = %v, want %v", got, index)
	}

	// test TagBytesN with multiple references
	gotDesc, err = oras.TagBytesN(ctx, repo, indexMediaType, index, refs, oras.DefaultTagBytesNOptions)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, indexDesc) {
		t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, indexDesc)
	}
	for _, ref := range refs {
		gotDesc, err := repo.Resolve(ctx, ref)
		if err != nil {
			t.Fatal("Repository.Resolve() error =", err)
		}
		if !reflect.DeepEqual(gotDesc, indexDesc) {
			t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, indexDesc)
		}
	}
	rc, err = repo.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Repository.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Repository.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Repository.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, index) {
		t.Errorf("Repository.Fetch() = %v, want %v", got, index)
	}
}

func TestTagBytes(t *testing.T) {
	s := memory.New()

	content := []byte("hello world")
	mediaType := "test"
	descTest := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	ref := "foobar"
	// test TagBytes
	gotDesc, err := oras.TagBytes(ctx, s, mediaType, content, ref)
	if err != nil {
		t.Fatal("oras.TagBytes() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descTest) {
		t.Errorf("oras.TagBytes() = %v, want %v", gotDesc, descTest)
	}
	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Memory.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, descTest) {
		t.Fatalf("oras.TagBytes() = %v, want %v", gotDesc, descTest)
	}
	rc, err := s.Fetch(ctx, gotDesc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}
}
