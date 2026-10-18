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

package remote

import (
	"bytes"
	"context"
	_ "crypto/sha512"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
)

var _ content.UnsafePusher = (*blobStore)(nil)

func TestBlobStore_UnsafePush_UnknownDigestAndSize(t *testing.T) {
	blob := []byte("hello world") // 11 bytes, MaxChunkSize 4 -> 3 PATCHes
	want := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(blob), Size: int64(len(blob))}

	reg := &chunkedUploadRegistry{t: t}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	repo := newChunkedTestRepo(t, srv, 4)

	got, err := repo.blobs().UnsafePush(context.Background(), ocispec.Descriptor{MediaType: want.MediaType, Size: -1}, bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("UnsafePush() error = %v", err)
	}
	if got.Digest != want.Digest || got.Size != want.Size || got.MediaType != want.MediaType {
		t.Errorf("UnsafePush() = %+v, want %+v", got, want)
	}
	if reg.digestQuery != want.Digest.String() {
		t.Errorf("PUT ?digest= %q, want %q", reg.digestQuery, want.Digest)
	}
	if !bytes.Equal(reg.uploaded, blob) {
		t.Errorf("uploaded = %q, want %q", reg.uploaded, blob)
	}
	if reg.patchCount() == 0 {
		t.Errorf("expected streamed PATCH upload, got %v", reg.methodSequence())
	}
}

func TestBlobStore_UnsafePush_Errors(t *testing.T) {
	blob := []byte("hello world")
	tests := []struct {
		name     string
		expected ocispec.Descriptor
		limit    int64
		wantErr  error
	}{
		{"wrong digest", ocispec.Descriptor{Digest: digest.FromString("other"), Size: -1}, 0, content.ErrMismatchedDigest},
		{"short size", ocispec.Descriptor{Size: 20}, 0, io.ErrUnexpectedEOF},
		{"long size", ocispec.Descriptor{Size: 5}, 0, content.ErrTrailingData},
		{"cap exceeded", ocispec.Descriptor{Size: -1}, 8, errdef.ErrSizeExceedsLimit},
		{"known size over cap", ocispec.Descriptor{Size: 11}, 8, errdef.ErrSizeExceedsLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &chunkedUploadRegistry{t: t}
			srv := httptest.NewServer(reg.handler())
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, 4)
			if tt.limit > 0 {
				repo.MaxUnsafePushBytes = tt.limit
			}
			got, err := repo.blobs().UnsafePush(context.Background(), tt.expected, bytes.NewReader(blob))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UnsafePush() error = %v, want %v", err, tt.wantErr)
			}
			if got.Digest != "" || got.Size != 0 {
				t.Errorf("UnsafePush() desc = %+v, want empty", got)
			}
			if reg.digestQuery != "" {
				t.Errorf("upload was committed with digest %q", reg.digestQuery)
			}
			if reg.patchCount() > 0 && !reg.cancelled {
				t.Errorf("open session not cancelled: %v", reg.methodSequence())
			}
		})
	}
}

func TestBlobStore_UnsafePush_Success(t *testing.T) {
	for _, chunk := range []int64{0, 4, 16, math.MaxInt64} {
		for _, fields := range []string{"neither", "size", "digest", "both", "sha512"} {
			t.Run(fmt.Sprintf("chunk=%d/%s", chunk, fields), func(t *testing.T) {
				blob := []byte("hello world")
				want := ocispec.Descriptor{
					MediaType:    "application/octet-stream",
					Digest:       digest.FromBytes(blob),
					Size:         int64(len(blob)),
					Annotations:  map[string]string{"test": "preserved"},
					URLs:         []string{"https://example.invalid/blob"},
					Data:         []byte("inline"),
					Platform:     &ocispec.Platform{OS: "linux", Architecture: "amd64"},
					ArtifactType: "application/test",
				}
				expected := want
				switch fields {
				case "neither":
					expected.Digest, expected.Size = "", -1
				case "size":
					expected.Digest = ""
				case "digest":
					expected.Size = -1
				case "sha512":
					want.Digest = digest.SHA512.FromBytes(blob)
					expected.Digest, expected.Size = want.Digest, -1
				}
				reg := &chunkedUploadRegistry{t: t}
				srv := httptest.NewServer(reg.handler())
				defer srv.Close()
				repo := newChunkedTestRepo(t, srv, chunk)
				if chunk == math.MaxInt64 {
					repo.MaxUnsafePushBytes = math.MaxInt64
				}
				got, err := repo.blobs().UnsafePush(context.Background(), expected, bytes.NewReader(blob))
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
				}
				if !bytes.Equal(reg.uploaded, blob) || reg.digestQuery != want.Digest.String() || reg.cancelled {
					t.Errorf("upload = %q, digest = %q, cancelled = %v", reg.uploaded, reg.digestQuery, reg.cancelled)
				}
				if (reg.patchCount() > 0) != (chunk == 4) {
					t.Errorf("unexpected upload sequence: %v", reg.methodSequence())
				}
				var wantMethods []string
				if fields == "both" {
					wantMethods = append(wantMethods, http.MethodHead)
				}
				wantMethods = append(wantMethods, http.MethodPost)
				if chunk == 4 {
					wantMethods = append(wantMethods, http.MethodPatch, http.MethodPatch, http.MethodPatch)
				}
				wantMethods = append(wantMethods, http.MethodPut)
				if !slices.Equal(reg.methodSequence(), wantMethods) {
					t.Errorf("methods = %v, want %v", reg.methodSequence(), wantMethods)
				}
			})
		}
	}
}

func TestBlobStore_UnsafePush_EmptyAndChunkBoundary(t *testing.T) {
	for _, blob := range []string{"", "1234"} {
		for _, fields := range []string{"neither", "size", "digest", "both"} {
			t.Run(fmt.Sprintf("%q/%s", blob, fields), func(t *testing.T) {
				expected := ocispec.Descriptor{Size: -1}
				if fields == "size" || fields == "both" {
					expected.Size = int64(len(blob))
				}
				if fields == "digest" || fields == "both" {
					expected.Digest = digest.FromString(blob)
				}
				reg := &chunkedUploadRegistry{t: t}
				srv := httptest.NewServer(reg.handler())
				defer srv.Close()
				repo := newChunkedTestRepo(t, srv, 4)
				got, err := repo.blobs().UnsafePush(context.Background(), expected, strings.NewReader(blob))
				if err != nil || got.Digest != digest.FromString(blob) || got.Size != int64(len(blob)) {
					t.Fatalf("UnsafePush() = %+v, %v", got, err)
				}
				if reg.patchCount() != 0 || string(reg.uploaded) != blob {
					t.Errorf("upload = %q, methods = %v", reg.uploaded, reg.methodSequence())
				}
			})
		}
	}
}

type unsafeNoReadReader struct {
	t *testing.T
}

func (r unsafeNoReadReader) Read([]byte) (int, error) {
	r.t.Error("content was read before validation or duplicate detection")
	return 0, errors.New("unexpected read")
}

func TestBlobStore_UnsafePush_Existing(t *testing.T) {
	expected := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromString("hello"), Size: 5}
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			reg := &chunkedUploadRegistry{t: t}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				reg.mu.Lock()
				reg.methods = append(reg.methods, r.Method)
				reg.mu.Unlock()
				w.Header().Set("Content-Length", "5")
				w.Header().Set("Docker-Content-Digest", expected.Digest.String())
				w.WriteHeader(status)
			}))
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, 4)
			got, err := repo.blobs().UnsafePush(context.Background(), expected, unsafeNoReadReader{t})
			if status == http.StatusOK {
				if !errors.Is(err, errdef.ErrAlreadyExists) || !reflect.DeepEqual(got, expected) {
					t.Fatalf("UnsafePush() = %+v, %v; want expected, ErrAlreadyExists", got, err)
				}
			} else if err == nil || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Fatalf("UnsafePush() = %+v, %v; want empty, error", got, err)
			}
			if !slices.Equal(reg.methodSequence(), []string{http.MethodHead}) {
				t.Errorf("methods = %v, want HEAD only", reg.methodSequence())
			}
		})
	}
}

func TestBlobStore_UnsafePush_KnownVerification(t *testing.T) {
	for _, chunk := range []int64{0, 4} {
		for _, tt := range []struct {
			name    string
			blob    string
			digest  digest.Digest
			wantErr error
		}{
			{"trailing", "hello world!", digest.FromString("hello world"), content.ErrTrailingData},
			{"short", "hello", digest.FromString("hello world"), io.ErrUnexpectedEOF},
			{"mismatch", "hello world", digest.FromString("other"), content.ErrMismatchedDigest},
		} {
			t.Run(fmt.Sprintf("chunk=%d/%s", chunk, tt.name), func(t *testing.T) {
				reg := &chunkedUploadRegistry{t: t}
				srv := httptest.NewServer(reg.handler())
				defer srv.Close()
				repo := newChunkedTestRepo(t, srv, chunk)
				expected := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: tt.digest, Size: 11}
				got, err := repo.blobs().UnsafePush(context.Background(), expected, strings.NewReader(tt.blob))
				if !errors.Is(err, tt.wantErr) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
					t.Fatalf("UnsafePush() = %+v, %v; want empty, %v", got, err, tt.wantErr)
				}
				if reg.digestQuery != "" {
					t.Errorf("content was committed with digest %q", reg.digestQuery)
				}
				if chunk == 0 && !slices.Equal(reg.methodSequence(), []string{http.MethodHead}) {
					t.Errorf("methods = %v, want HEAD only", reg.methodSequence())
				}
				if chunk > 0 && !reg.cancelled {
					t.Error("chunked upload session was not canceled")
				}
			})
		}
	}
}

func TestBlobStore_UnsafePush_Validation(t *testing.T) {
	for _, tt := range []struct {
		name            string
		expected        ocispec.Descriptor
		wantErr         error
		policy          bool
		cancel          bool
		rejectAlgorithm bool
	}{
		{name: "known size over limit", expected: ocispec.Descriptor{Size: 9}, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "invalid digest", expected: ocispec.Descriptor{Digest: "sha256:bad", Size: -1}, wantErr: errdef.ErrInvalidDigest},
		{name: "unsupported algorithm", expected: ocispec.Descriptor{Digest: "sha999:abcd", Size: -1}, wantErr: errdef.ErrUnsupported},
		{name: "remote algorithm rejected", expected: ocispec.Descriptor{Digest: digest.FromString("hello"), Size: -1}, wantErr: errdef.ErrUnsupported, rejectAlgorithm: true},
		{name: "policy rejected", policy: true},
		{name: "canceled context", cancel: true, wantErr: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRejectPolicyRepo(t)
			if !tt.policy {
				repo.Registry.Policy = nil
			}
			repo.MaxUnsafePushBytes = 8
			requests := 0
			repo.Registry.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected request")
			})}
			if tt.rejectAlgorithm {
				previous := isSupportedAlgorithm
				isSupportedAlgorithm = func(digest.Algorithm) bool { return false }
				t.Cleanup(func() { isSupportedAlgorithm = previous })
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			got, err := repo.blobs().UnsafePush(ctx, tt.expected, unsafeNoReadReader{t})
			if tt.policy {
				assertPolicyDenied(t, err, "UnsafePush")
			} else if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, ocispec.Descriptor{}) || requests != 0 {
				t.Errorf("descriptor = %+v, requests = %d; want empty and 0", got, requests)
			}
		})
	}
}

func TestBlobStore_UnsafePush_Limits(t *testing.T) {
	for _, tt := range []struct {
		name                            string
		chunk, registryLimit, repoLimit int64
		wantErr                         error
	}{
		{"small over limit", 16, 0, 8, errdef.ErrSizeExceedsLimit},
		{"stream over limit", 4, 0, 8, errdef.ErrSizeExceedsLimit},
		{"small exact limit", 16, 0, 11, nil},
		{"stream exact limit", 4, 0, 11, nil},
		{"registry limit", 4, 8, 0, errdef.ErrSizeExceedsLimit},
		{"repository precedence", 4, 8, 11, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &chunkedUploadRegistry{t: t}
			srv := httptest.NewServer(reg.handler())
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, tt.chunk)
			repo.Registry.MaxUnsafePushBytes = tt.registryLimit
			repo.MaxUnsafePushBytes = tt.repoLimit
			got, err := repo.blobs().UnsafePush(context.Background(), ocispec.Descriptor{Size: -1}, strings.NewReader("hello world"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if !reflect.DeepEqual(got, ocispec.Descriptor{}) || reg.digestQuery != "" {
					t.Errorf("failed upload returned %+v or committed with %q", got, reg.digestQuery)
				}
				if reg.patchCount() > 0 && !reg.cancelled {
					t.Error("session was not canceled")
				}
			} else if got.Size != 11 || got.Digest != digest.FromString("hello world") {
				t.Errorf("descriptor = %+v", got)
			}
		})
	}
}

func TestRepository_maxUnsafePushBytes(t *testing.T) {
	for _, tt := range []struct {
		registry, repository, want int64
	}{
		{0, 0, 4 << 30},
		{-1, -1, 4 << 30},
		{8, 0, 8},
		{8, -1, 8},
		{8, 16, 16},
		{-1, 16, 16},
	} {
		repo := &Repository{Registry: &Registry{MaxUnsafePushBytes: tt.registry}, MaxUnsafePushBytes: tt.repository}
		if got := repo.maxUnsafePushBytes(); got != tt.want {
			t.Errorf("maxUnsafePushBytes(%d, %d) = %d, want %d", tt.registry, tt.repository, got, tt.want)
		}
	}
}

func TestBlobStore_UnsafePush_StreamFailures(t *testing.T) {
	readErr := errors.New("unsafe push reader failed")
	for _, tt := range []struct {
		name                   string
		expected               ocispec.Descriptor
		limit                  int64
		configure              func(*chunkedUploadRegistry)
		reader                 func() io.Reader
		wantErr                error
		wantCancel, wantCommit bool
	}{
		{name: "mismatch", expected: ocispec.Descriptor{Digest: digest.FromString("other"), Size: -1}, wantErr: content.ErrMismatchedDigest, wantCancel: true},
		{name: "trailing", expected: ocispec.Descriptor{Size: 8}, wantErr: content.ErrTrailingData, wantCancel: true},
		{name: "cap", expected: ocispec.Descriptor{Size: -1}, limit: 8, wantErr: errdef.ErrSizeExceedsLimit, wantCancel: true},
		{name: "short", expected: ocispec.Descriptor{Size: 20}, wantErr: io.ErrUnexpectedEOF, wantCancel: true},
		{name: "PATCH rejected", expected: ocispec.Descriptor{Size: -1}, configure: func(reg *chunkedUploadRegistry) { reg.rejectPATCHStatus = http.StatusInternalServerError }, wantCancel: true},
		{name: "PUT rejected", expected: ocispec.Descriptor{Size: -1}, configure: func(reg *chunkedUploadRegistry) { reg.rejectPUTStatus = http.StatusInternalServerError }, wantCancel: true},
		{name: "POST rejected", expected: ocispec.Descriptor{Size: -1}, configure: func(reg *chunkedUploadRegistry) { reg.rejectPOSTStatus = http.StatusBadRequest }},
		{name: "chunk minimum too large", expected: ocispec.Descriptor{Size: -1}, configure: func(reg *chunkedUploadRegistry) { reg.minChunkLength = maxChunkMinLength + 1 }, wantCancel: true},
		{name: "read error", expected: ocispec.Descriptor{Size: -1}, reader: func() io.Reader { return io.MultiReader(strings.NewReader("hello wo"), &errReader{err: readErr}) }, wantErr: readErr, wantCancel: true},
		{name: "truncated source", expected: ocispec.Descriptor{Size: -1}, reader: func() io.Reader {
			return io.MultiReader(strings.NewReader("hello wo"), &errReader{err: io.ErrUnexpectedEOF})
		}, wantErr: io.ErrUnexpectedEOF, wantCancel: true},
		{name: "error with final bytes", expected: ocispec.Descriptor{Size: -1}, reader: func() io.Reader { return &unsafeFinalErrorReader{data: []byte("hello wo"), err: readErr} }, wantErr: readErr, wantCancel: true},
		{name: "known descriptor source error", expected: ocispec.Descriptor{Digest: digest.FromString("hello world"), Size: 11}, reader: func() io.Reader { return &unsafeFinalErrorReader{data: []byte("hello wo"), err: readErr} }, wantErr: readErr, wantCancel: true},
		{name: "error at known size boundary", expected: ocispec.Descriptor{Digest: digest.FromString("hello wo"), Size: 8}, reader: func() io.Reader { return &unsafeFinalErrorReader{data: []byte("hello wo"), err: readErr} }, wantErr: readErr, wantCancel: true},
		{name: "probe error", expected: ocispec.Descriptor{Size: 11}, reader: func() io.Reader { return io.MultiReader(strings.NewReader("hello world"), &errReader{err: readErr}) }, wantErr: readErr, wantCancel: true},
		{name: "registry digest mismatch", expected: ocispec.Descriptor{Size: -1}, configure: func(reg *chunkedUploadRegistry) { reg.putDigestOverride = digest.FromString("other").String() }, wantCommit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &chunkedUploadRegistry{t: t}
			if tt.configure != nil {
				tt.configure(reg)
			}
			srv := httptest.NewServer(reg.handler())
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, 4)
			repo.MaxUnsafePushBytes = tt.limit
			var r io.Reader = strings.NewReader("hello world")
			if tt.reader != nil {
				r = tt.reader()
			}
			got, err := repo.blobs().UnsafePush(context.Background(), tt.expected, r)
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("error = %v, want error wrapping %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, ocispec.Descriptor{}) || reg.cancelled != tt.wantCancel || (reg.digestQuery != "") != tt.wantCommit {
				t.Errorf("descriptor = %+v, canceled = %v, digest = %q; want empty, %v, committed=%v", got, reg.cancelled, reg.digestQuery, tt.wantCancel, tt.wantCommit)
			}
		})
	}
}

func TestBlobStore_UnsafePush_SmallFailures(t *testing.T) {
	readErr := errors.New("unsafe push head read failed")
	for _, tt := range []struct {
		name       string
		expected   ocispec.Descriptor
		reader     io.Reader
		wantErr    error
		rejectPOST bool
	}{
		{name: "head read error", expected: ocispec.Descriptor{Size: -1}, reader: &errReader{err: readErr}, wantErr: readErr},
		{name: "trailing", expected: ocispec.Descriptor{Size: 2}, reader: strings.NewReader("hello"), wantErr: content.ErrTrailingData},
		{name: "short", expected: ocispec.Descriptor{Size: 6}, reader: strings.NewReader("hello"), wantErr: io.ErrUnexpectedEOF},
		{name: "mismatch", expected: ocispec.Descriptor{Digest: digest.FromString("other"), Size: -1}, reader: strings.NewReader("hello"), wantErr: content.ErrMismatchedDigest},
		{name: "POST rejected", expected: ocispec.Descriptor{Size: -1}, reader: strings.NewReader("hello"), rejectPOST: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := &chunkedUploadRegistry{t: t}
			if tt.rejectPOST {
				reg.rejectPOSTStatus = http.StatusBadRequest
			}
			srv := httptest.NewServer(reg.handler())
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, 16)
			got, err := repo.blobs().UnsafePush(context.Background(), tt.expected, tt.reader)
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Fatalf("UnsafePush() = %+v, %v; want empty, error wrapping %v", got, err, tt.wantErr)
			}
			if reg.digestQuery != "" || reg.patchCount() != 0 {
				t.Errorf("unexpected upload: %v, digest = %q", reg.methodSequence(), reg.digestQuery)
			}
		})
	}
}

// unsafeFinalErrorReader returns its terminal error together with its last bytes.
type unsafeFinalErrorReader struct {
	data []byte
	err  error
}

func (r *unsafeFinalErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		err := r.err
		r.err = io.EOF
		return n, err
	}
	return n, nil
}

func TestBlobStore_UnsafePush_RaisedChunkMinimum(t *testing.T) {
	reg := &chunkedUploadRegistry{t: t, minChunkLength: 16}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	repo := newChunkedTestRepo(t, srv, 4)
	got, err := repo.blobs().UnsafePush(context.Background(), ocispec.Descriptor{Size: -1}, strings.NewReader("hello world"))
	if err != nil || got.Size != 11 || got.Digest != digest.FromString("hello world") {
		t.Fatalf("UnsafePush() = %+v, %v", got, err)
	}
	// Once the head has been consumed, even a raised minimum larger than the
	// blob must use the open session rather than fall back to monolithic push.
	if reg.cancelled || !slices.Equal(reg.methodSequence(), []string{http.MethodPost, http.MethodPatch, http.MethodPut}) {
		t.Errorf("unexpected methods: %v, canceled = %v", reg.methodSequence(), reg.cancelled)
	}
}

func TestBlobStore_UnsafePush_DuplicateWrongSize(t *testing.T) {
	expected := ocispec.Descriptor{Digest: digest.FromString("foobar"), Size: 7}
	reg := &chunkedUploadRegistry{t: t}
	handler := reg.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			reg.mu.Lock()
			reg.methods = append(reg.methods, r.Method)
			reg.mu.Unlock()
			w.Header().Set("Content-Length", "6")
			w.Header().Set("Docker-Content-Digest", expected.Digest.String())
			w.WriteHeader(http.StatusOK)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	repo := newChunkedTestRepo(t, srv, 16)
	got, err := repo.blobs().UnsafePush(context.Background(), expected, strings.NewReader("foobar"))
	if !errors.Is(err, io.ErrUnexpectedEOF) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Fatalf("UnsafePush() = %+v, %v; want empty, ErrUnexpectedEOF", got, err)
	}
	if reg.digestQuery != "" || !slices.Equal(reg.methodSequence(), []string{http.MethodHead}) {
		t.Errorf("unexpected upload: %v, digest = %q", reg.methodSequence(), reg.digestQuery)
	}
}

func TestBlobStore_UnsafePush_SmallReplayable(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known=%v", known), func(t *testing.T) {
			const blob = "hello world"
			reg := &chunkedUploadRegistry{t: t}
			srv := httptest.NewServer(reg.handler())
			defer srv.Close()
			repo := newChunkedTestRepo(t, srv, 16)
			expected := ocispec.Descriptor{Size: -1}
			wantMethods := []string{http.MethodPost}
			if known {
				expected.Digest, expected.Size = digest.FromString(blob), int64(len(blob))
				wantMethods = []string{http.MethodHead, http.MethodPost, http.MethodPut}
			} else {
				repo.SingleRequestPushThreshold = 16
			}
			repo.Registry.Client = &http.Client{Transport: chunkedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut || (req.Method == http.MethodPost && req.URL.Query().Get("digest") != "") {
					if req.GetBody == nil {
						t.Error("verified small content is not replayable")
					} else {
						body, err := req.GetBody()
						if err != nil {
							t.Errorf("GetBody() error = %v", err)
						} else {
							replay, err := io.ReadAll(body)
							body.Close()
							if err != nil || string(replay) != blob {
								t.Errorf("replayed body = %q, %v; want %q, nil", replay, err, blob)
							}
						}
					}
				}
				return http.DefaultTransport.RoundTrip(req)
			})}
			got, err := repo.blobs().UnsafePush(context.Background(), expected, strings.NewReader(blob))
			if err != nil || got.Digest != digest.FromString(blob) || got.Size != int64(len(blob)) {
				t.Fatalf("UnsafePush() = %+v, %v", got, err)
			}
			if string(reg.uploaded) != blob || reg.digestQuery != got.Digest.String() || !slices.Equal(reg.methodSequence(), wantMethods) {
				t.Errorf("upload = %q, digest = %q, methods = %v; want %v", reg.uploaded, reg.digestQuery, reg.methodSequence(), wantMethods)
			}
		})
	}
}
