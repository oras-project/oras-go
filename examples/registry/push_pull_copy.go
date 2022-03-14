// This example shows how to use oras go library to do some
// basic registry operations
//
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	_ "crypto/sha256"
	"encoding/json"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

func main() {
	ctx := context.Background()
	env := os.Getenv("LOCAL_REGISTRY_HOSTNAME")
	if env == "" {
		env = "localhost"
	}
	// 0. Create a registry struct pointing to our local OSS registry instance
	localRegistry, err := remote.NewRegistry(fmt.Sprintf("%s:5000", env))
	localRegistry.RepositoryOptions.PlainHTTP = true
	CheckError(err)

	// 1. Create a image in the 'push-example' repository
	var localRepoName = "push-example"
	localRepo, err := localRegistry.Repository(ctx, localRepoName)
	CheckError(err)
	// 1.1 Create a config blob and a layer blob
	// For each blob , we need to create its content and descriptor respectively
	layer1Blob := []byte("Hello layer")
	layer1Descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layer1Blob),
		Size:      int64(len(layer1Blob)),
	}
	configBlob := []byte("Hello config")
	configDescriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(configBlob),
		Size:      int64(len(configBlob)),
	}
	// 1.2 Create a manifest pointing to the config and layer blobs
	manifest1Content := ocispec.Manifest{
		Config:    configDescriptor,
		Layers:    []ocispec.Descriptor{layer1Descriptor},
		Versioned: specs.Versioned{SchemaVersion: 2},
	}
	manifest1Blob, err := json.Marshal(manifest1Content)
	CheckError(err)
	manifest1Descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest1Blob),
		Size:      int64(len(manifest1Blob)),
	}
	// 1.3 Push all the blobs
	err = localRepo.Push(ctx, manifest1Descriptor, bytes.NewReader(manifest1Blob))
	CheckError(err)
	err = localRepo.Push(ctx, layer1Descriptor, bytes.NewReader(layer1Blob))
	CheckError(err)
	err = localRepo.Push(ctx, configDescriptor, bytes.NewReader(configBlob))
	CheckError(err)

	// 2. Pull a layer blob
	reader, err := localRepo.Fetch(ctx, layer1Descriptor) // we can use the descriptor to pull the blob
	CheckError(err)
	pulledBlob, err := io.ReadAll(reader)
	CheckError(err)
	fmt.Println("--- Pull a layer blob---")
	fmt.Printf("Pushed layer => \"%v\"; Pulled layer => \"%v\"\n", string(layer1Blob), string(pulledBlob))
	fmt.Println()

	// 3. Pull a manifest blob
	reader, err = localRepo.Fetch(ctx, manifest1Descriptor) // we can use the descriptor to pull a manifest, which is also a blob
	CheckError(err)
	pulledBlob, err = io.ReadAll(reader)
	CheckError(err)
	fmt.Println("--- Pull manifest as a blob---")
	fmt.Printf("Pushed manifest =>\n %v\nPulled manifest =>\n %v\n", string(manifest1Blob), string(pulledBlob))
	fmt.Println()

	// 4. Pull a manifest blob with validation
	manifestBlob, err := content.FetchAll(ctx, localRepo, manifest1Descriptor) // Another way to pull
	CheckError(err)
	fmt.Println("--- Pull manifest with verification---")
	fmt.Printf("Pushed manifest =>\n %v\nPulled manifest =>\n %v\n", string(manifest1Blob), string(manifestBlob))
	fmt.Println()

	// 5. Tag a manifest + Resolve
	tagName := "example-tag"
	localRepo.Tag(ctx, manifest1Descriptor, tagName)           // create a human-readable tag to the manifest
	resolvedDescriptor, err := localRepo.Resolve(ctx, tagName) // get the descriptor associated with the tag
	CheckError(err)
	fmt.Printf("%s is tagged with tag name %s\n", resolvedDescriptor.Digest, tagName)
	fmt.Println()

	// 6. Push and tag a manifest + Resolve
	// 6.1 Prepare another layer blob and re-generate the manifest
	layer2Blob := []byte("Hello another layer")
	layer2Descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(layer2Blob),
		Size:      int64(len(layer2Blob)),
	}
	manifest2Content := ocispec.Manifest{
		Config:    configDescriptor,
		Layers:    []ocispec.Descriptor{layer2Descriptor},
		Versioned: specs.Versioned{SchemaVersion: 2},
	}
	manifest2Blob, err := json.Marshal(manifest2Content)
	CheckError(err)
	manifest2Descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest2Blob),
		Size:      int64(len(manifest2Blob)),
	}
	// 6.2 Push the created blob and manifest
	err = localRepo.Push(ctx, layer2Descriptor, bytes.NewReader(layer2Blob))
	CheckError(err)
	tagName = "example-push-tag"
	err = localRepo.PushTag(ctx, manifest2Descriptor, bytes.NewReader(manifest2Blob), tagName) // Both push and tag are done in this line
	CheckError(err)

	// 6.3 Validate the changes
	resolvedDescriptor, err = localRepo.Resolve(ctx, tagName)
	CheckError(err)
	fmt.Printf("%s is pushed and tagged with tag name %s\n", resolvedDescriptor.Digest, tagName)
	fmt.Println()

	// 7. Copy a manifest from MCR to local registry
	// 7.1 Create registry and repository pointing to the source
	srcRepoName := "mcr/hello-world"
	mcr, err := remote.NewRegistry("mcr.microsoft.com")
	CheckError(err)
	mcrRepo, err := mcr.Repository(ctx, srcRepoName)
	CheckError(err)
	// 7.2 Create repository pointing to the destination
	dstRepoName := "copy-example"
	localRepo, err = localRegistry.Repository(ctx, dstRepoName)
	CheckError(err)
	// 7.3 Copy from source to destination
	tagName = "latest"
	_, err = oras.Copy(ctx, mcrRepo, tagName, localRepo, tagName)
	CheckError(err)
	// 7.4 Verify the copied manifest
	fmt.Println("--- Copy manifest verification---")
	copiedManifest, err := localRepo.Resolve(ctx, tagName)
	CheckError(err)
	fmt.Printf("%s is copied with tag name %s\n", copiedManifest.Digest, tagName)
	copiedBlob, err := content.FetchAll(ctx, localRepo, copiedManifest)
	CheckError(err)
	fmt.Printf("Content of copied manifest =>\n %v\n", string(copiedBlob))
	fmt.Println()

	// 8. List all the repositories in the local registry
	fmt.Printf("--- What's in our local registry so far ---\n")
	fmt.Printf("%s:5000 (Registry)\n", env)
	localRegistry.PlainHTTP = true
	repos, err := registry.Repositories(ctx, localRegistry)
	CheckError(err)
	for _, repoName := range repos {
		fmt.Printf("  +--%s (Repository)\n", repoName)
		repo, err := localRegistry.Repository(ctx, repoName)
		CheckError(err)
		err = repo.Tags(ctx, func(tags []string) error {
			for _, tag := range tags {
				manifestDesc, err := repo.Resolve(ctx, tag)
				CheckError(err)
				fmt.Printf("      +-%s (%s)\n", tag, manifestDesc.Digest)
			}
			return nil
		})
		CheckError(err)
	}
}

func CheckError(e error) {
	if e != nil {
		panic(e)
	}
}
