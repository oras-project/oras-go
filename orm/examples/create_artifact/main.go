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

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/orm"
)

func main() {
	ctx := context.Background()

	// Create a memory store (can be replaced with OCI store or remote registry)
	store := memory.New()

	// Create ORM client
	client := orm.NewClient(store)

	// Create blobs
	configData := []byte(`{"version": "1.0.0", "description": "Example artifact"}`)
	configBlob := client.NewBlob("application/json", configData)

	dataData := []byte("This is the artifact payload")
	dataBlob := client.NewBlob("application/octet-stream", dataData)

	// Push blobs to storage
	if err := configBlob.Push(ctx); err != nil {
		log.Fatalf("Failed to push config blob: %v", err)
	}
	if err := dataBlob.Push(ctx); err != nil {
		log.Fatalf("Failed to push data blob: %v", err)
	}

	// Build and push artifact
	artifact, err := client.BuildArtifact("application/vnd.example+type").
		AddBlob(configBlob).
		AddBlob(dataBlob).
		WithAnnotation("org.opencontainers.image.version", "1.0.0").
		WithAnnotation("org.opencontainers.image.description", "Example artifact").
		BuildAndPush(ctx, "example-artifact:v1.0.0")

	if err != nil {
		log.Fatalf("Failed to create artifact: %v", err)
	}

	fmt.Printf("✓ Created artifact: %s\n", artifact.Digest())
	fmt.Printf("  Media Type: %s\n", artifact.MediaType())
	fmt.Printf("  Size: %d bytes\n", artifact.Size())

	// Fetch the artifact back
	fetched, err := client.FetchByReference(ctx, "example-artifact:v1.0.0")
	if err != nil {
		log.Fatalf("Failed to fetch artifact: %v", err)
	}

	fmt.Printf("\n✓ Fetched artifact: %s\n", fetched.Digest())
	fmt.Printf("  Annotations: %v\n", fetched.Annotations())
}
