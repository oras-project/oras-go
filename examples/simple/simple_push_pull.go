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
	"os"

	"oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/oras"
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
	memoryStore := content.NewMemory()
	desc, err := memoryStore.Add(fileName, customMediaType, fileContent)
	check(err)

	manifest, manifestDesc, config, configDesc, err := content.GenerateManifestAndConfig(nil, nil, desc)
	check(err)
	memoryStore.Set(configDesc, config)
	err = memoryStore.StoreManifest(ref, manifestDesc, manifest)
	check(err)
	registry, err := content.NewRegistry(content.RegistryOptions{PlainHTTP: true})
	fmt.Printf("Pushing %s to %s...\n", fileName, ref)
	desc, err = oras.Copy(ctx, memoryStore, ref, registry, "")
	check(err)
	fmt.Printf("Pushed to %s with digest %s\n", ref, desc.Digest)

	// Pull file(s) from registry and save to disk
	fmt.Printf("Pulling from %s and saving to %s...\n", ref, fileName)
	fileStore := content.NewFile("")
	defer fileStore.Close()
	allowedMediaTypes := []string{customMediaType}
	desc, err = oras.Copy(ctx, registry, ref, fileStore, "", oras.WithAllowedMediaTypes(allowedMediaTypes))
	check(err)
	fmt.Printf("Pulled from %s with digest %s\n", ref, desc.Digest)
	fmt.Printf("Try running 'cat %s'\n", fileName)
}
