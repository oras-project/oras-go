package main

import (
	"context"
	"fmt"
	"os"

	orasContent "oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/images"
	"oras.land/oras-go/pkg/oras"
	orasDocker "oras.land/oras-go/pkg/remotes/docker"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func getLocalRegistryHostname() string {
	hostname := "localhost"
	if v := os.Getenv("LOCAL_REGISTRY_HOSTNAME"); v != "" {
		hostname = v
	}
	return hostname
}

func main() {
	ref := fmt.Sprintf("%s:5000/oras:test", getLocalRegistryHostname())
	fileName := "hello.txt"
	fileContent := []byte("Hello World!\n")
	customMediaType := "my.custom.media.type"

	ctx := context.Background()

	// Push file(s) w custom mediatype to registry
	memoryStore := orasContent.NewMemory()
	desc, err := memoryStore.Add(fileName, customMediaType, fileContent)
	check(err)
	_, err = memoryStore.GenerateManifest(ref, nil, desc)
	check(err)
	registry, err := orasContent.NewRegistry(orasContent.RegistryOptions{PlainHTTP: true})
	check(err)
	fmt.Printf("Pushing %s to %s...\n", fileName, ref)

	r, err := orasDocker.WithDiscover(ref, registry.Resolver, registry.Opts)
	check(err)

	f, err := r.Fetcher(ctx, ref)
	check(err)

	providerWrapper := &oras.ProviderWrapper{
		Fetcher: f,
	}

	desc, err = oras.Copy(ctx, memoryStore, ref, registry, "", oras.WithPullBaseHandler(
		images.AppendArtifactsHandler(providerWrapper)))
	check(err)
	fmt.Printf("Pushed to %s with digest %s\n", ref, desc.Digest)
}
