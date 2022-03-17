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

package cas

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
)

func TestMemorySuccess(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewMemory()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Memory.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Memory.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Memory.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Memory.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Memory.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Memory.Fetch() = %v, want %v", got, content)
	}
	if got := len(s.Map()); got != 1 {
		t.Errorf("Memory.Map() = %v, want %v", got, 1)
	}
}

func TestMemoryNotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewMemory()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Memory.Exists() error =", err)
	}
	if exists {
		t.Errorf("Memory.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Memory.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestMemoryAlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewMemory()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Memory.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestMemoryBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewMemory()
	ctx := context.Background()

	err := s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Memory.Push() error = %v, wantErr %v", err, true)
	}
}
