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

package memory

import (
	"bytes"
	"context"
	"crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/cas"
	"github.com/oras-project/oras-go/v3/internal/ioutil"
	"github.com/oras-project/oras-go/v3/internal/resolver"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"golang.org/x/sync/errgroup"
)

func TestStoreInterface(t *testing.T) {
	var store any = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(content.PredecessorFinder); !ok {
		t.Error("&Store{} does not conform content.PredecessorFinder")
	}
}

func TestStoreSuccess(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	internalResolver := s.resolver.(*resolver.Memory)
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
	internalStorage := s.storage.(*cas.Memory)
	if got := len(internalStorage.Map()); got != 1 {
		t.Errorf("storage.Map() = %v, want %v", got, 1)
	}
}

func TestStoreContentNotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreContentAlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStoreContentBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStoreTagNotFound(t *testing.T) {
	ref := "foobar"

	s := New()
	ctx := context.Background()

	_, err := s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreTagUnknownContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Tag(ctx, desc, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreRepeatTag(t *testing.T) {
	generate := func(content []byte) ocispec.Descriptor {
		return ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	// get internal resolver
	internalResolver := s.resolver.(*resolver.Memory)

	// initial tag
	content := []byte("hello world")
	desc := generate(content)
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("foo")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("bar")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}
}

func TestStorePredecessors(t *testing.T) {
	s := New()
	ctx := context.Background()

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
	generateIndex := func(manifests ...ocispec.Descriptor) {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	generateArtifactManifest := func(subject ocispec.Descriptor, blobs ...ocispec.Descriptor) {
		var manifest spec.Artifact
		manifest.Subject = &subject
		manifest.Blobs = append(manifest.Blobs, blobs...)
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		appendBlob(spec.MediaTypeArtifactManifest, manifestJSON)
	}

	appendBlob(ocispec.MediaTypeImageConfig, []byte("config")) // Blob 0
	appendBlob(ocispec.MediaTypeImageLayer, []byte("foo"))     // Blob 1
	appendBlob(ocispec.MediaTypeImageLayer, []byte("bar"))     // Blob 2
	appendBlob(ocispec.MediaTypeImageLayer, []byte("hello"))   // Blob 3
	generateManifest(descs[0], descs[1:3]...)                  // Blob 4
	generateManifest(descs[0], descs[3])                       // Blob 5
	generateManifest(descs[0], descs[1:4]...)                  // Blob 6
	generateIndex(descs[4:6]...)                               // Blob 7
	generateIndex(descs[6])                                    // Blob 8
	generateIndex()                                            // Blob 9
	generateIndex(descs[7:10]...)                              // Blob 10
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_1"))   // Blob 11
	generateArtifactManifest(descs[6], descs[11])              // Blob 12
	appendBlob(ocispec.MediaTypeImageLayer, []byte("sig_2"))   // Blob 13
	generateArtifactManifest(descs[10], descs[13])             // Blob 14

	eg, egCtx := errgroup.WithContext(ctx)
	for i := range blobs {
		eg.Go(func(i int) func() error {
			return func() error {
				err := s.Push(egCtx, descs[i], bytes.NewReader(blobs[i]))
				if err != nil {
					return fmt.Errorf("failed to push test content to src: %d: %v", i, err)
				}
				return nil
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	// verify predecessors
	wants := [][]ocispec.Descriptor{
		descs[4:7],            // Blob 0
		{descs[4], descs[6]},  // Blob 1
		{descs[4], descs[6]},  // Blob 2
		{descs[5], descs[6]},  // Blob 3
		{descs[7]},            // Blob 4
		{descs[7]},            // Blob 5
		{descs[8], descs[12]}, // Blob 6
		{descs[10]},           // Blob 7
		{descs[10]},           // Blob 8
		{descs[10]},           // Blob 9
		{descs[14]},           // Blob 10
		{descs[12]},           // Blob 11
		nil,                   // Blob 12
		{descs[14]},           // Blob 13
		nil,                   // Blob 14
	}
	for i, want := range wants {
		predecessors, err := s.Predecessors(ctx, descs[i])
		if err != nil {
			t.Errorf("Store.Predecessors(%d) error = %v", i, err)
		}
		if !equalDescriptorSet(predecessors, want) {
			t.Errorf("Store.Predecessors(%d) = %v, want %v", i, predecessors, want)
		}
	}
}

func TestStore_BadDigest(t *testing.T) {
	data := []byte("hello world")
	ref := "foobar"

	t.Run("invalid digest", func(t *testing.T) {
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    "invalid-digest",
		}

		s := New()
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, digest.ErrDigestInvalidFormat) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, digest.ErrDigestInvalidFormat)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Exists(ctx, desc); err != nil {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, nil)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})

	t.Run("unsupported digest (sha1)", func(t *testing.T) {
		h := sha1.New()
		h.Write(data)
		desc := ocispec.Descriptor{
			MediaType: "application/test",
			Size:      int64(len(data)),
			Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		}

		s := New()
		ctx := context.Background()
		if err := s.Push(ctx, desc, bytes.NewReader(data)); !errors.Is(err, digest.ErrDigestUnsupported) {
			t.Errorf("Store.Push() error = %v, wantErr %v", err, digest.ErrDigestUnsupported)

		}

		if err := s.Tag(ctx, desc, ref); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Tag() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Exists(ctx, desc); err != nil {
			t.Errorf("Store.Exists() error = %v, wantErr %v", err, nil)
		}

		if _, err := s.Fetch(ctx, desc); !errors.Is(err, errdef.ErrNotFound) {
			t.Errorf("Store.Fetch() error = %v, wantErr %v", err, errdef.ErrNotFound)
		}

		if _, err := s.Predecessors(ctx, desc); err != nil {
			t.Errorf("Store.Predecessors() error = %v, wantErr %v", err, nil)
		}
	})
}

func equalDescriptorSet(actual []ocispec.Descriptor, expected []ocispec.Descriptor) bool {
	if len(actual) != len(expected) {
		return false
	}
	contains := func(node ocispec.Descriptor) bool {
		for _, candidate := range actual {
			if reflect.DeepEqual(candidate, node) {
				return true
			}
		}
		return false
	}
	for _, node := range expected {
		if !contains(node) {
			return false
		}
	}
	return true
}

// TestStore_Fetch_UnwrapsToSeekable guards the contract that an in-memory Store
// returns blob content that, once unwrapped from io.NopCloser, is a seekable
// built-in reader. The remote blob push unwraps the content so net/http sets a
// replayable Request.GetBody, letting the body be resent (on a redirect, an
// HTTP/2 connection recycle, or a retry). If Fetch ever returns content that
// does not unwrap to a seekable built-in, retryable blob push silently breaks
// for this store.
func TestStore_Fetch_UnwrapsToSeekable(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()
	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	defer rc.Close()
	if _, ok := ioutil.UnwrapNopCloser(rc).(io.ReadSeeker); !ok {
		t.Errorf("Store.Fetch() returned %T, which does not unwrap to io.ReadSeeker", rc)
	}
}

var _ content.UnsafePusher = (*Store)(nil)

func TestStorage_UnsafePush(t *testing.T) {
	blob := []byte("foobar")
	fail := errors.New("reader failed")
	tests := []struct {
		name      string
		digest    digest.Digest
		size      int64
		limit     int64
		blob      []byte
		wantErr   error
		noRead    bool
		readError bool
	}{
		{name: "unknown digest and size", size: -1},
		{name: "known digest only", digest: digest.FromBytes(blob), size: -1},
		{name: "known size only", size: int64(len(blob))},
		{name: "known digest and size", digest: digest.FromBytes(blob), size: int64(len(blob))},
		{name: "empty unknown size", size: -1, blob: []byte{}},
		{name: "empty known size", size: 0, blob: []byte{}},
		{name: "empty known descriptor", digest: digest.FromBytes(nil), size: 0, blob: []byte{}},
		{name: "mismatched digest", digest: digest.FromString("wrong"), size: -1, wantErr: content.ErrMismatchedDigest},
		{name: "short content", size: int64(len(blob)) + 1, wantErr: io.ErrUnexpectedEOF},
		{name: "long content", size: int64(len(blob)) - 1, wantErr: content.ErrTrailingData},
		{name: "nonempty with zero size", size: 0, wantErr: content.ErrTrailingData},
		{name: "unknown exceeds limit", size: -1, limit: 8, blob: []byte("1234567890"), wantErr: errdef.ErrSizeExceedsLimit},
		{name: "known exceeds limit", size: 9, limit: 8, wantErr: errdef.ErrSizeExceedsLimit, noRead: true},
		{name: "exact limit unknown", size: -1, limit: 8, blob: []byte("12345678")},
		{name: "exact limit known", size: 8, limit: 8, blob: []byte("12345678")},
		{name: "invalid digest", digest: "invalid", size: -1, wantErr: errdef.ErrInvalidDigest, noRead: true},
		{name: "invalid digest encoding", digest: "sha256:abcd", size: -1, wantErr: errdef.ErrInvalidDigest, noRead: true},
		{name: "unsupported algorithm", digest: digest.Digest("sha1:" + strings.Repeat("a", 40)), size: -1, wantErr: errdef.ErrUnsupported, noRead: true},
		{name: "sha512", digest: digest.SHA512.FromBytes(blob), size: -1},
		{name: "reader failure", size: -1, wantErr: fail, readError: true},
		{name: "max limit unknown", size: -1, limit: math.MaxInt64},
		{name: "max known size", size: math.MaxInt64, limit: math.MaxInt64, wantErr: io.ErrUnexpectedEOF},
		{name: "negative limit uses default", size: -1, limit: -1},
		{name: "default limit enforced", size: defaultMaxUnsafePushBytes + 1, wantErr: errdef.ErrSizeExceedsLimit, noRead: true},
		{name: "negative limit default enforced", size: defaultMaxUnsafePushBytes + 1, limit: -1, wantErr: errdef.ErrSizeExceedsLimit, noRead: true},
		{name: "any negative size is unknown", size: -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := New()
			s.MaxUnsafePushBytes = tt.limit
			data := tt.blob
			if data == nil {
				data = blob
			}
			expected := ocispec.Descriptor{
				MediaType: "test", Digest: tt.digest, Size: tt.size,
				Annotations: map[string]string{"test": "preserved"},
				URLs:        []string{"https://example.com/blob"}, ArtifactType: "test/artifact",
				Platform: &ocispec.Platform{Architecture: "amd64", OS: "linux"},
			}
			var source io.Reader = bytes.NewReader(data)
			if tt.readError {
				source = io.MultiReader(source, unsafePushErrorReader{fail})
			}
			reader := &unsafePushReadCounter{r: source}
			got, err := s.UnsafePush(ctx, expected, reader)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UnsafePush() error = %v, want %v", err, tt.wantErr)
			}
			if tt.noRead && reader.calls != 0 {
				t.Errorf("reader called %d times before validation", reader.calls)
			}
			bound := tt.limit
			if bound <= 0 {
				bound = defaultMaxUnsafePushBytes
			}
			if tt.size >= 0 && tt.size < bound {
				bound = tt.size
			}
			if bound < math.MaxInt64 && reader.bytes > bound+1 {
				t.Errorf("read %d bytes, bound %d", reader.bytes, bound+1)
			}
			if err != nil {
				if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
					t.Errorf("error descriptor = %+v, want empty", got)
				}
				actual := content.NewDescriptorFromBytes(expected.MediaType, data)
				exists, err := s.Exists(ctx, actual)
				if err != nil || exists {
					t.Errorf("Exists() = %v, %v after failed push", exists, err)
				}
			} else {
				want := expected
				want.Size = int64(len(data))
				if tt.digest == "" {
					want.Digest = digest.FromBytes(data)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("UnsafePush() = %+v, want %+v", got, want)
				}
				fetched, err := content.FetchAll(ctx, s, got)
				if err != nil || !bytes.Equal(fetched, data) {
					t.Errorf("FetchAll() = %q, %v, want %q", fetched, err, data)
				}
			}
		})
	}
}

type unsafePushReadCounter struct {
	r     io.Reader
	calls int
	bytes int64
}

func (r *unsafePushReadCounter) Read(p []byte) (int, error) {
	r.calls++
	n, err := r.r.Read(p)
	r.bytes += int64(n)
	return n, err
}

type unsafePushErrorReader struct{ err error }

func (r unsafePushErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestStorage_UnsafePush_Duplicate(t *testing.T) {
	for _, mode := range []string{"complete", "unknown", "digest only", "size only", "invalid duplicate"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := New()
			blob := []byte("foobar")
			desc := content.NewDescriptorFromBytes("test", blob)
			desc.Annotations = map[string]string{"key": "value"}
			if err := s.Push(ctx, desc, bytes.NewReader(blob)); err != nil {
				t.Fatal(err)
			}
			expected := desc
			data := blob
			wantErr := errdef.ErrAlreadyExists
			switch mode {
			case "unknown":
				expected.Digest = ""
				expected.Size = -1
			case "digest only":
				expected.Size = -1
			case "size only":
				expected.Digest = ""
			case "invalid duplicate":
				expected.Size = -1
				data = []byte("wrong!")
				wantErr = content.ErrMismatchedDigest
			}
			reader := &unsafePushReadCounter{r: bytes.NewReader(data)}
			got, err := s.UnsafePush(ctx, expected, reader)
			if !errors.Is(err, wantErr) {
				t.Fatalf("UnsafePush() error = %v, want %v", err, wantErr)
			}
			if mode == "invalid duplicate" {
				if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
					t.Errorf("error descriptor = %+v, want empty", got)
				}
			} else if !reflect.DeepEqual(got, desc) {
				t.Errorf("duplicate descriptor = %+v, want %+v", got, desc)
			}
			if mode == "complete" {
				if reader.calls != 0 {
					t.Errorf("pre-check read %d times", reader.calls)
				}
			} else if reader.bytes != int64(len(data)) {
				t.Errorf("read %d bytes, want %d", reader.bytes, len(data))
			}
			fetched, err := content.FetchAll(ctx, s, desc)
			if err != nil || !bytes.Equal(fetched, blob) {
				t.Errorf("existing content = %q, %v", fetched, err)
			}
		})
	}
}

func TestStorage_UnsafePush_Canceled(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &unsafePushReadCounter{r: strings.NewReader("foobar")}
	got, err := s.UnsafePush(ctx, ocispec.Descriptor{Size: -1}, reader)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Fatalf("UnsafePush() = %+v, %v", got, err)
	}
	if reader.calls != 0 {
		t.Errorf("canceled push read %d times", reader.calls)
	}
}

func TestStorage_UnsafePush_Concurrent(t *testing.T) {
	s := New()
	ctx := context.Background()
	blob := []byte("foobar")
	want := content.NewDescriptorFromBytes("test", blob)
	var group errgroup.Group
	for range 16 {
		group.Go(func() error {
			got, err := s.UnsafePush(ctx, ocispec.Descriptor{MediaType: "test", Size: -1}, bytes.NewReader(blob))
			if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("descriptor = %+v, want %+v", got, want)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	fetched, err := content.FetchAll(ctx, s, want)
	if err != nil || !bytes.Equal(fetched, blob) {
		t.Errorf("FetchAll() = %q, %v", fetched, err)
	}
}

func TestStore_UnsafePush(t *testing.T) {
	t.Run("manifest indexed", func(t *testing.T) {
		ctx := context.Background()
		s := New()
		config := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, []byte("{}"))
		if err := s.Push(ctx, config, strings.NewReader("{}")); err != nil {
			t.Fatal(err)
		}
		blob, err := json.Marshal(ocispec.Manifest{Config: config})
		if err != nil {
			t.Fatal(err)
		}
		expected := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, blob)
		got, err := s.UnsafePush(ctx, ocispec.Descriptor{MediaType: expected.MediaType, Size: -1}, bytes.NewReader(blob))
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("UnsafePush() = %+v, %v, want %+v", got, err, expected)
		}
		predecessors, err := s.Predecessors(ctx, config)
		if err != nil || !reflect.DeepEqual(predecessors, []ocispec.Descriptor{expected}) {
			t.Errorf("Predecessors() = %+v, %v", predecessors, err)
		}
	})
	for _, size := range []int64{-1, 9} {
		t.Run(fmt.Sprintf("store limit size %d", size), func(t *testing.T) {
			ctx := context.Background()
			s := New()
			s.MaxUnsafePushBytes = 8
			reader := &unsafePushReadCounter{r: strings.NewReader("123456789")}
			got, err := s.UnsafePush(ctx, ocispec.Descriptor{MediaType: "test", Size: size}, reader)
			if !errors.Is(err, errdef.ErrSizeExceedsLimit) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Fatalf("UnsafePush() = %+v, %v", got, err)
			}
			if size >= 0 && reader.calls != 0 {
				t.Errorf("reader called %d times", reader.calls)
			}
			exists, err := s.Exists(ctx, content.NewDescriptorFromBytes("test", []byte("123456789")))
			if err != nil || exists {
				t.Errorf("Exists() = %v, %v after failure", exists, err)
			}
		})
	}
	t.Run("mismatch leaves no content", func(t *testing.T) {
		ctx := context.Background()
		s := New()
		expected := content.NewDescriptorFromBytes("test", []byte("foobar"))
		expected.Size = -1
		got, err := s.UnsafePush(ctx, expected, strings.NewReader("wrong!"))
		if !errors.Is(err, content.ErrMismatchedDigest) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
			t.Fatalf("UnsafePush() = %+v, %v", got, err)
		}
		for _, data := range []string{"foobar", "wrong!"} {
			exists, err := s.Exists(ctx, content.NewDescriptorFromBytes("test", []byte(data)))
			if err != nil || exists {
				t.Errorf("Exists(%q) = %v, %v", data, exists, err)
			}
		}
	})
	t.Run("index failure returns empty descriptor", func(t *testing.T) {
		ctx := context.Background()
		s := New()
		got, err := s.UnsafePush(ctx, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Size: -1}, strings.NewReader("invalid manifest"))
		if err == nil || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
			t.Fatalf("UnsafePush() = %+v, %v", got, err)
		}
	})
}

// cancelAfterFirstRead cancels after reading at most three bytes.
type cancelAfterFirstRead struct {
	io.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *cancelAfterFirstRead) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.Reader.Read(p[:min(len(p), 3)])
	r.cancel()
	return n, err
}

func TestStore_UnsafePush_CanceledDuringCopy(t *testing.T) {
	blob := []byte("foobarbazqux")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New()
	r := &cancelAfterFirstRead{Reader: bytes.NewReader(blob), cancel: cancel}

	got, err := s.UnsafePush(ctx, ocispec.Descriptor{MediaType: "test", Size: -1}, r)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Fatalf("UnsafePush() = %+v, %v, want empty, context.Canceled", got, err)
	}
	if r.reads != 1 {
		t.Errorf("source read %d times, want 1", r.reads)
	}
	if got := len(s.storage.(*cas.Memory).Map()); got != 0 {
		t.Errorf("storage.Map() = %v, want 0 after cancellation", got)
	}
}
