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

package properties_test

import (
	_ "crypto/sha256" // required to parse sha256 digest. See [Reference.Digest]
	"fmt"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// ExampleNewReference demonstrates parsing a reference string into a Reference.
func ExampleNewReference() {
	ref, err := properties.NewReference("ghcr.io/oras-project/oras-go:v3.0.0")
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", ref.Registry)
	fmt.Println("Repository:", ref.Repository)
	fmt.Println("Tag:", ref.Tag)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras-go
	// Tag: v3.0.0
}

// ExampleNewReference_digest demonstrates parsing a reference string with a digest.
func ExampleNewReference_digest() {
	ref, err := properties.NewReference("ghcr.io/oras-project/oras-go@sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76")
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", ref.Registry)
	fmt.Println("Repository:", ref.Repository)
	fmt.Println("Digest:", ref.Digest)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras-go
	// Digest: sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76
}

// ExampleNewReferenceList demonstrates parsing a comma-separated tag list.
func ExampleNewReferenceList() {
	refs, err := properties.NewReferenceList("ghcr.io/oras-project/oras-go:v3.0.0,v3.0.1,latest")
	if err != nil {
		panic(err)
	}

	for _, ref := range refs {
		fmt.Println(ref.String())
	}

	// Output:
	// ghcr.io/oras-project/oras-go:v3.0.0
	// ghcr.io/oras-project/oras-go:v3.0.1
	// ghcr.io/oras-project/oras-go:latest
}

// ExampleNewRegistry demonstrates creating a Registry property from a reference string.
func ExampleNewRegistry() {
	props, err := properties.NewRegistry("registry.example.com/myapp:v1.0")
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", props.Reference.Registry)
	fmt.Println("Repository:", props.Reference.Repository)
	fmt.Println("Tag:", props.Reference.Tag)
	fmt.Println("PlainHTTP:", props.Transport.PlainHTTP)

	// Output:
	// Registry: registry.example.com
	// Repository: myapp
	// Tag: v1.0
	// PlainHTTP: false
}

// ExampleReference_String demonstrates converting a Reference back to a string.
func ExampleReference_String() {
	ref := properties.Reference{
		Registry:   "ghcr.io",
		Repository: "oras-project/oras-go",
		Tag:        "v3.0.0",
	}

	fmt.Println(ref.String())

	// Output:
	// ghcr.io/oras-project/oras-go:v3.0.0
}

// ExampleReference_Host demonstrates the Host method with docker.io aliasing.
func ExampleReference_Host() {
	ref := properties.Reference{Registry: "docker.io", Repository: "library/nginx"}
	fmt.Println(ref.Host())

	ref2 := properties.Reference{Registry: "ghcr.io", Repository: "oras-project/oras-go"}
	fmt.Println(ref2.Host())

	// Output:
	// registry-1.docker.io
	// ghcr.io
}

// ExampleNewRegistryFromReference demonstrates creating a Registry property
// from an already-parsed Reference.
func ExampleNewRegistryFromReference() {
	ref := properties.Reference{
		Registry:   "registry.example.com",
		Repository: "myapp",
		Tag:        "v1.0",
	}

	props := properties.NewRegistryFromReference(ref)
	fmt.Println("Registry:", props.Reference.Registry)
	fmt.Println("Tag:", props.Reference.Tag)

	// Output:
	// Registry: registry.example.com
	// Tag: v1.0
}

// ExampleReference_GetReference demonstrates GetReference returning the digest
// when set, or the tag otherwise.
func ExampleReference_GetReference() {
	byTag := properties.Reference{Registry: "ghcr.io", Repository: "org/app", Tag: "v1.0"}
	fmt.Println(byTag.GetReference())

	byDigest := properties.Reference{
		Registry:   "ghcr.io",
		Repository: "org/app",
		Digest:     "sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76",
	}
	fmt.Println(byDigest.GetReference())

	// Output:
	// v1.0
	// sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76
}

// ExampleReference_ReferenceOrDefault demonstrates ReferenceOrDefault returning
// "latest" when neither tag nor digest is set.
func ExampleReference_ReferenceOrDefault() {
	withTag := properties.Reference{Registry: "ghcr.io", Repository: "org/app", Tag: "v1.0"}
	fmt.Println(withTag.ReferenceOrDefault())

	noRef := properties.Reference{Registry: "ghcr.io", Repository: "org/app"}
	fmt.Println(noRef.ReferenceOrDefault())

	// Output:
	// v1.0
	// latest
}

// ExampleReference_GetDigest demonstrates GetDigest returning a typed digest.Digest.
func ExampleReference_GetDigest() {
	ref := properties.Reference{
		Registry:   "ghcr.io",
		Repository: "org/app",
		Digest:     "sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76",
	}

	d, err := ref.GetDigest()
	if err != nil {
		panic(err)
	}
	fmt.Println("Algorithm:", d.Algorithm())
	fmt.Println("Hex:", d.Hex()[:12]+"...")

	// Output:
	// Algorithm: sha256
	// Hex: 601d05a48832...
}
