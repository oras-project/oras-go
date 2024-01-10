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

package registry_test

import (
	_ "crypto/sha256" // required to parse sha256 digest. See [Reference.Digest]
	"fmt"

	"oras.land/oras-go/v2/registry"
)

// ExampleParseReference_digest demonstrates parsing a reference string with
// digest and print its components.
func ExampleParseReference_digest() {
	rawRef := "ghcr.io/oras-project/oras-go@sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76"
	ref, err := registry.ParseReference(rawRef)
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", ref.Registry)
	fmt.Println("Repository:", ref.Repository)

	digest, err := ref.Digest()
	if err != nil {
		panic(err)
	}
	fmt.Println("Digest:", digest)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras-go
	// Digest: sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76
}
