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

// Package oci provides access to an OCI content store.
// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc4/image-layout.md
package oci

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDeletableStore(t *testing.T) {
	content := []byte("test delete")
	desc := ocispec.Descriptor{
		MediaType: "test-delete",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "latest"

	tempDir := t.TempDir()
	s, err := NewDeletableStore(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	ctx := context.Background()

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, false)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	err = s.tag(ctx, desc, ref)
	if err != nil {
		t.Errorf("error tagging descriptor error = %v, wantErr %v", err, false)
	}

	resolvedDescr, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Errorf("error resolving descriptor error = %v, wantErr %v", err, false)
	}

	if !reflect.DeepEqual(resolvedDescr, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", resolvedDescr, desc)
	}

	err = s.Delete(ctx, desc)
	if err != nil {
		t.Errorf("Store.Delete() = %v, wantErr %v", err, true)
	}

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}
}
