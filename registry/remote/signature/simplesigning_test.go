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
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestNewSimpleSigningPayload(t *testing.T) {
	dgst := digest.FromString("test content")
	ref := "registry.example.com/namespace/repo:latest"

	payload := NewSimpleSigningPayload(dgst, ref)

	if payload.Critical.Type != simpleSigningType {
		t.Errorf("Type = %v, want %v", payload.Critical.Type, simpleSigningType)
	}
	if payload.Critical.Image.DockerManifestDigest != dgst.String() {
		t.Errorf("DockerManifestDigest = %v, want %v", payload.Critical.Image.DockerManifestDigest, dgst.String())
	}
	if payload.Critical.Identity.DockerReference != ref {
		t.Errorf("DockerReference = %v, want %v", payload.Critical.Identity.DockerReference, ref)
	}
}

func TestSimpleSigningPayload_MarshalAndParse(t *testing.T) {
	dgst := digest.FromString("test content")
	ref := "registry.example.com/namespace/repo:latest"

	original := NewSimpleSigningPayload(dgst, ref)
	original.Optional.Creator = "test-tool"
	original.Optional.Timestamp = 1700000000

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	parsed, err := ParseSimpleSigningPayload(data)
	if err != nil {
		t.Fatalf("ParseSimpleSigningPayload() error: %v", err)
	}

	if parsed.Critical.Type != original.Critical.Type {
		t.Errorf("Type = %v, want %v", parsed.Critical.Type, original.Critical.Type)
	}
	if parsed.Critical.Image.DockerManifestDigest != original.Critical.Image.DockerManifestDigest {
		t.Errorf("Digest = %v, want %v", parsed.Critical.Image.DockerManifestDigest, original.Critical.Image.DockerManifestDigest)
	}
	if parsed.Critical.Identity.DockerReference != original.Critical.Identity.DockerReference {
		t.Errorf("Reference = %v, want %v", parsed.Critical.Identity.DockerReference, original.Critical.Identity.DockerReference)
	}
	if parsed.Optional.Creator != original.Optional.Creator {
		t.Errorf("Creator = %v, want %v", parsed.Optional.Creator, original.Optional.Creator)
	}
	if parsed.Optional.Timestamp != original.Optional.Timestamp {
		t.Errorf("Timestamp = %v, want %v", parsed.Optional.Timestamp, original.Optional.Timestamp)
	}
}

func TestParseSimpleSigningPayload_InvalidJSON(t *testing.T) {
	_, err := ParseSimpleSigningPayload([]byte("not json{{{"))
	if err == nil {
		t.Fatal("ParseSimpleSigningPayload() should return error for invalid JSON")
	}
	if !errors.Is(err, ErrInvalidSigningPayload) {
		t.Errorf("error should wrap ErrInvalidSigningPayload, got %v", err)
	}
}

func TestSimpleSigningPayload_Validate(t *testing.T) {
	dgst := digest.FromString("test content")

	tests := []struct {
		name    string
		payload *SimpleSigningPayload
		wantErr bool
	}{
		{
			name:    "Valid payload",
			payload: NewSimpleSigningPayload(dgst, "registry.example.com/repo:latest"),
			wantErr: false,
		},
		{
			name: "Wrong type",
			payload: &SimpleSigningPayload{
				Critical: SimpleSigningCritical{
					Type:     "wrong type",
					Image:    SimpleSigningImage{DockerManifestDigest: dgst.String()},
					Identity: SimpleSigningIdentity{DockerReference: "ref"},
				},
			},
			wantErr: true,
		},
		{
			name: "Missing digest",
			payload: &SimpleSigningPayload{
				Critical: SimpleSigningCritical{
					Type:     simpleSigningType,
					Image:    SimpleSigningImage{DockerManifestDigest: ""},
					Identity: SimpleSigningIdentity{DockerReference: "ref"},
				},
			},
			wantErr: true,
		},
		{
			name: "Missing reference",
			payload: &SimpleSigningPayload{
				Critical: SimpleSigningCritical{
					Type:     simpleSigningType,
					Image:    SimpleSigningImage{DockerManifestDigest: dgst.String()},
					Identity: SimpleSigningIdentity{DockerReference: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "Invalid digest format",
			payload: &SimpleSigningPayload{
				Critical: SimpleSigningCritical{
					Type:     simpleSigningType,
					Image:    SimpleSigningImage{DockerManifestDigest: "not-a-digest"},
					Identity: SimpleSigningIdentity{DockerReference: "ref"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSimpleSigningPayload_ImageDigest(t *testing.T) {
	dgst := digest.FromString("test content")
	payload := NewSimpleSigningPayload(dgst, "ref")

	got, err := payload.ImageDigest()
	if err != nil {
		t.Fatalf("ImageDigest() error: %v", err)
	}
	if got != dgst {
		t.Errorf("ImageDigest() = %v, want %v", got, dgst)
	}
}

func TestSimpleSigningPayload_DockerReference(t *testing.T) {
	ref := "registry.example.com/repo:latest"
	payload := NewSimpleSigningPayload(digest.FromString("test"), ref)

	if got := payload.DockerReference(); got != ref {
		t.Errorf("DockerReference() = %v, want %v", got, ref)
	}
}
