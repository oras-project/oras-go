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
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestProxyCache(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewProxy(NewMemory(), NewMemory())
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Proxy.Push() error =", err)
	}

	// first fetch
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	rc, err := s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Fetch() error =", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal("Proxy.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Proxy.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Proxy.Fetch() = %v, want %v", got, content)
	}

	// repeated fetch should not touch base CAS
	// nil base will generate panic if the base CAS is touched
	s.Storage = nil

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	rc, err = s.Fetch(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Fetch() error =", err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal("Proxy.Fetch().Read() error =", err)
	}
	err = rc.Close()
	if err != nil {
		t.Error("Proxy.Fetch().Close() error =", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Proxy.Fetch() = %v, want %v", got, content)
	}
}

func TestProxyPushPassThrough(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := NewProxy(NewMemory(), nil)
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Proxy.Push() error =", err)
	}
}
