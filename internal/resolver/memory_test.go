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
	"time"

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

func TestMemoryAll(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	for _, ref := range []string{"foo", "bar", "baz"} {
		content := []byte(ref)
		desc := ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		}
		if err := s.Tag(ctx, desc, ref); err != nil {
			t.Fatal(err)
		}
	}

	got := make(map[string]ocispec.Descriptor)
	for ref, desc := range s.All() {
		got[ref] = desc
	}
	if want := s.Map(); !reflect.DeepEqual(got, want) {
		t.Errorf("Memory.All() = %v, want %v", got, want)
	}

	// stopping early must release the read lock
	for range s.All() {
		break
	}
	done := make(chan struct{})
	go func() {
		s.Untag("foo")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Memory.Untag() blocked after breaking out of Memory.All()")
	}
}

func TestMemoryTagged(t *testing.T) {
	ctx := context.Background()
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes([]byte("tagged")),
		Size:      6,
	}
	s := NewMemory()

	if s.Tagged(desc) {
		t.Error("Memory.Tagged() = true for an unknown descriptor")
	}
	if err := s.Tag(ctx, desc, desc.Digest.String()); err != nil {
		t.Fatal(err)
	}
	if s.Tagged(desc) {
		t.Error("Memory.Tagged() = true with only the digest reference")
	}
	if err := s.Tag(ctx, desc, "latest"); err != nil {
		t.Fatal(err)
	}
	if !s.Tagged(desc) {
		t.Error("Memory.Tagged() = false with a tag")
	}
	s.Untag("latest")
	if s.Tagged(desc) {
		t.Error("Memory.Tagged() = true after the last tag was removed")
	}
}

// flushed returns the changes reported by Flush by reference.
func flushed(s *Memory) map[string]Change {
	got := make(map[string]Change)
	s.Flush(func(c Change) { got[c.Ref] = c })
	return got
}

func TestMemoryFlush(t *testing.T) {
	ctx := context.Background()
	newDesc := func(name string) ocispec.Descriptor {
		return ocispec.Descriptor{
			MediaType: "test",
			Digest:    digest.FromBytes([]byte(name)),
			Size:      int64(len(name)),
		}
	}
	d1, d2 := newDesc("one"), newDesc("two")
	dg1, dg2 := d1.Digest.String(), d2.Digest.String()

	s := NewMemory()
	for _, tag := range []struct {
		desc ocispec.Descriptor
		ref  string
	}{{d1, dg1}, {d1, "a"}, {d2, dg2}} {
		if err := s.Tag(ctx, tag.desc, tag.ref); err != nil {
			t.Fatal(err)
		}
	}

	// the first flush reports every reference
	got := flushed(s)
	if len(got) != 3 {
		t.Fatalf("first Flush() reported %d references, want 3: %v", len(got), got)
	}
	if c := got["a"]; !c.Exists || c.Desc.Digest != d1.Digest || !c.Tagged {
		t.Errorf("Flush() change of a = %+v, want an existing tagged reference to d1", c)
	}
	if c := got[dg2]; !c.Exists || c.Tagged {
		t.Errorf("Flush() change of the digest of d2 = %+v, want an existing untagged reference", c)
	}

	// nothing changed since
	if got := flushed(s); len(got) != 0 {
		t.Errorf("Flush() with no changes reported %v", got)
	}

	// tagging reports the tag and the digest reference, which may now be
	// covered by the tag
	if err := s.Tag(ctx, d2, "b"); err != nil {
		t.Fatal(err)
	}
	got = flushed(s)
	if len(got) != 2 || !got["b"].Exists || !got[dg2].Tagged {
		t.Errorf("Flush() after Tag = %v, want b, and the digest reference of d2 as tagged", got)
	}

	// moving a reference reports the digest references of both digests
	if err := s.Tag(ctx, d2, "a"); err != nil {
		t.Fatal(err)
	}
	got = flushed(s)
	if c := got["a"]; c.Desc.Digest != d2.Digest {
		t.Errorf("Flush() after moving a: a = %+v, want d2", c)
	}
	if c, ok := got[dg1]; !ok || c.Tagged {
		t.Errorf("Flush() after moving a: digest reference of d1 = %+v, %v, want untagged", c, ok)
	}
	if _, ok := got[dg2]; !ok {
		t.Error("Flush() after moving a did not report the digest reference of d2")
	}

	// untagging reports the reference as gone, and the digest reference as
	// untagged once its last tag is gone
	s.Untag("a")
	s.Untag("b")
	got = flushed(s)
	if c, ok := got["a"]; !ok || c.Exists {
		t.Errorf("Flush() after Untag: a = %+v, %v, want a reference that does not exist", c, ok)
	}
	if c := got[dg2]; !c.Exists || c.Tagged {
		t.Errorf("Flush() after Untag: digest reference of d2 = %+v, want untagged", c)
	}

	// a reference that changed repeatedly is reported once, as it is now
	for range 3 {
		if err := s.Tag(ctx, d1, "c"); err != nil {
			t.Fatal(err)
		}
		s.Untag("c")
	}
	if err := s.Tag(ctx, d2, "c"); err != nil {
		t.Fatal(err)
	}
	if c := flushed(s)["c"]; !c.Exists || c.Desc.Digest != d2.Digest {
		t.Errorf("Flush() after repeated changes: c = %+v, want d2", c)
	}
}

// TestMemoryFlushRecordsOnlyWhenUsed checks that a Memory that is never
// flushed does not accumulate changes.
func TestMemoryFlushRecordsOnlyWhenUsed(t *testing.T) {
	ctx := context.Background()
	desc := ocispec.Descriptor{MediaType: "test", Digest: digest.FromBytes([]byte("x")), Size: 1}
	s := NewMemory()
	for _, ref := range []string{"a", "b", "c"} {
		if err := s.Tag(ctx, desc, ref); err != nil {
			t.Fatal(err)
		}
		s.Untag(ref)
	}
	if s.changed != nil {
		t.Errorf("Memory recorded %d changes without Flush", len(s.changed))
	}
}
