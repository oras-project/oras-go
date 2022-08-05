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

package oras

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"
)

func Test_Pack_Default(t *testing.T) {
	s := memory.New()

	// prepare test content
	layer_1 := []byte("hello world")
	desc_1 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(layer_1),
		Size:      int64(len(layer_1)),
	}

	layer_2 := []byte("goodbye world")
	desc_2 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(layer_2),
		Size:      int64(len(layer_2)),
	}
	layers := []ocispec.Descriptor{
		desc_1,
		desc_2,
	}

	// test Pack
	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, layers, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: MediaTypeUnknownConfig,
			Digest:    "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Size:      0,
		},
		Layers: layers,
	}
	expectedManifestBytes, err := json.Marshal(expectedManifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}

	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, expectedManifestBytes) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expectedManifestBytes)
	}
}

func Test_Pack_WithOptions(t *testing.T) {
	s := memory.New()

	// prepare test content
	layer_1 := []byte("hello world")
	desc_1 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(layer_1),
		Size:      int64(len(layer_1)),
	}

	layer_2 := []byte("goodbye world")
	desc_2 := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(layer_2),
		Size:      int64(len(layer_2)),
	}
	layers := []ocispec.Descriptor{
		desc_1,
		desc_2,
	}
	configBytes := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: MediaTypeUnknownConfig,
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}
	annotations := map[string]string{
		"foo": "bar",
	}

	// test Pack
	ctx := context.Background()
	opts := PackOptions{
		ConfigDescriptor:    &configDesc,
		ConfigAnnotations:   annotations,
		ConfigMediaType:     ocispec.MediaTypeImageConfig,
		ManifestAnnotations: annotations,
	}
	manifestDesc, err := Pack(ctx, s, layers, opts)
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:   ocispec.MediaTypeImageManifest,
		Config:      configDesc,
		Layers:      layers,
		Annotations: annotations,
	}
	expectedManifestBytes, err := json.Marshal(expectedManifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}

	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, expectedManifestBytes) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expectedManifestBytes)
	}
}

func Test_Pack_NoLayer(t *testing.T) {
	s := memory.New()

	// test Pack
	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, nil, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: MediaTypeUnknownConfig,
			Digest:    "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Size:      0,
		},
		Layers: []ocispec.Descriptor{},
	}
	expectedManifestBytes, err := json.Marshal(expectedManifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}

	// test manifest
	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, expectedManifestBytes) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expectedManifestBytes)
	}
}

func Test_PackArtifact_Default(t *testing.T) {
	s := memory.New()

	// prepare test content
	blob_1 := []byte("hello world")
	desc_1 := artifactspec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob_1),
		Size:      int64(len(blob_1)),
	}

	blob_2 := []byte("goodbye world")
	desc_2 := artifactspec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob_2),
		Size:      int64(len(blob_2)),
	}
	blobs := []artifactspec.Descriptor{
		desc_1,
		desc_2,
	}
	artifactType := "application/vnd.test"

	// test PackArtifact
	ctx := context.Background()
	manifestDesc, err := PackArtifact(ctx, s, artifactType, blobs, PackArtifactOptions{})
	if err != nil {
		t.Fatal("Oras.PackArtifact() error =", err)
	}

	// test blobs
	var manifest artifactspec.Manifest
	rc, err := s.Fetch(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatal("error decoding manifest, error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("Store.Fetch().Close() error =", err)
	}
	if !reflect.DeepEqual(manifest.Blobs, blobs) {
		t.Errorf("Store.Fetch() = %v, want %v", manifest.Blobs, blobs)
	}

	// test media type
	got := manifest.MediaType
	if got != artifactspec.MediaTypeArtifactManifest {
		t.Fatalf("got media type = %s, want %s", got, artifactspec.MediaTypeArtifactManifest)
	}

	// test artifact type
	got = manifest.ArtifactType
	if got != artifactType {
		t.Fatalf("got artifact type = %s, want %s", got, artifactType)
	}

	// test created time annotation
	createdTime, ok := manifest.Annotations[artifactspec.AnnotationArtifactCreated]
	if !ok {
		t.Errorf("Annotation %s = %v, want %v", artifactspec.AnnotationArtifactCreated, ok, true)
	}
	_, err = time.Parse(time.RFC3339, createdTime)
	if err != nil {
		t.Errorf("error parsing created time: %s, error = %v", createdTime, err)
	}
}

func Test_PackArtifact_WithOptions(t *testing.T) {
	s := memory.New()

	// prepare test content
	blob_1 := []byte("hello world")
	desc_1 := artifactspec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob_1),
		Size:      int64(len(blob_1)),
	}

	blob_2 := []byte("goodbye world")
	desc_2 := artifactspec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(blob_2),
		Size:      int64(len(blob_2)),
	}
	blobs := []artifactspec.Descriptor{
		desc_1,
		desc_2,
	}

	artifactType := "application/vnd.test"
	subjectManifest := []byte(`{"layers":[]}`)
	subjectDesc := artifactspec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(subjectManifest),
		Size:      int64(len(subjectManifest)),
	}
	annotations := map[string]string{
		artifactspec.AnnotationArtifactCreated: "2000-01-01T00:00:00Z",
	}

	// test PackArtifact
	ctx := context.Background()
	opts := PackArtifactOptions{
		Subject:             &subjectDesc,
		ManifestAnnotations: annotations,
	}
	manifestDesc, err := PackArtifact(ctx, s, artifactType, blobs, opts)
	if err != nil {
		t.Fatal("Oras.PackArtifact() error =", err)
	}

	expectedManifest := artifactspec.Manifest{
		MediaType:    artifactspec.MediaTypeArtifactManifest,
		ArtifactType: artifactType,
		Blobs:        blobs,
		Subject:      opts.Subject,
		Annotations:  annotations,
	}
	expectedManifestBytes, err := json.Marshal(expectedManifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}

	// test manifest
	rc, err := s.Fetch(ctx, manifestDesc)
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
	if !bytes.Equal(got, expectedManifestBytes) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expectedManifestBytes)
	}
}

func Test_PackArtifact_NoBlob(t *testing.T) {
	s := memory.New()

	// test Pack
	ctx := context.Background()
	artifactType := "application/vnd.test"
	manifestDesc, err := PackArtifact(ctx, s, artifactType, nil, PackArtifactOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest artifactspec.Manifest
	rc, err := s.Fetch(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatal("error decoding manifest, error =", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal("Store.Fetch().Close() error =", err)
	}

	// test media type
	got := manifest.MediaType
	if got != artifactspec.MediaTypeArtifactManifest {
		t.Fatalf("got media type = %s, want %s", got, artifactspec.MediaTypeArtifactManifest)
	}

	// test artifact type
	got = manifest.ArtifactType
	if got != artifactType {
		t.Fatalf("got artifact type = %s, want %s", got, artifactType)
	}

	// test created time annotation
	createdTime, ok := manifest.Annotations[artifactspec.AnnotationArtifactCreated]
	if !ok {
		t.Errorf("Annotation %s = %v, want %v", artifactspec.AnnotationArtifactCreated, ok, true)
	}
	_, err = time.Parse(time.RFC3339, createdTime)
	if err != nil {
		t.Errorf("error parsing created time: %s, error = %v", createdTime, err)
	}
}

func Test_PackArtifact_MissingArtifactType(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	_, err := PackArtifact(ctx, s, "", nil, PackArtifactOptions{})
	if err == nil || !errors.Is(err, ErrMissingArtifactType) {
		t.Errorf("Oras.Pack() error = %v, wantErr = %v", err, ErrMissingArtifactType)
	}
}

func Test_PackArtifact_InvalidDateTimeFormat(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	opts := PackArtifactOptions{
		ManifestAnnotations: map[string]string{
			artifactspec.AnnotationArtifactCreated: "2000/01/01 00:00:00",
		},
	}
	artifactType := "application/vnd.test"
	_, err := PackArtifact(ctx, s, artifactType, nil, opts)
	if err == nil || !errors.Is(err, ErrInvalidDateTimeFormat) {
		t.Errorf("Oras.Pack() error = %v, wantErr = %v", err, ErrInvalidDateTimeFormat)
	}
}
