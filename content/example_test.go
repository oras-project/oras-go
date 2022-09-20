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

package content_test

import (
	"bytes"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

// ExampleVerifyReader gives an example of creating and using VerifyReader.
func ExampleVerifyReader() {
	blob := []byte("hello world")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob))}
	r := bytes.NewReader(blob)
	vr := content.NewVerifyReader(r, desc)
	buf := make([]byte, len(blob))

	if _, err := vr.Read(buf); err != nil {
		panic(err)
	}
	if err := vr.Verify(); err != nil {
		panic(err)
	}

	fmt.Println(string(buf))

	// Output:
	// hello world
}
