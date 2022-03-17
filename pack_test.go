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
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"
)

func Test_Pack_Default(t *testing.T) {
	s := memory.New()

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

	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, layers, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	// test manifest
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
	if !reflect.DeepEqual(manifest.Layers, layers) {
		t.Errorf("Store.Fetch() = %v, want %v", manifest.Layers, layers)
	}

	// test config
	config := manifest.Config
	expected := []byte("{}")
	rc, err = s.Fetch(ctx, config)
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
	if !bytes.Equal(got, expected) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expected)
	}
}

func Test_Pack_NoLayer(t *testing.T) {
	s := memory.New()

	ctx := context.Background()
	manifestDesc, err := Pack(ctx, s, nil, PackOptions{})
	if err != nil {
		t.Fatal("Oras.Pack() error =", err)
	}

	// test manifest
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
	expectedLayers := []ocispec.Descriptor{}
	if !reflect.DeepEqual(manifest.Layers, expectedLayers) {
		t.Errorf("Store.Fetch() = %v, want %v", manifest.Layers, expectedLayers)
	}

	// test config
	config := manifest.Config
	expectedConfig := []byte("{}")
	rc, err = s.Fetch(ctx, config)
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
	if !bytes.Equal(got, expectedConfig) {
		t.Errorf("Store.Fetch() = %v, want %v", got, expectedConfig)
	}
}
