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

package signature_test

import (
	_ "crypto/sha256" // required for digest parsing
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// ExampleNewSimpleSigningPayload demonstrates creating a simple signing payload
// for a given image digest and docker reference.
func ExampleNewSimpleSigningPayload() {
	dgst := digest.FromString("example image content")
	payload := signature.NewSimpleSigningPayload(dgst, "registry.example.com/myapp:v1.0")

	fmt.Println("Reference:", payload.DockerReference())

	d, err := payload.ImageDigest()
	if err != nil {
		panic(err)
	}
	fmt.Println("Algorithm:", d.Algorithm())

	// Output:
	// Reference: registry.example.com/myapp:v1.0
	// Algorithm: sha256
}

// ExampleParseSimpleSigningPayload demonstrates a marshal/parse round-trip
// of a simple signing payload.
func ExampleParseSimpleSigningPayload() {
	dgst := digest.FromString("example image content")
	original := signature.NewSimpleSigningPayload(dgst, "registry.example.com/myapp:v1.0")

	data, err := original.Marshal()
	if err != nil {
		panic(err)
	}

	parsed, err := signature.ParseSimpleSigningPayload(data)
	if err != nil {
		panic(err)
	}

	fmt.Println("Reference:", parsed.DockerReference())
	fmt.Println("Valid:", parsed.Validate() == nil)

	// Output:
	// Reference: registry.example.com/myapp:v1.0
	// Valid: true
}

// ExampleNewLookasideStore demonstrates creating a lookaside store with
// separate read and write URLs, as used in air-gapped or enterprise environments.
func ExampleNewLookasideStore() {
	store := signature.NewLookasideStore(
		"https://signatures.example.com/read",
		"https://signatures.example.com/write",
	)
	fmt.Println(store != nil)

	// Output:
	// true
}

// ExampleNewSignedByVerifier demonstrates creating a signature verifier
// backed by a custom SignatureStore implementation.
func ExampleNewSignedByVerifier() {
	store := signature.NewLookasideStore(
		"file:///var/lib/signatures",
		"file:///var/lib/signatures",
	)
	verifier := signature.NewSignedByVerifier(store)
	fmt.Println(verifier != nil)

	// Output:
	// true
}
