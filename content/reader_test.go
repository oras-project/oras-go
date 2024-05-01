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
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/spec"
)

func TestVerifyReader_NewVerifyReader(t *testing.T) {
	content := []byte("example content")

	// Default no-resume path
	r := bytes.NewReader(content)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
	}
	vr := NewVerifyReader(r, desc)
	if vr.resume {
		t.Fatalf("resume is set: %s", desc.Annotations)
	}

	// Resume is enabled and we expect to read something
	r = bytes.NewReader(content)
	desc = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
	vr = NewVerifyReader(r, desc)
	if !vr.resume {
		t.Fatalf("resume is not set: %+v", desc.Annotations)
	}

	// Resume is enabled and we expect to read something with hash
	d := digest.FromBytes(content)
	h, _ := EncodeHash(d.Algorithm().Hash())
	r = bytes.NewReader(content)
	desc = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    d,
		Size:      int64(len(content) + 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
			spec.AnnotationResumeHash:     h,
		},
	}
	vr = NewVerifyReader(r, desc)
	if !vr.resume {
		t.Fatalf("resume is not set: %+v", desc.Annotations)
	}
}

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

func TestVerifyReader_Read_Resume(t *testing.T) {
	// matched content and descriptor with small buffer
	content := []byte("example content")
	desc := NewDescriptorFromBytes("test", content)
	r := bytes.NewReader(content)
	vr := NewVerifyReader(r, desc)
	vr.resume = true
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
	vr.resume = true
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
	vr.resume = true
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

func TestVerifyReader_Verify_Resume(t *testing.T) {
	// matched content and descriptor
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
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
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) - 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
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
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
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
	desc = ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes([]byte("foo")),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
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

func TestReadAll_CorrectDescriptor_Resume(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
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

func TestReadAll_ReadSizeSmallerThanDescriptorSize_Resume(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAll() error = %v, want %v", err, io.EOF)
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

func TestReadAll_ReadSizeLargerThanDescriptorSize_Resume(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) - 1),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
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

func TestReadAll_InvalidDigest_Resume(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes([]byte("another content")),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrMismatchedDigest) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrMismatchedDigest)
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

func TestReadAll_EmptyContent_Resume(t *testing.T) {
	content := []byte("")
	desc := ocispec.Descriptor{
		MediaType: "test",
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
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

func TestReadAll_InvalidDescriptorSize_Resume(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      -1,
		Annotations: map[string]string{
			spec.AnnotationResumeDownload: "true",
			spec.AnnotationResumeOffset:   "3",
		},
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrInvalidDescriptorSize) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrInvalidDescriptorSize)
	}
}

func TestEncodeDecodeHash(t *testing.T) {
	content := []byte("example content")

	d := digest.FromBytes(content)
	eh, err := EncodeHash(d.Algorithm().Hash())
	if err != nil {
		t.Fatal("EncodeHash failed =", err)
	}
	if eh == "" {
		t.Fatal("EncodeHash returned empty")
	}

	dh, err := DecodeHash(eh, d)
	if err != nil {
		t.Fatal("DecodeHash failed =", err)
	}
	if !reflect.DeepEqual(dh, d.Algorithm().Hash()) {
		t.Fatalf("EncodeHash/DecodeHash error = %v, want %v", dh, d.Algorithm().Hash())
	}
}
