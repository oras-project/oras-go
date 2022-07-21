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
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestReadAllCorrectDescriptor(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content)),
	}
	r := bytes.NewReader([]byte(content))
	got, err := ReadAll(r, desc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("Incorrect content")
	}
}

func TestReadAllWrongSize(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(content),
		Size:      int64(len(content) + 1),
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || err != ErrUnexpectedEOF {
		t.Fatal("expected err = ErrUnexpectedEOF, got error = ", err)
	}
}

func TestReadAllWrongDigest(t *testing.T) {
	content := []byte("example content")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes([]byte("wrong content")),
		Size:      int64(len(content)),
	}
	r := bytes.NewReader([]byte(content))
	_, err := ReadAll(r, desc)
	if err == nil || err != ErrMismatchedDigest {
		t.Fatal("expected err = ErrMismatchedDigest, got error = ", err)
	}
}
