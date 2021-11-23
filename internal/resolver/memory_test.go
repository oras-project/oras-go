package resolver

import (
	"context"
	_ "crypto/sha256"
	"errors"
	"reflect"
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
	ref := "foobar"

	s := NewMemory()
	ctx := context.Background()

	err := s.Tag(ctx, desc, ref)
	if err != nil {
		t.Fatal("Memory.Tag() error =", err)
	}

	got, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatal("Memory.Resolve() error =", err)
	}
	if !reflect.DeepEqual(got, desc) {
		t.Errorf("Memory.Resolve() = %v, want %v", got, desc)
	}
	if got := len(s.Map()); got != 1 {
		t.Errorf("Memory.Map() = %v, want %v", got, 1)
	}
}

func TestMemoryNotFound(t *testing.T) {
	ref := "foobar"

	s := NewMemory()
	ctx := context.Background()

	_, err := s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Memory.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
}
