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
	"io"

	_ "crypto/sha256"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

// ExampleVerifyReader gives an example of creating and using VerifyReader.
func ExampleVerifyReader() {
	blob := []byte("hello world")
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, blob)
	r := bytes.NewReader(blob)
	vr := content.NewVerifyReader(r, desc)
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, vr); err != nil {
		panic(err)
	}

	// note: users should not trust the the read content until
	// Verify() returns nil.
	if err := vr.Verify(); err != nil {
		panic(err)
	}

	fmt.Println(buf)

	// Output:
	// hello world
}
