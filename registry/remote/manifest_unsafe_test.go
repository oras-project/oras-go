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
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

var _ content.UnsafePusher = (*manifestStore)(nil)

const manifestUnsafePrefix = "/v2/test/manifests/"

// manifestUnsafeRequest scripts one request, including its response and optional
// body checks. Every test asserts the complete request sequence, even on failure.
type manifestUnsafeRequest struct {
	method, path string
	status       int
	headers      http.Header
	putDesc      *ocispec.Descriptor
	body         []byte
	hashBody     bool
	check        func(*http.Request, []byte)
}

func newManifestUnsafeRepo(t *testing.T, steps ...manifestUnsafeRequest) *Repository {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		i := len(requests)
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if i >= len(steps) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		step := steps[i]
		if r.Method != step.method || r.URL.RequestURI() != step.path {
			t.Errorf("request %d = %s %s, want %s %s", i, r.Method, r.URL.RequestURI(), step.method, step.path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if step.putDesc != nil {
			if got := r.Header.Get("Content-Type"); got != step.putDesc.MediaType {
				t.Errorf("Content-Type = %q, want %q", got, step.putDesc.MediaType)
			}
			if r.ContentLength != step.putDesc.Size {
				t.Errorf("Content-Length = %d, want %d", r.ContentLength, step.putDesc.Size)
			}
			if !bytes.Equal(body, step.body) {
				t.Errorf("PUT body differs: got %d bytes, want %d bytes", len(body), len(step.body))
			}
		}
		if step.check != nil {
			step.check(r, body)
		}
		for key, values := range step.headers {
			w.Header()[key] = values
		}
		if step.hashBody {
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(body).String())
		}
		w.WriteHeader(step.status)
	}))
	t.Cleanup(func() {
		srv.Close()
		mu.Lock()
		defer mu.Unlock()
		var want []string
		for _, step := range steps {
			want = append(want, step.method+" "+step.path)
		}
		if !slices.Equal(requests, want) {
			t.Errorf("requests = %v, want %v", requests, want)
		}
	})
	repo, err := NewRepository(strings.TrimPrefix(srv.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	// Avoid automatic retries so rejected requests have an exact count.
	repo.Registry.Client = srv.Client()
	return repo
}

func manifestUnsafeHead(desc ocispec.Descriptor, status int) manifestUnsafeRequest {
	return manifestUnsafeRequest{
		method: http.MethodHead, path: manifestUnsafePrefix + desc.Digest.String(), status: status,
		headers: http.Header{
			"Content-Type":          {desc.MediaType},
			"Content-Length":        {strconv.FormatInt(desc.Size, 10)},
			"Docker-Content-Digest": {desc.Digest.String()},
		},
	}
}

func manifestUnsafePut(desc ocispec.Descriptor, body []byte, status int) manifestUnsafeRequest {
	return manifestUnsafeRequest{
		method: http.MethodPut, path: manifestUnsafePrefix + desc.Digest.String(), status: status,
		headers: http.Header{"Docker-Content-Digest": {desc.Digest.String()}},
		putDesc: &desc, body: body,
	}
}

// manifestUnsafeReader counts bytes and calls without exposing WriterTo, so
// verification tests can prove that content was unread or bounded as required.
type manifestUnsafeReader struct {
	r     io.Reader
	n     int
	calls int
}

func (r *manifestUnsafeReader) Read(p []byte) (int, error) {
	r.calls++
	n, err := r.r.Read(p)
	r.n += n
	return n, err
}

func TestManifestStore_UnsafePush_Success(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	for _, tt := range []struct {
		name, mediaType string
		knownDigest     bool
		size            int64
		algorithm       digest.Algorithm
	}{
		{name: "unknown digest and size", size: -1},
		{name: "known digest and size", knownDigest: true, size: int64(len(body))},
		{name: "known digest only", knownDigest: true, size: -1},
		{name: "known size only", size: int64(len(body))},
		{name: "negative five means unknown", size: -5},
		{name: "sha512", knownDigest: true, size: -1, algorithm: digest.SHA512},
		{name: "custom media type", size: -1, mediaType: "application/vnd.example.manifest.v1+json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mt := tt.mediaType
			if mt == "" {
				mt = ocispec.MediaTypeImageManifest
			}
			alg := tt.algorithm
			if alg == "" {
				alg = digest.Canonical
			}
			want := ocispec.Descriptor{
				MediaType: mt, Digest: alg.FromBytes(body), Size: int64(len(body)),
				Annotations: map[string]string{"example": "preserved"}, ArtifactType: "application/example",
				URLs: []string{"https://example.com/manifest"}, Data: body,
				Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"},
			}
			expected := want
			expected.Size = tt.size
			if !tt.knownDigest {
				expected.Digest = ""
			}
			repo := newManifestUnsafeRepo(t, manifestUnsafeHead(want, http.StatusNotFound), manifestUnsafePut(want, body, http.StatusCreated))
			if tt.mediaType != "" {
				repo.ManifestMediaTypes = []string{tt.mediaType}
			}
			got, err := repo.manifests().UnsafePush(context.Background(), expected, bytes.NewReader(body))
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
			}
		})
	}
}

func TestManifestStore_UnsafePush_Validation(t *testing.T) {
	mt := ocispec.MediaTypeImageManifest
	for _, tt := range []struct {
		name            string
		expected        ocispec.Descriptor
		wantErr         error
		cancel          bool
		rejectAlgorithm bool
	}{
		{name: "size limit plus one", expected: ocispec.Descriptor{MediaType: mt, Size: 9}, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "empty media type", expected: ocispec.Descriptor{Size: -1}, wantErr: errdef.ErrInvalidMediaType},
		{name: "blob media type", expected: ocispec.Descriptor{MediaType: "application/octet-stream", Size: -1}, wantErr: errdef.ErrInvalidMediaType},
		{name: "unsupported md5", expected: ocispec.Descriptor{MediaType: mt, Digest: digest.Digest("md5:" + strings.Repeat("0", 32)), Size: -1}, wantErr: errdef.ErrUnsupported},
		{name: "invalid digest", expected: ocispec.Descriptor{MediaType: mt, Digest: "sha256:bad", Size: -1}, wantErr: errdef.ErrInvalidDigest},
		{name: "remote allowlist", expected: ocispec.Descriptor{MediaType: mt, Digest: digest.FromString("manifest"), Size: -1}, wantErr: errdef.ErrUnsupported, rejectAlgorithm: true},
		{name: "cancel before policy", expected: ocispec.Descriptor{MediaType: mt, Size: -1}, wantErr: context.Canceled, cancel: true},
		{name: "validation before cancel", expected: ocispec.Descriptor{Size: -1}, wantErr: errdef.ErrInvalidMediaType, cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newManifestUnsafeRepo(t)
			repo.Registry.MaxMetadataBytes = 8
			// A denying policy must not mask validation or cancellation errors.
			repo.Registry.Policy = newRejectPolicyRepo(t).Registry.Policy
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
			r := &manifestUnsafeReader{r: strings.NewReader("unread")}
			got, err := repo.manifests().UnsafePush(ctx, tt.expected, r)
			if !errors.Is(err, tt.wantErr) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Errorf("UnsafePush() = %+v, %v; want empty, %v", got, err, tt.wantErr)
			}
			if r.calls != 0 {
				t.Errorf("reader calls = %d, want 0", r.calls)
			}
		})
	}
}

func TestManifestStore_UnsafePush_Verification(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	readErr := errors.New("manifest source failed")
	for _, tt := range []struct {
		name     string
		digest   digest.Digest
		size     int64
		reader   io.Reader
		wantErr  error
		maxBytes int
		maxMeta  int64
	}{
		{name: "digest mismatch unknown size", digest: digest.FromString("other"), size: -1, wantErr: content.ErrMismatchedDigest},
		{name: "digest mismatch known size", digest: digest.FromString("other"), size: int64(len(body)), wantErr: content.ErrMismatchedDigest},
		{name: "truncated", size: int64(len(body) + 1), wantErr: io.ErrUnexpectedEOF},
		{name: "huge known size empty reader", size: 1 << 62, reader: bytes.NewReader(nil), wantErr: io.ErrUnexpectedEOF, maxMeta: math.MaxInt64},
		{name: "truncated known digest", digest: digest.FromBytes(body), size: int64(len(body) + 1), wantErr: io.ErrUnexpectedEOF},
		{name: "trailing", size: 5, wantErr: content.ErrTrailingData, maxBytes: 6},
		{name: "trailing known digest", digest: digest.FromBytes(body), size: 5, wantErr: content.ErrTrailingData, maxBytes: 6},
		{name: "zero size nonempty", size: 0, wantErr: content.ErrTrailingData, maxBytes: 1},
		{name: "source error", size: -1, reader: io.MultiReader(bytes.NewReader(body[:5]), &errReader{err: readErr}), wantErr: readErr},
		{name: "probe error", size: int64(len(body)), reader: io.MultiReader(bytes.NewReader(body), &errReader{err: readErr}), wantErr: readErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expected := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: tt.digest, Size: tt.size}
			var steps []manifestUnsafeRequest
			if tt.digest != "" && tt.size >= 0 {
				steps = append(steps, manifestUnsafeHead(expected, http.StatusNotFound))
			}
			repo := newManifestUnsafeRepo(t, steps...)
			repo.Registry.MaxMetadataBytes = tt.maxMeta
			src := tt.reader
			if src == nil {
				src = bytes.NewReader(body)
			}
			r := &manifestUnsafeReader{r: src}
			got, err := repo.manifests().UnsafePush(context.Background(), expected, r)
			if !errors.Is(err, tt.wantErr) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
				t.Errorf("UnsafePush() = %+v, %v; want empty, %v", got, err, tt.wantErr)
			}
			if tt.maxBytes > 0 && r.n > tt.maxBytes {
				t.Errorf("read %d bytes, want at most %d", r.n, tt.maxBytes)
			}
		})
	}
}

func TestManifestStore_UnsafePush_Limits(t *testing.T) {
	for _, tt := range []struct {
		name       string
		limit      int64
		size       int64
		bodySize   int
		wantErr    error
		wantUnread bool
	}{
		{name: "unknown size over cap", limit: 8, size: -1, bodySize: 20, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "known size over cap", limit: 8, size: 9, bodySize: 9, wantErr: errdef.ErrSizeExceedsLimit, wantUnread: true},
		{name: "known size at cap", limit: 8, size: 8, bodySize: 8},
		{name: "unknown size at cap", limit: 8, size: -1, bodySize: 8},
		{name: "default cap", size: -1, bodySize: 4<<20 + 1, wantErr: errdef.ErrSizeExceedsLimit},
		{name: "negative cap uses default", limit: -1, size: -1, bodySize: 4<<20 + 1, wantErr: errdef.ErrSizeExceedsLimit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// JSON whitespace keeps successful boundary bodies valid manifests.
			body := append([]byte("{}"), bytes.Repeat([]byte(" "), tt.bodySize-2)...)
			expected := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Size: tt.size}
			want := expected
			want.Size, want.Digest = int64(len(body)), digest.FromBytes(body)
			var steps []manifestUnsafeRequest
			if tt.wantErr == nil {
				steps = []manifestUnsafeRequest{manifestUnsafeHead(want, http.StatusNotFound), manifestUnsafePut(want, body, http.StatusCreated)}
			}
			repo := newManifestUnsafeRepo(t, steps...)
			repo.Registry.MaxMetadataBytes = tt.limit
			r := &manifestUnsafeReader{r: bytes.NewReader(body)}
			got, err := repo.manifests().UnsafePush(context.Background(), expected, r)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				want = ocispec.Descriptor{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("descriptor = %+v, want %+v", got, want)
			}
			limit := tt.limit
			if limit <= 0 {
				limit = 4 << 20
			}
			if int64(r.n) > limit+1 || (tt.wantUnread && r.calls != 0) {
				t.Errorf("reader: %d bytes, %d calls; limit %d, wantUnread %v", r.n, r.calls, limit, tt.wantUnread)
			}
		})
	}
}

func TestManifestStore_UnsafePush_Existing(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	want := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body)),
		Annotations: map[string]string{"example": "kept"}, ArtifactType: "application/example",
	}
	for _, tt := range []struct {
		name        string
		knownDigest bool
		knownSize   bool
		status      int
		different   bool
		badDigest   bool
		wantErr     error
	}{
		{name: "known duplicate", knownDigest: true, knownSize: true, status: http.StatusOK, wantErr: errdef.ErrAlreadyExists},
		{name: "computed duplicate", status: http.StatusOK, wantErr: errdef.ErrAlreadyExists},
		{name: "unknown size duplicate", knownDigest: true, status: http.StatusOK, wantErr: errdef.ErrAlreadyExists},
		{name: "unknown digest duplicate", knownSize: true, status: http.StatusOK, wantErr: errdef.ErrAlreadyExists},
		{name: "known different size", knownDigest: true, knownSize: true, status: http.StatusOK, different: true},
		{name: "computed different size", status: http.StatusOK, different: true},
		{name: "known HEAD failure", knownDigest: true, knownSize: true, status: http.StatusInternalServerError},
		{name: "computed HEAD failure", status: http.StatusInternalServerError},
		{name: "known HEAD digest mismatch", knownDigest: true, knownSize: true, status: http.StatusOK, badDigest: true, wantErr: content.ErrMismatchedDigest},
		{name: "computed HEAD digest mismatch", status: http.StatusOK, badDigest: true, wantErr: content.ErrMismatchedDigest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expected := want
			if !tt.knownDigest {
				expected.Digest = ""
			}
			if !tt.knownSize {
				expected.Size = -1
			}
			head := manifestUnsafeHead(want, tt.status)
			if tt.different {
				head.headers.Set("Content-Length", strconv.Itoa(len(body)+1))
			}
			if tt.badDigest {
				head.headers.Set("Docker-Content-Digest", digest.FromString("other").String())
			}
			steps := []manifestUnsafeRequest{head}
			if tt.different {
				steps = append(steps, manifestUnsafePut(want, body, http.StatusCreated))
			}
			repo := newManifestUnsafeRepo(t, steps...)
			r := &manifestUnsafeReader{r: bytes.NewReader(body)}
			got, err := repo.manifests().UnsafePush(context.Background(), expected, r)
			if tt.different {
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
				}
			} else {
				if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Errorf("error = %v, want error wrapping %v", err, tt.wantErr)
				}
				wantDesc := ocispec.Descriptor{}
				if tt.wantErr == errdef.ErrAlreadyExists {
					wantDesc = want
				}
				if !reflect.DeepEqual(got, wantDesc) {
					t.Errorf("descriptor = %+v, want %+v", got, wantDesc)
				}
				if tt.knownDigest && tt.knownSize && r.calls != 0 {
					t.Errorf("reader calls = %d, want 0", r.calls)
				}
			}
			if (!tt.knownDigest || !tt.knownSize || tt.different) && r.n != len(body) {
				t.Errorf("read %d bytes, want %d", r.n, len(body))
			}
		})
	}
}

func TestManifestStore_UnsafePush_Policy(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	want := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body))}
	for _, mode := range []string{"reference deny", "digest scope deny", "verifier deny", "verifier allow"} {
		for _, known := range []bool{false, true} {
			name := mode + "/computed"
			if known {
				name = mode + "/known"
			}
			t.Run(name, func(t *testing.T) {
				var steps []manifestUnsafeRequest
				if mode == "verifier allow" {
					steps = []manifestUnsafeRequest{manifestUnsafeHead(want, http.StatusNotFound), manifestUnsafePut(want, body, http.StatusCreated)}
				}
				repo := newManifestUnsafeRepo(t, steps...)
				var verifier *recordingSignedByVerifier
				switch mode {
				case "reference deny":
					repo.Registry.Policy = newRejectPolicyRepo(t).Registry.Policy
				case "digest scope deny":
					pol := &policy.Policy{
						Default: policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
						Transports: map[policy.TransportName]policy.TransportScopes{
							policy.TransportNameDocker: {
								repo.Reference().String() + "@" + want.Digest.String(): {&policy.Reject{}},
							},
						},
					}
					evaluator, err := policy.NewEvaluator(pol)
					if err != nil {
						t.Fatal(err)
					}
					repo.Registry.Policy = evaluator
				default:
					verifier = &recordingSignedByVerifier{allow: mode == "verifier allow"}
					repo.Registry.Policy = newSignedByEvaluator(t, verifier)
				}
				expected := want
				if !known {
					expected.Digest, expected.Size = "", -1
				}
				r := &manifestUnsafeReader{r: bytes.NewReader(body)}
				got, err := repo.manifests().UnsafePush(context.Background(), expected, r)
				if mode == "verifier allow" {
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Errorf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
					}
				} else {
					assertPolicyDenied(t, err, "UnsafePush")
					if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
						t.Errorf("descriptor = %+v, want empty", got)
					}
				}
				if verifier != nil {
					if len(verifier.seen) != 1 || verifier.seen[0].Digest != want.Digest {
						t.Errorf("policy calls = %+v, want one call for %s", verifier.seen, want.Digest)
					}
				}
				if mode == "reference deny" || (known && mode != "verifier allow") {
					if r.calls != 0 {
						t.Errorf("reader calls = %d, want 0", r.calls)
					}
				} else if r.n != len(body) {
					t.Errorf("read %d bytes, want %d", r.n, len(body))
				}
			})
		}
	}
}

func TestManifestStore_UnsafePush_Referrers(t *testing.T) {
	subject := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString("subject"), Size: 123}
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest, Subject: &subject,
		ArtifactType: "application/example", Annotations: map[string]string{"example": "indexed"},
	}
	manifest.SchemaVersion = 2
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := ocispec.Descriptor{
		MediaType: manifest.MediaType, Digest: digest.FromBytes(body), Size: int64(len(body)),
		ArtifactType: manifest.ArtifactType, Annotations: manifest.Annotations,
	}
	for _, mode := range []string{"fallback", "supported", "index rejected"} {
		t.Run(mode, func(t *testing.T) {
			put := manifestUnsafePut(want, body, http.StatusCreated)
			if mode == "supported" {
				put.headers.Set("OCI-Subject", subject.Digest.String())
			}
			steps := []manifestUnsafeRequest{manifestUnsafeHead(want, http.StatusNotFound), put}
			if mode != "supported" {
				indexPath := manifestUnsafePrefix + "sha256-" + subject.Digest.Encoded()
				indexPut := manifestUnsafeRequest{
					method: http.MethodPut, path: indexPath, status: http.StatusCreated,
					hashBody: true,
					check: func(r *http.Request, body []byte) {
						if r.Header.Get("Content-Type") != ocispec.MediaTypeImageIndex || r.ContentLength != int64(len(body)) {
							t.Errorf("index headers = %v, length = %d", r.Header, r.ContentLength)
						}
						var index ocispec.Index
						if err := json.Unmarshal(body, &index); err != nil {
							t.Errorf("decode index: %v", err)
						} else if !reflect.DeepEqual(index.Manifests, []ocispec.Descriptor{want}) {
							t.Errorf("index manifests = %+v, want [%+v]", index.Manifests, want)
						}
					},
				}
				if mode == "index rejected" {
					indexPut.status = http.StatusInternalServerError
				}
				steps = append(steps, manifestUnsafeRequest{method: http.MethodGet, path: indexPath, status: http.StatusNotFound}, indexPut)
			}
			// A fresh repository is required because referrers capability is sticky.
			repo := newManifestUnsafeRepo(t, steps...)
			expected := want
			expected.Digest, expected.Size = "", -1
			got, err := repo.manifests().UnsafePush(context.Background(), expected, bytes.NewReader(body))
			if mode == "index rejected" {
				if err == nil || !strings.Contains(err.Error(), "push failed:") || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
					t.Errorf("UnsafePush() = %+v, %v; want empty, wrapped indexing error", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
			}
		})
	}
}

func TestManifestStore_UnsafePush_PutRejected(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	want := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body))}
	repo := newManifestUnsafeRepo(t, manifestUnsafeHead(want, http.StatusNotFound), manifestUnsafePut(want, body, http.StatusBadRequest))
	got, err := repo.manifests().UnsafePush(context.Background(), ocispec.Descriptor{MediaType: want.MediaType, Size: -1}, bytes.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), "push failed:") || errors.Unwrap(err) == nil || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Errorf("UnsafePush() = %+v, %v; want empty, wrapped PUT error", got, err)
	}
}

func TestManifestStore_UnsafePush_AuthReplay(t *testing.T) {
	body := []byte(`{"layers":[]}`)
	want := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body))}
	challenge := manifestUnsafePut(want, body, http.StatusUnauthorized)
	challenge.headers.Set("WWW-Authenticate", `Basic realm="test"`)
	put := manifestUnsafePut(want, body, http.StatusCreated)
	put.check = func(r *http.Request, _ []byte) {
		if user, password, ok := r.BasicAuth(); !ok || user != "user" || password != "password" {
			t.Errorf("retry credentials = %q, %q, %v", user, password, ok)
		}
	}
	repo := newManifestUnsafeRepo(t, manifestUnsafeHead(want, http.StatusNotFound), challenge, put)
	repo.Registry.Client = &auth.Client{
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPut {
				if r.GetBody == nil {
					t.Error("PUT has no GetBody for auth replay")
				} else {
					rc, err := r.GetBody()
					if err != nil {
						t.Errorf("GetBody(): %v", err)
					} else {
						replay, err := io.ReadAll(rc)
						rc.Close()
						if err != nil || !bytes.Equal(replay, body) {
							t.Errorf("GetBody() = %q, %v; want %q, nil", replay, err, body)
						}
					}
				}
			}
			return http.DefaultTransport.RoundTrip(r)
		})},
		CredentialFunc: func(context.Context, properties.Resource) (credentials.Credential, error) {
			return credentials.Credential{Username: "user", Password: "password"}, nil
		},
	}
	got, err := repo.manifests().UnsafePush(context.Background(), ocispec.Descriptor{MediaType: want.MediaType, Size: -1}, bytes.NewReader(body))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("UnsafePush() = %+v, %v; want %+v, nil", got, err, want)
	}
}

func TestManifestStore_UnsafePush_CanceledDuringCopy(t *testing.T) {
	repo := newManifestUnsafeRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &unsafeCancelReader{data: `{"schemaVersion":2,"layers":[]}`, cancel: cancel, cancelAfter: 1}
	got, err := repo.manifests().UnsafePush(ctx, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Size: -1}, r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UnsafePush() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(got, ocispec.Descriptor{}) {
		t.Errorf("UnsafePush() desc = %+v, want empty", got)
	}
	if r.reads != 1 {
		t.Errorf("source read %d times, want 1", r.reads)
	}
}
