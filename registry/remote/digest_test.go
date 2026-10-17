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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// unregisteredDigest is a well-formed digest whose algorithm has no hash
// implementation linked in, which is the only non-allowlisted algorithm that
// can be built on go-digest v1.0.0: that version exposes no way to register
// another one. It therefore exercises the same guard branch as an algorithm a
// future dependency registers without oras-go accepting it.
var unregisteredDigest = digest.Digest("sha1:" + strings.Repeat("a", 40))

func TestCheckDigestAlgorithm(t *testing.T) {
	for _, tt := range []struct {
		name    string
		digest  digest.Digest
		wantErr bool
	}{
		{"absent digest", "", false},
		{"sha256", digest.SHA256.FromString("content"), false},
		{"sha384", digest.SHA384.FromString("content"), false},
		{"sha512", digest.SHA512.FromString("content"), false},
		{"unregistered algorithm", unregisteredDigest, true},
		{"malformed without separator", digest.Digest("notadigest"), false},
		{"missing encoded portion", digest.Digest("sha256:"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkDigestAlgorithm(digestSourceReference, tt.digest)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("checkDigestAlgorithm() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), `"sha1"`) {
				t.Errorf("error = %v, want the rejected algorithm named", err)
			}
			if !strings.Contains(err.Error(), digestSourceReference) {
				t.Errorf("error = %v, want the digest source named", err)
			}
		})
	}
}

// TestCheckReferenceDigestAlgorithm covers the check manifestStore.resolve and
// fetchReference run before issuing a request: reference parsing only asks
// go-digest whether the algorithm is available, not whether oras-go accepts it.
func TestCheckReferenceDigestAlgorithm(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ref     properties.Reference
		wantErr bool
	}{
		{"tag only", properties.Reference{Tag: "latest"}, false},
		{"sha512 digest", properties.Reference{Digest: digest.SHA512.FromString("content").String()}, false},
		{"unregistered algorithm", properties.Reference{Digest: unregisteredDigest.String()}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkReferenceDigestAlgorithm(tt.ref)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("checkReferenceDigestAlgorithm() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), digestSourceReference) {
				t.Errorf("error = %v, want the digest source named", err)
			}
		})
	}
}

func TestParseSupportedDigest(t *testing.T) {
	valid := digest.SHA512.FromString("content")
	got, err := parseSupportedDigest(digestSourceHeader, valid.String())
	if err != nil || got != valid {
		t.Fatalf("parseSupportedDigest(sha512) = %v, %v; want %v, nil", got, err, valid)
	}

	_, err = parseSupportedDigest(digestSourceHeader, unregisteredDigest.String())
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("parseSupportedDigest(unregistered) = %v, want errdef.ErrUnsupported", err)
	}
	// The algorithm is checked before go-digest's parse, so a caller sees one
	// error shape whether the algorithm is unregistered today or registered by
	// a dependency later.
	if errors.Is(err, digest.ErrDigestUnsupported) {
		t.Errorf("error = %v, want the boundary error, not go-digest's parse error", err)
	}
	if !strings.Contains(err.Error(), digestSourceHeader) {
		t.Errorf("error = %v, want the digest source named", err)
	}

	if _, err := parseSupportedDigest(digestSourceHeader, "notadigest"); err == nil || errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("parseSupportedDigest(malformed) = %v, want a parse error", err)
	}
}

// TestManifestResolveUnsupportedHeaderAlgorithm covers the read path the issue
// calls out: Resolve and Exists used to turn whatever algorithm the registry
// named in Docker-Content-Digest into the descriptor's identity, even though
// Fetch then refuses that algorithm. The push and blob paths stay strict.
func TestManifestResolveUnsupportedHeaderAlgorithm(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		w.Header().Set(headerDockerContentDigest, unregisteredDigest.String())
		if r.Method != http.MethodHead {
			_, _ = w.Write(manifest)
		}
	}))
	defer server.Close()

	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	ctx := context.Background()

	for _, tt := range []struct {
		name string
		call func() error
	}{
		{"Resolve", func() error {
			_, err := repo.Resolve(ctx, "latest")
			return err
		}},
		{"FetchReference", func() error {
			_, rc, err := repo.FetchReference(ctx, "latest")
			if rc != nil {
				rc.Close()
			}
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, errdef.ErrUnsupported) {
				t.Fatalf("error = %v, want errdef.ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), digestSourceHeader) {
				t.Errorf("error = %v, want the header source named", err)
			}
			if !strings.Contains(err.Error(), `"sha1"`) {
				t.Errorf("error = %v, want the rejected algorithm named", err)
			}
			if errors.Is(err, digest.ErrDigestUnsupported) {
				t.Errorf("error = %v, want the boundary error, not go-digest's parse error", err)
			}
		})
	}
}

func TestManifestResolveAllowlistedHeaderStillAccepted(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"manifests":[]}`)
	headerDigest := digest.SHA256.FromBytes(manifest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		w.Header().Set(headerDockerContentDigest, headerDigest.String())
		if r.Method != http.MethodHead {
			_, _ = w.Write(manifest)
		}
	}))
	defer server.Close()

	repo, err := NewRepository(strings.TrimPrefix(server.URL, "http://") + "/test")
	if err != nil {
		t.Fatal(err)
	}
	repo.Registry.PlainHTTP = true
	desc, err := repo.Resolve(context.Background(), "latest")
	if err != nil {
		t.Fatalf("Resolve() = %v, want success", err)
	}
	if desc.Digest != headerDigest {
		t.Errorf("digest = %v, want %v", desc.Digest, headerDigest)
	}
}

func TestVerifyContentDigestUnsupportedHeaderAlgorithm(t *testing.T) {
	resp := &http.Response{
		Request: httptest.NewRequest(http.MethodPut, "http://example.com/v2/test/manifests/latest", nil),
		Header:  http.Header{},
	}
	resp.Header.Set(headerDockerContentDigest, unregisteredDigest.String())
	err := verifyContentDigest(resp, digest.SHA256.FromString("content"))
	if !errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("verifyContentDigest() = %v, want errdef.ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), digestSourceHeader) {
		t.Errorf("error = %v, want the header source named", err)
	}

	// A malformed header is still reported as such, not as unsupported.
	resp.Header.Set(headerDockerContentDigest, "sha256:invalid")
	if err := verifyContentDigest(resp, digest.SHA256.FromString("content")); err == nil || errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("verifyContentDigest(malformed) = %v, want a parse error", err)
	}
}
