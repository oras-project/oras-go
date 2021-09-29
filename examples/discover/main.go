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
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/containerd/containerd/remotes"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/artifact"
	"oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/remotes/docker"
)

const (
	repoRef  = "localhost:5000/net-monitor"
	imageRef = "localhost:5000/net-monitor:v1"
)

type exampleState struct {
	localhostResolver  remotes.Resolver
	example_references []artifactspec.Descriptor
	targetResolver     *content.Memory
}

func fail(err error, message string) {
	if err != nil {
		panic(fmt.Sprintf("%s: %s", message, err.Error()))
	}
}

func main() {
	ctx := context.Background()
	e := &exampleState{}

	// If a test registry isn't running then start an mock server
	CreateExampleTestServerIfLocalServerIsNotRunning(e)

	registry, err := content.NewRegistry(content.RegistryOptions{PlainHTTP: true})
	fail(err, "could not create a new registry")

	discoverer, err := docker.WithDiscover(imageRef, e.localhostResolver, &registry.Opts)
	fail(err, "could not create discoverer")

	desc, blobs, err := oras.Discover(ctx, discoverer, imageRef, "")
	fail(err, "could not discover artifacts")

	output := json.NewEncoder(os.Stdout)
	err = output.Encode(desc)
	fail(err, "could not encode artifact manifest to std out")

	for _, b := range blobs {
		err = output.Encode(b)
		fail(err, "could not encode artifact blob")
	}

	desc, err = oras.Copy(ctx, discoverer, "localhost:5000/net-monitor:v1", e.targetResolver, "",
		oras.WithAllowedMediaType(
			artifactspec.MediaTypeArtifactManifest,
			imagespec.MediaTypeImageManifest,
			"application/vnd.docker.image.rootfs.diff.tar.gzip",
			"application/vnd.docker.container.image.v1+json",
			"application/json"),
		oras.WithArtifactFilters(artifact.AnnotationFilter(func(annotations map[string]string) bool {
			val, ok := annotations["test-filter"]
			if !ok {
				return true
			}

			if val == "tested" {
				return false
			}

			return true
		})))
	fail(err, "could not copy image")

	desc, _, ok := e.targetResolver.GetByName("signature.json")
	if !ok {
		fail(errors.New("expected the signature.json blob to copied over"), "")
	}

	fmt.Printf("copied: %+v\n", desc)

	_, _, ok = e.targetResolver.GetByName("sbom.json")
	if ok {
		fail(errors.New("did not expect sbom.json to be copied over"), "")
	}

	fmt.Printf("skipped sbom.json with annotation filter\n")
}
