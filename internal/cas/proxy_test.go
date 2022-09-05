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
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
)

func TestProxyCache(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxy(base, NewMemory())

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
	s.ReadOnlyStorage = nil

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

func TestProxy_FetchCached_NotCachedContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxy(base, NewMemory())

	// FetchCached should fetch from the base CAS
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	rc, err := s.FetchCached(ctx, desc)
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

	// the content should not exist in the cache
	exists, err = s.Cache.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Cache.Exists() error =", err)
	}

	if exists {
		t.Errorf("Proxy.Cache.Exists()() = %v, want %v", exists, false)
	}
}

func TestProxy_FetchCached_CachedContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxy(base, NewMemory())

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

	// the subsequent FetchCached should not touch base CAS
	// nil base will generate panic if the base CAS is touched
	s.ReadOnlyStorage = nil

	exists, err = s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}
	rc, err = s.FetchCached(ctx, desc)
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

func TestProxy_StopCaching(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxy(base, NewMemory())

	// FetchCached should fetch from the base CAS
	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Proxy.Exists() = %v, want %v", exists, true)
	}

	// test StopCaching
	s.StopCaching = true
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

	// the content should not exist in the cache
	exists, err = s.Cache.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Proxy.Cache.Exists() error =", err)
	}

	if exists {
		t.Errorf("Proxy.Cache.Exists()() = %v, want %v", exists, false)
	}
}

func TestProxyWithLimit_WithinLimit(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxyWithLimit(base, NewMemory(), 4*1024*1024)

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
	s.ReadOnlyStorage = nil

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

func TestProxyWithLimit_ExceedsLimit(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	ctx := context.Background()
	base := NewMemory()
	err := base.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Memory.Push() error =", err)
	}
	s := NewProxyWithLimit(base, NewMemory(), 1)

	// test fetch
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
	_, err = io.ReadAll(rc)
	if !errors.Is(err, errdef.ErrSizeExceedsLimit) {
		t.Fatalf("Proxy.Fetch().Read() error = %v, wantErr %v", err, errdef.ErrSizeExceedsLimit)
	}
}
