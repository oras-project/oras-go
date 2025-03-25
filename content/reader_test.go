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

package content

import (
	"bytes"
	"crypto/sha1"
	_ "crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestVerifyReader_Read(t *testing.T) {
	// matched content and descriptor with small buffer
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader(content)
	vr := NewVerifyReader(r, desc)
	buf := make([]byte, 5)
	n, err := vr.Read(buf)
	if err != nil {
		t.Fatal("Read() error = ", err)
	}
	if !bytes.Equal(buf, []byte("examp")) {
		t.Fatalf("incorrect read content: %s", buf)
	}
	if n != 5 {
		t.Fatalf("incorrect number of bytes read: %d", n)
	}

	// matched content and descriptor with sufficient buffer
	content = []byte("foo foo")
	desc = NewDescriptorFromBytes("test", content)
	r = bytes.NewReader(content)
	vr = NewVerifyReader(r, desc)
	buf = make([]byte, len(content))
	n, err = vr.Read(buf)
	if err != nil {
		t.Fatal("Read() error = ", err)
	}
	if n != len(content) {
		t.Fatalf("incorrect number of bytes read: %d", n)
	}
	if !bytes.Equal(buf, content) {
		t.Fatalf("incorrect read content: %s", buf)
	}

	// mismatched content and descriptor with sufficient buffer
	r = bytes.NewReader([]byte("bar"))
	vr = NewVerifyReader(r, desc)
	if err != nil {
		t.Fatal("NewVerifyReaderSafe() error = ", err)
	}
	buf = make([]byte, 5)
	n, err = vr.Read(buf)
	if err != nil {
		t.Fatal("Read() error = ", err)
	}
	if n != 3 {
		t.Fatalf("incorrect number of bytes read: %d", n)
	}
}

func TestVerifyReader_Verify(t *testing.T) {
	// matched content and descriptor
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader(content)
	vr := NewVerifyReader(r, desc)
	buf := make([]byte, len(content))
	if _, err := vr.Read(buf); err != nil {
		t.Fatal("Read() error = ", err)
	}
	if err := vr.Verify(); err != nil {
		t.Fatal("Verify() error = ", err)
	}
	if !bytes.Equal(buf, content) {
		t.Fatalf("incorrect read content: %s", buf)
	}

	// mismatched content and descriptor, read size larger than descriptor size
	content = []byte("foo")
	r = bytes.NewReader(content)
	desc = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)) - 1,
	}
	vr = NewVerifyReader(r, desc)
	buf = make([]byte, len(content))
	if _, err := vr.Read(buf); err != nil {
		t.Fatal("Read() error = ", err)
	}
	if err := vr.Verify(); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrTrailingData)
	}
	// call vr.Verify again, the result should be the same
	if err := vr.Verify(); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("2nd Verify() error = %v, want %v", err, ErrTrailingData)
	}

	// mismatched content and descriptor, read size smaller than descriptor size
	content = []byte("foo")
	r = bytes.NewReader(content)
	desc = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)) + 1,
	}
	vr = NewVerifyReader(r, desc)
	buf = make([]byte, len(content))
	if _, err := vr.Read(buf); err != nil {
		t.Fatal("Read() error = ", err)
	}
	if err := vr.Verify(); !errors.Is(err, errEarlyVerify) {
		t.Fatalf("Verify() error = %v, want %v", err, errEarlyVerify)
	}
	// call vr.Verify again, the result should be the same
	if err := vr.Verify(); !errors.Is(err, errEarlyVerify) {
		t.Fatalf("Verify() error = %v, want %v", err, errEarlyVerify)
	}

	// mismatched content and descriptor, wrong digest
	content = []byte("bar")
	r = bytes.NewReader(content)
	desc = NewDescriptorFromBytes("test", []byte("foo"))
	vr = NewVerifyReader(r, desc)
	buf = make([]byte, len(content))
	if _, err := vr.Read(buf); err != nil {
		t.Fatal("Read() error = ", err)
	}
	if err := vr.Verify(); !errors.Is(err, ErrMismatchedDigest) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrMismatchedDigest)
	}
	// call vr.Verify again, the result should be the same
	if err := vr.Verify(); !errors.Is(err, ErrMismatchedDigest) {
		t.Fatalf("2nd Verify() error = %v, want %v", err, ErrMismatchedDigest)
	}
}

func TestReadAll_CorrectDescriptor(t *testing.T) {
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader([]byte(content))
	got, err := ReadAll(r, desc)
	if err != nil {
		t.Fatal("ReadAll() error = ", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("ReadAll() = %v, want %v", got, content)
	}
}

func TestReadAll_ReadSizeSmallerThanDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1)}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("ReadAll() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestReadAll_ReadSizeLargerThanDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) - 1)}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrTrailingData) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrTrailingData)
	}
}

func TestReadAll_MismatchedDigest(t *testing.T) {
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", []byte("another content"))
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrMismatchedDigest) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrMismatchedDigest)
	}
}

func TestReadAll_InvalidDigest(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    "invalid-digest",
		Size:      int64(len(content)),
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if wantErr := digest.ErrDigestInvalidFormat; !errors.Is(err, wantErr) {
		t.Errorf("ReadAll() error = %v, want %v", err, wantErr)
	}
}

func TestReadAll_UnsupportedAlgorithm_SHA1(t *testing.T) {
	content := []byte("example content")
	h := sha1.New()
	h.Write(content)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.NewDigestFromBytes("sha1", h.Sum(nil)),
		Size:      int64(len(content)),
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if wantErr := digest.ErrDigestUnsupported; !errors.Is(err, wantErr) {
		t.Errorf("ReadAll() error = %v, want %v", err, wantErr)
	}
}

func TestReadAll_EmptyContent(t *testing.T) {
	content := []byte("")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader([]byte(content))
	got, err := ReadAll(r, desc)
	if err != nil {
		t.Fatal("ReadAll() error = ", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("ReadAll() = %v, want %v", got, content)
	}
}

func TestReadAll_InvalidDescriptorSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      -1,
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrInvalidDescriptorSize) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrInvalidDescriptorSize)
	}
}
