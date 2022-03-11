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

type Uploadable struct {
	blob       []byte
	descriptor ocispec.Descriptor
}

func GetUploadable(mediaType string, content []byte) Uploadable {
	return Uploadable{
		blob: content,
		descriptor: ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(content),
			Size:      int64(len(content)),
		},
	}
}

func GetManifest(config ocispec.Descriptor, layers ...ocispec.Descriptor) Uploadable {
	manifest := ocispec.Manifest{
		Config: config,
		Layers: layers,
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	CheckErrorHere(err)
	return GetUploadable(ocispec.MediaTypeImageManifest, manifestJSON)
}

func main() {
	ctx := context.Background()
	env := os.Getenv("LOCAL_REGISTRY_HOSTNAME")
	if env == "" {
		env = "localhost"
	}

	// 1. Push blobs and a manifest
	localRegistry, err := remote.NewRegistry(fmt.Sprintf("%s:5000", env))
	CheckErrorHere(err)
	var localRepoName = "push-example"
	localRepo, err := localRegistry.Repository(ctx, localRepoName)
	CheckErrorHere(err)
	localRepo.(*remote.Repository).PlainHTTP = true
	layer1 := GetUploadable(ocispec.MediaTypeImageLayer, []byte("Hello layer"))
	err = localRepo.Push(ctx, layer1.descriptor, bytes.NewReader(layer1.blob))
	CheckErrorHere(err)
	config := GetUploadable(ocispec.MediaTypeImageLayer, []byte("Hello config"))
	err = localRepo.Push(ctx, config.descriptor, bytes.NewReader(config.blob))
	CheckErrorHere(err)
	manifest1 := GetManifest(config.descriptor, layer1.descriptor)
	err = localRepo.Push(ctx, manifest1.descriptor, bytes.NewReader(manifest1.blob))
	CheckErrorHere(err)

	// 2. Pull a layer blob
	reader, err := localRepo.Fetch(ctx, layer1.descriptor)
	CheckErrorHere(err)
	blob, err := io.ReadAll(reader)
	CheckErrorHere(err)
	fmt.Println("--- Pull a layer blob---")
	fmt.Printf("Pushed layer => \"%v\"; Pulled layer => \"%v\"\n", string(layer1.blob), string(blob))
	fmt.Println()

	// 3. Pull a manifest blob
	reader, err = localRepo.Fetch(ctx, manifest1.descriptor)
	CheckErrorHere(err)
	blob, err = io.ReadAll(reader)
	CheckErrorHere(err)
	fmt.Println("--- Pull manifest as a blob---")
	fmt.Printf("Pushed manifest =>\n %v\nPulled manifest =>\n %v\n", string(manifest1.blob), string(blob))
	fmt.Println()

	// 4. Pull a manifest blob with validation
	manifestBlob, err := content.FetchAll(ctx, localRepo, manifest1.descriptor)
	CheckErrorHere(err)
	fmt.Println("--- Pull manifest with verification---")
	fmt.Printf("Pushed manifest =>\n %v\nPulled manifest =>\n %v\n", string(manifest1.blob), string(manifestBlob))
	fmt.Println()

	// 5. Tag a manifest + Resolve
	tagName := "example-tag"
	localRepo.Tag(ctx, manifest1.descriptor, tagName)
	desc, err := localRepo.Resolve(ctx, tagName)
	CheckErrorHere(err)
	fmt.Printf("%s is tagged with tag name %s\n", desc.Digest, tagName)
	fmt.Println()

	// 6. Push and tag a manifest + Resolve
	tagName = "example-push-tag"
	layer2 := GetUploadable(ocispec.MediaTypeImageLayer, []byte("Hello another layer"))
	err = localRepo.Push(ctx, layer2.descriptor, bytes.NewReader(layer2.blob))
	CheckErrorHere(err)
	manifest2 := GetManifest(config.descriptor, layer1.descriptor, layer2.descriptor)
	err = localRepo.PushTag(ctx, manifest2.descriptor, bytes.NewReader(manifest2.blob), tagName) // upload a manifest with reference
	CheckErrorHere(err)
	desc, err = localRepo.Resolve(ctx, tagName)
	CheckErrorHere(err)
	fmt.Printf("%s is pushed and tagged with tag name %s\n", desc.Digest, tagName)
	fmt.Println()

	// 7. Copy a manifest from MCR to local registry
	tagName = "latest"
	srcRepoName := "mcr/hello-world"
	dstRepoName := "copy-example"
	mcr, err := remote.NewRegistry("mcr.microsoft.com")
	CheckErrorHere(err)
	mcrRepo, err := mcr.Repository(ctx, srcRepoName)
	CheckErrorHere(err)
	localRepo, err = localRegistry.Repository(ctx, dstRepoName)
	CheckErrorHere(err)
	localRepo.(*remote.Repository).PlainHTTP = true
	_, err = oras.Copy(ctx, mcrRepo, tagName, localRepo, tagName)
	CheckErrorHere(err)
	fmt.Println("--- Copy manifest verification---")
	copiedManifest, err := localRepo.Resolve(ctx, tagName)
	CheckErrorHere(err)
	fmt.Printf("%s is copied with tag name %s\n", copiedManifest.Digest, tagName)
	copiedBlob, err := content.FetchAll(ctx, localRepo, copiedManifest)
	CheckErrorHere(err)
	fmt.Printf("Content of copied manifest =>\n %v\n", string(copiedBlob))
	fmt.Println()

	// 8. List all the repositories in the local registry
	fmt.Printf("--- What's in our local registry so far ---\n")
	fmt.Printf("%s:5000\n", env)
	localRegistry.PlainHTTP = true
	repos, err := registry.Repositories(ctx, localRegistry)
	CheckErrorHere(err)
	for _, repoName := range repos {
		fmt.Printf("  +--%s\n", repoName)
		repo, err := localRegistry.Repository(ctx, repoName)
		CheckErrorHere(err)
		err = repo.Tags(ctx, func(tags []string) error {
			for _, tag := range tags {
				manifestDesc, err := repo.Resolve(ctx, tag)
				CheckErrorHere(err)
				fmt.Printf("      +-%s (%s)\n", tag, manifestDesc.Digest)
			}
			return nil
		})
		CheckErrorHere(err)
	}
}

func CheckErrorHere(e error) {
	if e != nil {
		panic(e)
	}
}
