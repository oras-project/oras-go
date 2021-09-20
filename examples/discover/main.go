package main

import (
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

func testArtifactsManifest() *artifactspec.Manifest {
	return &artifactspec.Manifest{
		Blobs: []artifactspec.Descriptor{
			{
				ArtifactType: "sbom/example",
				MediaType:    "application/json",
				Digest:       "test-sbom-example",
			},
			{
				ArtifactType: "signature/example",
				MediaType:    "application/json",
				Digest:       "test-signature-example",
			},
		},
	}
}

func main() {

	// Example artifact
	// Example subject

	// ctx := context.Background()

	// ref := fmt.Sprintf("localhost:5000/hello:v1")
	// fileName := "hello.txt"
	// fileContent := []byte("Hello World!\n")

	// memoryStore := content.NewMemory()
	// desc, err := memoryStore.Add(fileName, "text/utf8", fileContent)
	// if err != nil {
	// 	panic("could not write to memory store")
	// }

	// content.GenerateArtifactsManifest("sbom/hello", )

	// memoryStore.StoreManifest("localhost:5000/hello", )

	// oras.Copy(ctx)
}
