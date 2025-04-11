# Tutorial: Get started with oras-go v2

This tutorial introduces the basics of managing OCI artifacts with the [oras-go v2](https://pkg.go.dev/oras.land/oras-go/v2) package.

You'll get the most out of this tutorial if you have a basic familiarity with Go and its tooling. If this is your first exposure to Go, please see [Tutorial: Get started with Go](https://golang.org/doc/tutorial/getting-started) for a quick introduction.

The tutorial includes the following sections:

1. Connect to a remote repository.
2. Show tags in the repository.
3. Push a layer to the repository.
4. Push a manifest to the repository.
5. Fetch the manifest from the repository.
6. Copy the artifact from the repository.

The complete code is provided at the end of this tutorial.

## Connect to a remote repository with basic authentication

The following code snippet demonstrates how to use [NewRepository](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#NewRepository) from the [remote](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote) package to connect to a remote repository. Basic authentication (using a username and password) is handled by the [auth](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote/auth) package. Other authentication methods, such as access token authentication, are also supported.

```
ctx := context.Background()
registry := "myregistry.example.io"
repository := "myrepo"
repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", registry, repository))
if err != nil {
	panic(err)
}
// Note: The below code can be omitted if authentication is not required.
repo.Client = &auth.Client{
	Client: retry.DefaultClient,
	Cache:  auth.NewCache(),
	Credential: auth.StaticCredential(registry, auth.Credential{
		Username: "username",
		Password: "password",
	}),
}
```

## Show tags in the repository
The following code snippet uses the [(*Repository) Tags](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Tags) method from the [remote](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote) package to list the tags in the repository.

```
err = repo.Tags(ctx, "", func(tags []string) error {
	for _, tag := range tags {
		fmt.Println(tag)
	}
	return nil
})
if err != nil {
	panic(err)
}
```

## Push a layer to the repository

All referenced layers must exist in the repository before a manifest can be pushed. The following code snippet demonstrates how to push a manifest layer using the [(*Repository) Push](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Push) method.

```
// push manifest layer
manifestLayer := []byte("example manifest layer")
manifestLayerDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, manifestLayer)
err = repo.Push(ctx, manifestLayerDescriptor, bytes.NewReader(manifestLayer))
if err != nil {
	panic(err)
}
fmt.Println("Pushed manifest layer")
```

## Push a manifest to the repository with the tag "quickstart"

The following code snippet demonstrates how to pack a manifest and push it to the repository with the tag "quickstart" using the [PackManifest](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0#PackManifest) and the [(*Repository) Tag](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Tag) methods.

```
tag := "quickstart"
packOpts := oras.PackManifestOptions{
	Layers: []v1.Descriptor{manifestLayerDescriptor},
}
artifactType := "application/vnd.example+type"
desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
if err != nil {
	panic(err)
}
err = repo.Tag(ctx, desc, tag)
if err != nil {
	panic(err)
}
fmt.Println("Pushed and tagged manifest")
```

## Fetch the manifest from the repository by tag

The following code snippet demonstrates how to fetch a manifest from the repository by its tag. First, the descriptor associated with the tag is resolved using [(*Repository) Resolve](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Resolve). Then, the content is fetched using [(*Repository) Fetch](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Fetch). Finally, the content is read using [ReadAll](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/content#ReadAll) from the [content](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/content) package.

```
desc, err := repo.Resolve(ctx, tag)
if err != nil {
	panic(err)
}
rc, err := repo.Fetch(ctx, desc)
if err != nil {
	panic(err)
}
defer rc.Close() // don't forget to close
fetchedManifestContent, err := content.ReadAll(rc, desc)
if err != nil {
	panic(err)
}
fmt.Println(string(fetchedManifestContent))
```

## Copy the artifact to local OCI layout directory from the repository

The following code snippet demonstrates how to copy an artifact from the repository by its tag and save it to the current directory in the [OCI layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format. The copy operation is performed using the [Copy](https://pkg.go.dev/oras.land/oras-go/v2#Copy) function.

```
ociDir, err := os.MkdirTemp(".", "oras_oci_example_*")
if err != nil {
	panic(err)
}
ociTarget, err := oci.New(ociDir)
if err != nil {
	panic(err)
}
desc, err = oras.Copy(ctx, repo, tag, ociTarget, "quickstartOCI", oras.DefaultCopyOptions)
if err != nil {
	panic(err)
}
fmt.Println(desc.Digest)
```

## Conclusion

Congratulations! You’ve completed this tutorial.

Suggested next steps:
* Check out more [examples](https://pkg.go.dev/oras.land/oras-go/v2#pkg-overview) in the documentation.
* Learn about how `oras-go` v2 [models artifacts](https://github.com/oras-project/oras-go/blob/main/docs/Modeling-Artifacts.md).
* Learn about [Targets and Content Stores](https://github.com/oras-project/oras-go/blob/main/docs/Targets.md) in `oras-go` v2.

## Completed Code

This section contains the completed code from this tutorial.

```
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func main() {
	// 1. Connect to a remote repository with basic authentication
	ctx := context.Background()
	registry := "myregistry.example.io"
	repository := "myrepo"
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", registry, repository))
	if err != nil {
		panic(err)
	}
	// Note: The below code can be omitted if authentication is not required.
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
		Credential: auth.StaticCredential(registry, auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 2. Show the tags in the repository
	err = repo.Tags(ctx, "", func(tags []string) error {
		for _, tag := range tags {
			fmt.Println(tag)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	// 3. Push a layer to the repository
	manifestLayer := []byte("example manifest layer")
	manifestLayerDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, manifestLayer)
	err = repo.Push(ctx, manifestLayerDescriptor, bytes.NewReader(manifestLayer))
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed manifest layer")

	// 4. Push a manifest to the repository with the tag "quickstart"
	tag := "quickstart"
	packOpts := oras.PackManifestOptions{
		Layers: []v1.Descriptor{manifestLayerDescriptor},
	}
	artifactType := "application/vnd.example+type"
	desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
	if err != nil {
		panic(err)
	}
	err = repo.Tag(ctx, desc, tag)
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed and tagged manifest")

	// 5. Fetch the manifest from the repository by tag
	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}
	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		panic(err)
	}
	defer rc.Close() // don't forget to close
	fetchedManifestContent, err := content.ReadAll(rc, desc)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(fetchedManifestContent))

	// 6. Pull the manifest to local OCI layout directory with a tag "quickstartOCI"
	ociDir, err := os.MkdirTemp(".", "oras_oci_example_*")
	if err != nil {
		panic(err)
	}
	ociTarget, err := oci.New(ociDir)
	if err != nil {
		panic(err)
	}
	desc, err = oras.Copy(ctx, repo, tag, ociTarget, "quickstartOCI", oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}
	fmt.Println(desc.Digest)
}

```
