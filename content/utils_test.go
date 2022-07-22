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
	_ "crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestReadAllCorrectDescriptor(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content))}
	r := bytes.NewReader([]byte(content))
	got, err := ReadAll(r, desc)
	if err != nil {
		t.Fatal("ReadAll() error = ", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("ReadAll() = %v, want %v", got, content)
	}
}

func TestReadAllReadSizeSmallerThanDescriptorSize(t *testing.T) {
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

func TestReadAllReadSizeLargerThanDescriptorSize(t *testing.T) {
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

func TestReadAllInvalidDigest(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes([]byte("wrong content")),
		Size:      int64(len(content))}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || !errors.Is(err, ErrMismatchedDigest) {
		t.Errorf("ReadAll() error = %v, want %v", err, ErrMismatchedDigest)
	}
}

func TestReadAllEmptyContent(t *testing.T) {
	content := []byte("")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
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

func TestReadAllInvalidDescriptorSize(t *testing.T) {
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
