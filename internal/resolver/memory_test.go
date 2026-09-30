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

package resolver

import (
	"context"
	_ "crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/errdef"
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

	s.Untag(ref)
	_, err = s.Resolve(ctx, ref)
	if !errors.Is(err, errdef.ErrNotFound) {
		t.Errorf("Memory.Resolve() error = %v, want %v", err, errdef.ErrNotFound)
	}
	if got := len(s.Map()); got != 0 {
		t.Errorf("Memory.Map() = %v, want %v", got, 0)
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

func TestTagSet(t *testing.T) {
	refFoo := "foo"
	refBar := "bar"

	s := NewMemory()
	ctx := context.Background()

	content := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}

	s.Tag(ctx, desc, refFoo)
	s.Tag(ctx, desc, refBar)

	tagSet := s.TagSet(desc)

	if !tagSet.Contains(refFoo) {
		t.Fatalf("tagSet should contain %s", refFoo)
	}
	if !tagSet.Contains(refBar) {
		t.Fatalf("tagSet should contain %s", refFoo)
	}
	if len(tagSet) != 2 {
		t.Fatalf("expect size = %d, got %d", 2, len(tagSet))
	}
}

// Moving a reference to another descriptor must remove it from the tag set of
// the descriptor it previously pointed to.
func TestTagSetRetag(t *testing.T) {
	ref := "latest"

	s := NewMemory()
	ctx := context.Background()

	oldContent := []byte("foo")
	oldDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(oldContent),
		Size:      int64(len(oldContent)),
	}

	newContent := []byte("bar")
	newDesc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(newContent),
		Size:      int64(len(newContent)),
	}

	if err := s.Tag(ctx, oldDesc, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Tag(ctx, newDesc, ref); err != nil {
		t.Fatal(err)
	}

	if got := s.TagSet(oldDesc); got.Contains(ref) {
		t.Errorf("TagSet(oldDesc) = %v, want no %q after it moved to newDesc", got, ref)
	}
	if got := s.TagSet(newDesc); !got.Contains(ref) {
		t.Errorf("TagSet(newDesc) = %v, want it to contain %q", got, ref)
	}
}
