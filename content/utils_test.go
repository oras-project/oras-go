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
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestReadAll(t *testing.T) {
	testContent := "example content"
	testDescriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes([]byte(testContent)),
		Size:      int64(len(testContent)),
	}
	r := bytes.NewReader([]byte(testContent))
	readContent, err := ReadAll(r, testDescriptor)
	if err != nil {
		t.Fatal("ReadAll error", err)
	}
	fmt.Println(string(readContent))
}
