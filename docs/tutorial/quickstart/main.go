// Copyright The ORAS Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The tutorial build tag keeps this program out of the library build and
// coverage. Remove this line if you copy the file into your own module.
//go:build tutorial

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/oci"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

func main() {
	// 1. Connect to a remote repository with token authentication
	ref := "example.registry.com/myrepo"
	repo, err := remote.NewRepository(ref)
	if err != nil {
		panic(err)
	}
	// Note: The below code can be omitted if authentication is not required.
	repo.Registry.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
		CredentialFunc: credentials.StaticCredentialFunc(repo.Registry.Reference.Registry, credentials.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 2. Show the tags in the repository
	ctx := context.Background()
	err = repo.Tags(ctx, "", func(tags []string) error {
		for _, tag := range tags {
			fmt.Println(tag)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	// 3. push a layer to the repository
	layer := []byte("example manifest layer")
	layerDescriptor, err := oras.PushBytes(ctx, repo, ocispec.MediaTypeImageLayer, layer)
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed manifest layer:", layerDescriptor.Digest)

	// 4. Push a manifest to the repository with the tag "quickstart"
	packOpts := oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layerDescriptor},
	}
	artifactType := "application/vnd.example+type"
	desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
	if err != nil {
		panic(err)
	}
	tag := "quickstart"
	err = repo.Tag(ctx, desc, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed and tagged manifest")

	// 5. Fetch the manifest from the repository by tag
	_, fetchedManifestContent, err := oras.FetchBytes(ctx, repo, tag, oras.DefaultFetchBytesOptions)
	if err != nil {
		panic(err)
	}
	fmt.Println("Fetched manifest:", len(fetchedManifestContent), "bytes")

	// 6. Parse the fetched manifest content and get the layers
	var manifest ocispec.Manifest
	if err := json.Unmarshal(fetchedManifestContent, &manifest); err != nil {
		panic(err)
	}
	for _, layer := range manifest.Layers {
		layerContent, err := content.FetchAll(ctx, repo, layer)
		if err != nil {
			panic(err)
		}
		fmt.Println("Fetched layer:", layer.Digest, len(layerContent), "bytes")
	}

	// 7. Copy the artifact to local OCI layout directory with a tag "quickstartOCI"
	ociDir, err := os.MkdirTemp(".", "oras_oci_example_*")
	if err != nil {
		panic(err)
	}
	ociTarget, err := oci.New(ociDir)
	if err != nil {
		panic(err)
	}
	_, err = oras.Copy(ctx, repo, tag, ociTarget, "quickstartOCI", oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}
	fmt.Println("Copied the artifact")
}
