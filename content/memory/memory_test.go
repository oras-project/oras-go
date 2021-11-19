package memory

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go"
	"oras.land/oras-go/content"
	"oras.land/oras-go/errdef"
	"oras.land/oras-go/internal/cas"
	"oras.land/oras-go/internal/resolver"
)

func TestStoreInterface(t *testing.T) {
	var store interface{} = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(content.UpEdgeFinder); !ok {
		t.Error("&Store{} does not conform content.UpEdgeFinder")
	}
}

func TestStoreSuccess(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	internalResolver := s.resolver.(*resolver.Memory)
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Fatal("Store.Exists() error =", err)
	}
	if !exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, true)
	}

	rc, err := s.Fetch(ctx, desc)
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
	if !bytes.Equal(got, content) {
		t.Errorf("Store.Fetch() = %v, want %v", got, content)
	}
	internalStorage := s.storage.(*cas.Memory)
	if got := len(internalStorage.Map()); got != 1 {
		t.Errorf("storage.Map() = %v, want %v", got, 1)
	}
}

func TestStoreContentNotFound(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		t.Error("Store.Exists() error =", err)
	}
	if exists {
		t.Errorf("Store.Exists() = %v, want %v", exists, false)
	}

	_, err = s.Fetch(ctx, desc)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Fetch() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreContentAlreadyExists(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Push(ctx, desc, bytes.NewReader(content))
	if !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Errorf("Store.Push() error = %v, want %v", err, errdef.ErrAlreadyExists)
	}
}

func TestStoreContentBadPush(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s := New()
	ctx := context.Background()

	err := s.Push(ctx, desc, strings.NewReader("foobar"))
	if err == nil {
		t.Errorf("Store.Push() error = %v, wantErr %v", err, true)
	}
}

func TestStoreTagNotFound(t *testing.T) {
	ref := "foobar"

	s := New()
	ctx := context.Background()

	_, err := s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreTagUnknownContent(t *testing.T) {
	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	err := s.Tag(ctx, desc, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Store.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}

func TestStoreRepeatTag(t *testing.T) {
	generate := func(content []byte) ocispec.Descriptor {
		return ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
	}
	ref := "foobar"

	s := New()
	ctx := context.Background()

	// get internal resolver
	internalResolver := s.resolver.(*resolver.Memory)

	// initial tag
	content := []byte("hello world")
	desc := generate(content)
	err := s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("foo")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}

	// repeat tag
	content = []byte("bar")
	desc = generate(content)
	err = s.Push(ctx, desc, bytes.NewReader(content))
	if err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	err = s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Store.Tag() error =", err)
	}

	gotDesc, err = s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Store.Resolve() error =", err)
	}
	if !reflect.DeepEqual(gotDesc, desc) {
		t.Errorf("Store.Resolve() = %v, want %v", gotDesc, desc)
	}
	if got := len(internalResolver.Map()); got != 1 {
		t.Errorf("resolver.Map() = %v, want %v", got, 1)
	}
}
