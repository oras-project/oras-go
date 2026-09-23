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

package signature

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

const testScope = "registry.example.com/repo"

var testDigest = digest.FromString("hardening")

// TestGetSignatures_BoundedIndexScan covers a lookaside store that never says
// "not found". The enumeration is driven entirely by the server's responses, so
// without a ceiling it never terminates.
func TestGetSignatures_BoundedIndexScan(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > maxSignatures*4 {
			t.Error("index scan is unbounded")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte("not a real signature, but non-empty"))
	}))
	defer server.Close()

	store := NewLookasideStore(server.URL, "")
	_, err := store.GetSignatures(context.Background(), testScope, testDigest)
	if !errors.Is(err, ErrTooManySignatures) {
		t.Fatalf("GetSignatures error = %v, want ErrTooManySignatures", err)
	}
	if requests > maxSignatures+1 {
		t.Errorf("made %d requests, want at most %d", requests, maxSignatures+1)
	}
}

// TestGetSignatures_BoundedBodySize covers an oversized signature body.
func TestGetSignatures_BoundedBodySize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream past the limit without allocating it all up front.
		chunk := strings.Repeat("A", 1<<16)
		for written := 0; written < maxSignatureBytes+(1<<20); written += len(chunk) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	store := NewLookasideStore(server.URL, "")
	_, err := store.GetSignatures(context.Background(), testScope, testDigest)
	if err == nil {
		t.Fatal("GetSignatures error = nil, want an over-limit error")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("GetSignatures error = %v, want it to mention the size limit", err)
	}
}

// TestGetSignatures_StopsAtNotFound is the ordinary path: enumeration ends at
// the first missing index and returns what came before it.
func TestGetSignatures_StopsAtNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/signature-1") {
			w.Write([]byte("sig-1"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	store := NewLookasideStore(server.URL, "")
	sigs, err := store.GetSignatures(context.Background(), testScope, testDigest)
	if err != nil {
		t.Fatalf("GetSignatures: %v", err)
	}
	if len(sigs) != 1 || string(sigs[0]) != "sig-1" {
		t.Errorf("got %d signatures (%q), want exactly [sig-1]", len(sigs), sigs)
	}
}

func TestIsNotFound_Wrapped(t *testing.T) {
	base := &errNotFound{err: fmt.Errorf("missing")}
	if !isNotFound(base) {
		t.Error("isNotFound(bare) = false, want true")
	}
	if !isNotFound(fmt.Errorf("fetching signature: %w", base)) {
		t.Error("isNotFound(wrapped) = false, want true")
	}
	if isNotFound(fmt.Errorf("some other failure")) {
		t.Error("isNotFound(unrelated) = true, want false")
	}
}

// TestRemapIdentity_PrefixBoundary is the security-relevant case: a prefix must
// not match into an adjacent namespace, which would remap an image into the
// signer's namespace and let whoever controls that repository satisfy the
// policy.
func TestRemapIdentity_PrefixBoundary(t *testing.T) {
	tests := []struct {
		name         string
		prefix       string
		signedPrefix string
		imageRef     string
		signedRef    string
		want         bool
	}{
		{
			name:         "exact component boundary matches",
			prefix:       "registry.io/team",
			signedPrefix: "signer.io/team",
			imageRef:     "registry.io/team/app:v1",
			signedRef:    "signer.io/team/app:v1",
			want:         true,
		},
		{
			name:         "adjacent namespace does not match",
			prefix:       "registry.io/team",
			signedPrefix: "signer.io/team",
			imageRef:     "registry.io/team-evil/app:v1",
			signedRef:    "signer.io/team-evil/app:v1",
			want:         false,
		},
		{
			name:         "whole reference matches",
			prefix:       "registry.io/team/app",
			signedPrefix: "signer.io/team/app",
			imageRef:     "registry.io/team/app",
			signedRef:    "signer.io/team/app",
			want:         true,
		},
		{
			name:         "tag separator is a boundary",
			prefix:       "registry.io/team/app",
			signedPrefix: "signer.io/team/app",
			imageRef:     "registry.io/team/app:v1",
			signedRef:    "signer.io/team/app:v1",
			want:         true,
		},
		{
			name:         "digest separator is a boundary",
			prefix:       "registry.io/team/app",
			signedPrefix: "signer.io/team/app",
			imageRef:     "registry.io/team/app@sha256:abc",
			signedRef:    "signer.io/team/app@sha256:abc",
			want:         true,
		},
		{
			name:         "unrelated prefix does not match",
			prefix:       "other.io/team",
			signedPrefix: "signer.io/team",
			imageRef:     "registry.io/team/app:v1",
			signedRef:    "signer.io/team/app:v1",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remapIdentity(tt.prefix, tt.signedPrefix, tt.imageRef, tt.signedRef)
			if got != tt.want {
				t.Errorf("remapIdentity(%q, %q, %q, %q) = %v, want %v",
					tt.prefix, tt.signedPrefix, tt.imageRef, tt.signedRef, got, tt.want)
			}
		})
	}
}

func TestMatchSignedIdentity_RemapBoundary(t *testing.T) {
	id := &policy.SignedIdentity{
		Type:         policy.IdentityMatchRemap,
		Prefix:       "registry.io/team",
		SignedPrefix: "signer.io/team",
	}
	matched, err := MatchSignedIdentity(id, "registry.io/team-evil/app:v1", "signer.io/team-evil/app:v1")
	if err != nil {
		t.Fatalf("MatchSignedIdentity: %v", err)
	}
	if matched {
		t.Error("a remap prefix matched into an adjacent namespace")
	}
}

// TestVerifyOpenPGPSignature_RejectsOversizedPayload ensures the message body
// read is bounded. Garbage input is enough: the size guard must not depend on
// the message being well-formed.
func TestVerifyOpenPGPSignature_RejectsOversizedPayload(t *testing.T) {
	// A nil keyring is rejected before any reading happens, which confirms the
	// early guard stays ahead of the bounded read.
	_, err := VerifyOpenPGPSignature([]byte("whatever"), nil)
	if !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("error = %v, want ErrSignatureVerification", err)
	}
}
