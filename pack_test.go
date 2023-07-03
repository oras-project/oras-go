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

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/internal/spec"
)

func Test_Pack_Default(t *testing.T) {
	s := memory.New()

	// prepare test content
	blobs := []ocispec.Descriptor{
		content.NewDescriptorFromBytes("test", []byte("hello world")),
		content.NewDescriptorFromBytes("test", []byte("goodbye world")),
	}
	artifactType := "application/vnd.test"

	// test Pack
	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, artifactType, blobs, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	// test blobs
	var manifest spec.Artifact
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
	if got := manifest.MediaType; got != spec.MediaTypeArtifactManifest {
		t.Fatalf("got media type = %s, want %s", got, spec.MediaTypeArtifactManifest)
	}

	// test artifact type
	if got := manifest.ArtifactType; got != artifactType {
		t.Fatalf("got artifact type = %s, want %s", got, artifactType)
	}

	// test created time annotation
	createdTime, ok := manifest.Annotations[spec.AnnotationArtifactCreated]
	if !ok {
		t.Errorf("Annotation %s = %v, want %v", spec.AnnotationArtifactCreated, ok, true)
	}
	_, err = time.Parse(time.RFC3339, createdTime)
	if err != nil {
		t.Errorf("error parsing created time: %s, error = %v", createdTime, err)
	}
}

func Test_Pack_WithOptions(t *testing.T) {
	s := memory.New()

	// prepare test content
	blobs := []ocispec.Descriptor{
		content.NewDescriptorFromBytes("test", []byte("hello world")),
		content.NewDescriptorFromBytes("test", []byte("goodbye world")),
	}

	artifactType := "application/vnd.test"
	annotations := map[string]string{
		spec.AnnotationArtifactCreated: "2000-01-01T00:00:00Z",
	}
	subjectManifest := []byte(`{"layers":[]}`)
	subjectDesc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageManifest,
		Digest:       digest.FromBytes(subjectManifest),
		Size:         int64(len(subjectManifest)),
		ArtifactType: artifactType,
		Annotations:  annotations,
	}

	// test Pack
	ctx := context.Background()
	opts := PackOptions{
		Subject:             &subjectDesc,
		ManifestAnnotations: annotations,
	}
	manifestDesc, err := Pack(ctx, s, artifactType, blobs, opts)
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedManifest := spec.Artifact{
		MediaType:    spec.MediaTypeArtifactManifest,
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

func Test_Pack_NoBlob(t *testing.T) {
	s := memory.New()

	// test Pack
	ctx := context.Background()
	artifactType := "application/vnd.test"
	manifestDesc, err := Pack(ctx, s, artifactType, nil, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest spec.Artifact
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

	// test blobs
	var expectedBlobs []ocispec.Descriptor
	if !reflect.DeepEqual(manifest.Blobs, expectedBlobs) {
		t.Errorf("Store.Fetch() = %v, want %v", manifest.Blobs, expectedBlobs)
	}
}

func Test_Pack_NoArtifactType(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, "", nil, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest spec.Artifact
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

	// test artifact type
	if manifestDesc.ArtifactType != MediaTypeUnknownArtifact {
		t.Fatalf("got artifact type = %s, want %s", manifestDesc.ArtifactType, MediaTypeUnknownArtifact)
	}
	if manifest.ArtifactType != MediaTypeUnknownArtifact {
		t.Fatalf("got artifact type = %s, want %s", manifest.ArtifactType, MediaTypeUnknownArtifact)
	}
}

func Test_Pack_InvalidDateTimeFormat(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	opts := PackOptions{
		ManifestAnnotations: map[string]string{
			spec.AnnotationArtifactCreated: "2000/01/01 00:00:00",
		},
	}
	artifactType := "application/vnd.test"
	_, err := Pack(ctx, s, artifactType, nil, opts)
	if err == nil || !errors.Is(err, ErrInvalidDateTimeFormat) {
		t.Errorf("Oras.Pack() error = %v, wantErr = %v", err, ErrInvalidDateTimeFormat)
	}
}

func Test_Pack_Image(t *testing.T) {
	s := memory.New()

	// prepare test content
	layers := []ocispec.Descriptor{
		content.NewDescriptorFromBytes("test", []byte("hello world")),
		content.NewDescriptorFromBytes("test", []byte("goodbye world")),
	}

	// test Pack
	ctx := context.Background()
	artifactType := "testconfig"
	manifestDesc, err := Pack(ctx, s, artifactType, layers, PackOptions{PackImageManifest: true})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest ocispec.Manifest
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
	if got != ocispec.MediaTypeImageManifest {
		t.Fatalf("got media type = %s, want %s", got, ocispec.MediaTypeImageManifest)
	}

	// test config
	expectedConfigBytes := []byte("{}")
	expectedConfig := ocispec.Descriptor{
		MediaType: artifactType,
		Digest:    digest.FromBytes(expectedConfigBytes),
		Size:      int64(len(expectedConfigBytes)),
	}
	if !reflect.DeepEqual(manifest.Config, expectedConfig) {
		t.Errorf("got config = %v, want %v", manifest.Config, expectedConfig)
	}

	// test layers
	if !reflect.DeepEqual(manifest.Layers, layers) {
		t.Errorf("got layers = %v, want %v", manifest.Layers, layers)
	}

	// test created time annotation
	createdTime, ok := manifest.Annotations[ocispec.AnnotationCreated]
	if !ok {
		t.Errorf("Annotation %s = %v, want %v", ocispec.AnnotationCreated, ok, true)
	}
	_, err = time.Parse(time.RFC3339, createdTime)
	if err != nil {
		t.Errorf("error parsing created time: %s, error = %v", createdTime, err)
	}
}

func Test_Pack_Image_WithOptions(t *testing.T) {
	s := memory.New()

	// prepare test content
	layers := []ocispec.Descriptor{
		content.NewDescriptorFromBytes("test", []byte("hello world")),
		content.NewDescriptorFromBytes("test", []byte("goodbye world")),
	}
	configBytes := []byte("{}")
	configDesc := content.NewDescriptorFromBytes("testconfig", configBytes)
	configAnnotations := map[string]string{"foo": "bar"}
	annotations := map[string]string{
		ocispec.AnnotationCreated: "2000-01-01T00:00:00Z",
	}
	artifactType := "application/vnd.test"
	subjectManifest := []byte(`{"layers":[]}`)
	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(subjectManifest),
		Size:      int64(len(subjectManifest)),
	}

	// test Pack with ConfigDescriptor
	ctx := context.Background()
	opts := PackOptions{
		PackImageManifest:   true,
		Subject:             &subjectDesc,
		ConfigDescriptor:    &configDesc,
		ConfigAnnotations:   configAnnotations,
		ManifestAnnotations: annotations,
	}
	manifestDesc, err := Pack(ctx, s, artifactType, layers, opts)
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedManifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:   ocispec.MediaTypeImageManifest,
		Subject:     &subjectDesc,
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
		t.Errorf("Store.Fetch() = %v, want %v", string(got), string(expectedManifestBytes))
	}

	// test Pack without ConfigDescriptor
	opts = PackOptions{
		PackImageManifest:   true,
		Subject:             &subjectDesc,
		ConfigAnnotations:   configAnnotations,
		ManifestAnnotations: annotations,
	}
	manifestDesc, err = Pack(ctx, s, artifactType, layers, opts)
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	expectedConfigDesc := content.NewDescriptorFromBytes(artifactType, configBytes)
	expectedConfigDesc.Annotations = configAnnotations
	expectedManifest = ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		MediaType:   ocispec.MediaTypeImageManifest,
		Subject:     &subjectDesc,
		Config:      expectedConfigDesc,
		Layers:      layers,
		Annotations: annotations,
	}
	expectedManifestBytes, err = json.Marshal(expectedManifest)
	if err != nil {
		t.Fatal("failed to marshal manifest:", err)
	}

	rc, err = s.Fetch(ctx, manifestDesc)
	if err != nil {
		t.Fatal("Store.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Store.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Store.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, expectedManifestBytes) {
		t.Errorf("Store.Fetch() = %v, want %v", string(got), string(expectedManifestBytes))
	}
}

func Test_Pack_Image_NoArtifactType(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, "", nil, PackOptions{PackImageManifest: true})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest ocispec.Manifest
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

	// test artifact type and config media type
	if manifestDesc.ArtifactType != MediaTypeUnknownConfig {
		t.Fatalf("got artifact type = %s, want %s", manifestDesc.ArtifactType, MediaTypeUnknownConfig)
	}
	if manifest.Config.MediaType != MediaTypeUnknownConfig {
		t.Fatalf("got artifact type = %s, want %s", manifest.Config.MediaType, MediaTypeUnknownConfig)
	}
}

func Test_Pack_Image_NoLayer(t *testing.T) {
	s := memory.New()

	// test Pack
	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, "", nil, PackOptions{PackImageManifest: true})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	var manifest ocispec.Manifest
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

	// test layers
	expectedLayers := []ocispec.Descriptor{}
	if !reflect.DeepEqual(manifest.Layers, expectedLayers) {
		t.Errorf("got layers = %v, want %v", manifest.Layers, expectedLayers)
	}
}

func Test_Pack_Image_InvalidDateTimeFormat(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	opts := PackOptions{
		PackImageManifest: true,
		ManifestAnnotations: map[string]string{
			ocispec.AnnotationCreated: "2000/01/01 00:00:00",
		},
	}
	_, err := Pack(ctx, s, "", nil, opts)
	if err == nil || !errors.Is(err, ErrInvalidDateTimeFormat) {
		t.Errorf("Oras.Pack() error = %v, wantErr = %v", err, ErrInvalidDateTimeFormat)
	}
}
