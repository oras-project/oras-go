# OCI Registry As Storage

:construction: **This project is currently under active development. The API may and will change incompatibly from one commit to another.** :construction:

[![GitHub Actions status](https://github.com/oras-project/oras-go/workflows/build/badge.svg)](https://github.com/oras-project/oras-go/actions?query=workflow%3Abuild)
[![Go Report Card](https://goreportcard.com/badge/github.com/oras-project/oras-go)](https://goreportcard.com/report/github.com/oras-project/oras-go)
[![GoDoc](https://godoc.org/github.com/oras-project/oras-go?status.svg)](https://godoc.org/github.com/oras-project/oras-go)

![ORAS](https://github.com/oras-project/oras-www/raw/main/docs/assets/images/oras.png)

## ORAS Go Library

Using the ORAS Go library, you can develop your own push/pull experience: `myclient push artifacts.azurecr.io/myartifact:1.0 ./mything.thang`

The package `github.com/oras-project/oras-go/pkg/oras` can quickly be imported in other Go-based tools that
wish to benefit from the ability to store arbitrary content in container registries.

### ORAS Go Library Example

[Source](examples/simple_push_pull.go)

```go
package main

import (
	"context"
	"fmt"

	"github.com/oras-project/oras-go/pkg/content"
	"github.com/oras-project/oras-go/pkg/oras"

	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func main() {
	ref := "localhost:5000/oras:test"
	fileName := "hello.txt"
	fileContent := []byte("Hello World!\n")
	customMediaType := "my.custom.media.type"

	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})

	// Push file(s) w custom mediatype to registry
	memoryStore := content.NewMemoryStore()
	desc := memoryStore.Add(fileName, customMediaType, fileContent)
	pushContents := []ocispec.Descriptor{desc}
	fmt.Printf("Pushing %s to %s...\n", fileName, ref)
	desc, err := oras.Push(ctx, resolver, ref, memoryStore, pushContents)
	check(err)
	fmt.Printf("Pushed to %s with digest %s\n", ref, desc.Digest)

	// Pull file(s) from registry and save to disk
	fmt.Printf("Pulling from %s and saving to %s...\n", ref, fileName)
	fileStore := content.NewFileStore("")
	defer fileStore.Close()
	allowedMediaTypes := []string{customMediaType}
	desc, _, err = oras.Pull(ctx, resolver, ref, fileStore, oras.WithAllowedMediaTypes(allowedMediaTypes))
	check(err)
	fmt.Printf("Pulled from %s with digest %s\n", ref, desc.Digest)
	fmt.Printf("Try running 'cat %s'\n", fileName)
}
```

## Code of Conduct

This project has adopted the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md). See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for further details.


