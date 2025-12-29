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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/orm"
	"github.com/oras-project/oras-go/v3/orm/models"
)

func main() {
	ctx := context.Background()

	// Create a memory store
	store := memory.New()

	// Create ORM client
	client := orm.NewClient(store)

	// Create config blob
	configData := []byte(`{
		"architecture": "amd64",
		"os": "linux",
		"config": {
			"Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
		}
	}`)
	configBlob := client.NewBlob("application/vnd.oci.image.config.v1+json", configData)

	// Create layer blobs
	layer1Data := []byte("Layer 1 content")
	layer1Blob := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer1Data)

	layer2Data := []byte("Layer 2 content")
	layer2Blob := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer2Data)

	// Push blobs
	if err := configBlob.Push(ctx); err != nil {
		log.Fatalf("Failed to push config: %v", err)
	}
	if err := layer1Blob.Push(ctx); err != nil {
		log.Fatalf("Failed to push layer 1: %v", err)
	}
	if err := layer2Blob.Push(ctx); err != nil {
		log.Fatalf("Failed to push layer 2: %v", err)
	}

	// Build and push image
	image, err := client.BuildImage().
		WithConfig(configBlob).
		AddLayer(layer1Blob).
		AddLayer(layer2Blob).
		WithPlatform(&ocispec.Platform{
			Architecture: "amd64",
			OS:           "linux",
		}).
		WithAnnotation("org.opencontainers.image.version", "1.0.0").
		BuildAndPush(ctx, "example-image:latest")

	if err != nil {
		log.Fatalf("Failed to create image: %v", err)
	}

	fmt.Printf("✓ Created image: %s\n", image.Digest())
	fmt.Printf("  Media Type: %s\n", image.MediaType())
	fmt.Printf("  Size: %d bytes\n", image.Size())

	// Fetch and explore the image
	fetched, err := client.FetchByReference(ctx, "example-image:latest")
	if err != nil {
		log.Fatalf("Failed to fetch image: %v", err)
	}

	img := fetched.(*models.Image)

	// Get config
	config, err := img.Config(ctx)
	if err != nil {
		log.Fatalf("Failed to get config: %v", err)
	}
	fmt.Printf("\n✓ Config: %s (%d bytes)\n", config.Digest(), config.Size())

	// Get layers
	layers, err := img.Layers(ctx)
	if err != nil {
		log.Fatalf("Failed to get layers: %v", err)
	}
	fmt.Printf("\n✓ Layers:\n")
	for i, layer := range layers {
		fmt.Printf("  %d. %s (%d bytes)\n", i+1, layer.Digest(), layer.Size())
	}

	// Get platform
	platform, err := img.Platform(ctx)
	if err != nil {
		log.Fatalf("Failed to get platform: %v", err)
	}
	if platform != nil {
		fmt.Printf("\n✓ Platform: %s/%s\n", platform.OS, platform.Architecture)
	}
}
