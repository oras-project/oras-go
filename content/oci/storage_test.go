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
	_ "crypto/sha512"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/ioutil"
	"golang.org/x/sync/errgroup"
)

func TestStorage_Success(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test fetch
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_RelativeRoot_Success(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("error calling filepath.EvalSymlinks(), error =", err)
	}
	currDir, err := os.Getwd()
	if err != nil {
		t.Fatal("error calling Getwd(), error=", err)
	}
	t.Chdir(tempDir)
	s, err := NewStorage(".")
	if err != nil {
		t.Fatal("New() error =", err)
	}
	if want := tempDir; s.root != want {
		t.Errorf("Storage.root = %s, want %s", s.root, want)
	}
	// cd back to allow the temp directory to be removed
	t.Chdir(currDir)
	ctx := context.Background()

	// test push
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	// test fetch
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_NotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Storage.Exists() error =", err)
	}
	if exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Storage.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStorage_AlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Storage.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStorage_BadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Storage.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStorage_Push_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)
	for i := range concurrency {
		eg.Go(func(i int) func() error {
			return func() error {
				if err := s.Push(egCtx, desc, bytes.NewReader(content)); err != nil {
					if errors.Is(err, errdef.ErrAlreadyExists) {
						return nil
					}
					return fmt.Errorf("failed to push content: %v", err)
				}
				return nil
			}
		}(i))
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_Fetch_ExistingBlobs(t *testing.T) {
	content := []byte("hello world")
	dgst := digest.FromBytes(content)
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    dgst,
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "blobs", dgst.Algorithm().String(), dgst.Encoded())
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal("error calling Mkdir(), error =", err)
	}
	if err := os.WriteFile(path, content, 0444); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}

	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Storage.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Storage.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Storage.Fetch() = %v, want %v", got, content)
	}
}

func TestStorage_Fetch_Concurrent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}

	concurrency := 64
	eg, egCtx := errgroup.WithContext(ctx)

	for i := range concurrency {
		eg.Go(func(i int) func() error {
			return func() error {
				rc, err := s.Fetch(egCtx, desc)
				if err != nil {
					return fmt.Errorf("failed to fetch content: %v", err)
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					t.Fatal("Storage.Fetch().Read() error =", err)
				}
				err = rc.Close()
				if err != nil {
					t.Error("Storage.Fetch().Close() error =", err)
				}
				if !bytes.Equal(got, content) {
					t.Errorf("Storage.Fetch() = %v, want %v", got, content)
				}
				return nil
			}
		}(i))
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestStorage_Delete(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()
	if err := s.Push(ctx, desc, bytes.NewReader(content)); err != nil {
		t.Fatal("Storage.Push() error =", err)
	}
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, true)
	}
	err = s.Delete(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Delete() error =", err)
	}
	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Storage.Exists() error =", err)
	}
	if exists {
		t.Errorf("Storage.Exists() = %v, want %v", exists, false)
	}
	err = s.Delete(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("got error = %v, want %v", err, errdef.ErrNotFound)
	}
}

// TestStorage_BadPush_NoIngestFileLeftBehind ensures that the ingest file of a
// push that failed is removed. Nothing else removes it, so a leaked ingest file
// occupies its space until the store is deleted.
func TestStorage_BadPush_NoIngestFileLeftBehind(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	tempDir := t.TempDir()
	s, err := NewStorage(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	if err := s.Push(ctx, desc, strings.NewReader("foobar")); err == nil {
		t.Fatal("Storage.Push() error = nil, wantErr = true")
	}

	entries, err := os.ReadDir(s.ingestRoot)
	if err != nil {
		t.Fatal("error calling ReadDir(), error =", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(ingest entries) = %v, want %v", len(entries), 0)
	}
}

var _ content.UnsafePusher = (*Storage)(nil)

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
		{name: "default limit enforced", size: ioutil.DefaultMaxUnsafePushBytes + 1, wantErr: errdef.ErrSizeExceedsLimit, noRead: true},
		{name: "negative limit default enforced", size: ioutil.DefaultMaxUnsafePushBytes + 1, limit: -1, wantErr: errdef.ErrSizeExceedsLimit, noRead: true},
		{name: "any negative size is unknown", size: -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := NewStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
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
				bound = ioutil.DefaultMaxUnsafePushBytes
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
			assertUnsafeIngestEmpty(t, s)
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
			s, err := NewStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
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
			assertUnsafeIngestEmpty(t, s)
		})
	}
}

func TestStorage_UnsafePush_Canceled(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	assertUnsafeIngestEmpty(t, s)
}

func TestStorage_UnsafePush_Concurrent(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	assertUnsafeIngestEmpty(t, s)
}

func assertUnsafeIngestEmpty(t *testing.T, s *Storage) {
	t.Helper()
	entries, err := os.ReadDir(s.ingestRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("ingest contains %d entries, want empty", len(entries))
	}
}

func TestStorage_UnsafePush_FileErrors(t *testing.T) {
	for _, name := range []string{"ingest directory", "blob directory", "precheck stat", "create temp"} {
		t.Run(name, func(t *testing.T) {
			s, err := NewStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("foobar")
			expected := content.NewDescriptorFromBytes("test", data)
			expected.Size = -1
			switch name {
			case "ingest directory":
				if err := os.WriteFile(s.ingestRoot, nil, 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(s.ingestRoot) })
			case "blob directory", "precheck stat":
				if err := os.WriteFile(filepath.Join(s.root, ocispec.ImageBlobsDir), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if name == "precheck stat" {
					expected.Size = int64(len(data))
				}
			case "create temp":
				if os.Geteuid() == 0 {
					t.Skip("root can write a read-only directory")
				}
				if err := os.MkdirAll(s.ingestRoot, 0555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(s.ingestRoot, 0777) })
				if fp, err := os.CreateTemp(s.ingestRoot, "probe"); err == nil {
					fp.Close()
					os.Remove(fp.Name())
					t.Skip("file system does not enforce directory write permissions")
				}
			}
			reader := &unsafePushReadCounter{r: bytes.NewReader(data)}
			got, err := s.UnsafePush(context.Background(), expected, reader)
			if err == nil || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Fatalf("UnsafePush() = %+v, %v", got, err)
			}
			if name != "blob directory" && reader.calls != 0 {
				t.Errorf("read %d times before file error", reader.calls)
			}
			if name == "ingest directory" {
				if err := os.Remove(s.ingestRoot); err != nil {
					t.Fatal(err)
				}
			}
			assertUnsafeIngestEmpty(t, s)
		})
	}
}

func TestStorage_UnsafePush_RenamePermission(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires directory write permissions enforced on a non-root Unix user")
	}
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("foobar")
	desc := content.NewDescriptorFromBytes("test", data)
	dir := filepath.Join(s.root, ocispec.ImageBlobsDir, "sha256")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0755); err != nil {
			t.Errorf("restore directory permissions: %v", err)
		}
	})
	got, err := s.UnsafePush(context.Background(), desc, bytes.NewReader(data))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("UnsafePush() error = %v, want permission error", err)
	}
	if errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("permission error incorrectly classified as ErrAlreadyExists: %v", err)
	}
	var pathErr *os.LinkError
	if !errors.As(err, &pathErr) || pathErr.Op != "rename" {
		t.Errorf("error = %v, want original rename error", err)
	}
	if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Errorf("descriptor = %+v, want empty", got)
	}
	exists, err := s.Exists(context.Background(), desc)
	if err != nil || exists {
		t.Errorf("Exists() = %v, %v, want absent", exists, err)
	}
	assertUnsafeIngestEmpty(t, s)
}

func TestStorage_UnsafePush_DuplicateWrongSize(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	data := []byte("foobar")
	desc := content.NewDescriptorFromBytes("test", data)
	if err := s.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	expected := desc
	expected.Size = 7
	reader := &unsafePushReadCounter{r: bytes.NewReader(data)}
	got, err := s.UnsafePush(ctx, expected, reader)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("UnsafePush() error = %v, want unexpected EOF", err)
	}
	if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Errorf("descriptor = %+v, want empty", got)
	}
	if reader.bytes != int64(len(data)) {
		t.Errorf("read %d bytes, want %d", reader.bytes, len(data))
	}
	fetched, err := content.FetchAll(ctx, s, desc)
	if err != nil || !bytes.Equal(fetched, data) {
		t.Errorf("existing content = %q, %v, want %q", fetched, err, data)
	}
	assertUnsafeIngestEmpty(t, s)
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

func TestUnsafePush_CanceledDuringCopy(t *testing.T) {
	blob := []byte("foobarbazqux")
	for _, kind := range []string{"Storage", "Store"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			var p content.UnsafePusher
			var err error
			if kind == "Storage" {
				p, err = NewStorage(root)
			} else {
				p, err = New(root)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &cancelAfterFirstRead{Reader: bytes.NewReader(blob), cancel: cancel}
			got, err := p.UnsafePush(ctx, ocispec.Descriptor{MediaType: "test", Size: -1}, r)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Fatalf("UnsafePush() = %+v, %v, want empty, context.Canceled", got, err)
			}
			if r.reads != 1 {
				t.Errorf("source read %d times, want 1", r.reads)
			}
			if entries, err := os.ReadDir(filepath.Join(root, "blobs", "sha256")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			} else if len(entries) != 0 {
				t.Errorf("blob dir not empty: %d entries", len(entries))
			}
			if entries, err := os.ReadDir(filepath.Join(root, ingestDirName)); err != nil {
				t.Fatal(err)
			} else if len(entries) != 0 {
				t.Errorf("ingest dir not cleaned: %d entries", len(entries))
			}
		})
	}
}
