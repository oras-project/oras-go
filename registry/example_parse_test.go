package registry_test

import (
	_ "crypto/sha256"
	"fmt"
	"log"

	"oras.land/oras-go/v2/registry"
)

var img = "ghcr.io/oras-project/oras@sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76"

func ExampleParseReference() {

	ref, err := registry.ParseReference(img)
	if err != nil {
		log.Fatalf("%s", err)
	}

	fmt.Printf("Registry: %s\n", ref.Registry)
	fmt.Printf("Repository: %s\n", ref.Repository)
	digest, err := ref.Digest()
	if err != nil {
		log.Fatalf("%s", err)
	}
	fmt.Printf("Digest: %v\n", digest)

	// Output:
	// Registry: ghcr.io
	// Repository: oras-project/oras
	// Digest: sha256:601d05a48832e7946dab8f49b14953549bebf42e42f4d7973b1a5a287d77ab76
}
