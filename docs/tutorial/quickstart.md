# Tutorial: Get started with oras-go v3

This tutorial introduces the basics of managing OCI artifacts with the [oras-go v3](https://pkg.go.dev/github.com/oras-project/oras-go/v3) package.

You'll get the most out of this tutorial if you have a basic familiarity with Go and its tooling. If this is your first exposure to Go, please see [Tutorial: Get started with Go](https://golang.org/doc/tutorial/getting-started) for a quick introduction.

The tutorial includes the following sections:

1. Create a folder for your code.
2. Connect to a remote repository.
3. Show tags in the repository.
4. Push a layer to the repository.
5. Push a manifest to the repository.
6. Fetch the manifest from the repository.
7. Parse the fetched manifest content and get the layers.
8. Copy the artifact from the repository.

The complete code is provided at the end of this tutorial.

## Create a folder for your code

Open a command prompt and cd to your working directory.

Create a directory for your code.
```shell
mkdir oras-go-v3-quickstart
cd oras-go-v3-quickstart
```
Create a module in which you can manage dependencies.

```console
$ go mod init quickstart/oras-go-v3
go: creating new go.mod: quickstart/oras-go-v3
```

Import the `oras-go` v3 package.
```shell
go get github.com/oras-project/oras-go/v3
```

In your text editor, create a file `main.go` in which to write your code.

## Connect to a remote repository with token authentication

Paste the following into `main.go` and save the file. This code demonstrates how to use [NewRepository](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#NewRepository) from the [registry/remote](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote) package to connect to a remote repository. Token authentication (using a username and password, see reference [here](https://distribution.github.io/distribution/spec/auth/token/)) is handled by the [registry/remote/auth](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/auth) package. Other authentication methods, such as refresh token authentication, are also supported.

```go
package main

import (
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
}
```

## Show tags in the repository
Add these two lines to the `import` block of `main.go`.

```go
"context"
"fmt"
```

The following code snippet uses the [(*Repository) Tags](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#Repository.Tags) method to list the tags in the repository. Paste the code into `main.go` after the last section.

```go
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
```

### Run the code

Run `go mod tidy` to clean up dependencies.

```shell
go mod tidy
```

Run the code.
```shell
go run .
```

You should see the tags in the repository.

## Push a layer to the repository

All referenced layers must exist in the repository before a manifest can be pushed, so we need to push manifest layers before we can push a manifest. 

Add these two lines to the `import` block of `main.go`.

```go
ocispec "github.com/opencontainers/image-spec/specs-go/v1"
"github.com/oras-project/oras-go/v3"
```

The following code snippet demonstrates how to push a manifest layer with [PushBytes](https://pkg.go.dev/github.com/oras-project/oras-go/v3#PushBytes). Paste the code into `main.go` after the last section.

```go
// 3. push a layer to the repository
layer := []byte("example manifest layer")
layerDescriptor, err := oras.PushBytes(ctx, repo, ocispec.MediaTypeImageLayer, layer)
if err != nil {
	panic(err)
}
fmt.Println("Pushed manifest layer:", layerDescriptor.Digest)
```

## Push a manifest to the repository with the tag "quickstart"

The following code snippet demonstrates how to pack a manifest and push it to the repository with the tag "quickstart" using the [PackManifest](https://pkg.go.dev/github.com/oras-project/oras-go/v3#PackManifest) and the [(*Repository) Tag](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#Repository.Tag) methods. Paste the code into `main.go` after the last section.

```go
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
```

## Fetch the manifest from the repository by tag

The following code snippet demonstrates how to fetch a manifest from the repository by its tag with [FetchBytes](https://pkg.go.dev/github.com/oras-project/oras-go/v3#FetchBytes). Paste the code into `main.go` after the last section.

```go
// 5. Fetch the manifest from the repository by tag
_, fetchedManifestContent, err := oras.FetchBytes(ctx, repo, tag, oras.DefaultFetchBytesOptions)
if err != nil {
	panic(err)
}
fmt.Println("Fetched manifest:", len(fetchedManifestContent), "bytes")
```

## Parse the fetched manifest content and get the layers

Add these two lines to the `import` block of `main.go`.
```go
"encoding/json"
"github.com/oras-project/oras-go/v3/content"
```

The following code snippet demonstrates how to parse the fetched manifest content and get the layers. [FetchAll](https://pkg.go.dev/github.com/oras-project/oras-go/v3/content#FetchAll) from the [content](https://pkg.go.dev/github.com/oras-project/oras-go/v3/content) package is used to read and fetch the content identified by a descriptor. Paste the code into `main.go` after the last section.

```go
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
```

## Copy the artifact to local OCI layout directory from the repository

Add these two lines to the `import` block of `main.go`.
```go
"os"
"github.com/oras-project/oras-go/v3/content/oci"
```

The following code snippet demonstrates how to copy an artifact from the repository by its tag and save it to the current directory in the [OCI layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format. The copy operation is performed using [Copy](https://pkg.go.dev/github.com/oras-project/oras-go/v3#Copy). Paste the code into `main.go` after the last section.

```go
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
```

## Run the code

Run the code.
```shell
go run .
```

You should see a similar output on the terminal as below and an OCI layout folder in the current directory.
```
tag1
tag2
tag3
Pushed manifest layer: sha256:4f19474743ecb04b60156ea41b73e06fdf6a5b758e007b788aaa92595dcd3a49
Pushed and tagged manifest
Fetched manifest: 520 bytes
Fetched layer: sha256:4f19474743ecb04b60156ea41b73e06fdf6a5b758e007b788aaa92595dcd3a49 22 bytes
Copied the artifact
```

## Conclusion

Congratulations! You’ve completed this tutorial.

Suggested next steps:
* Check out more [examples](https://pkg.go.dev/github.com/oras-project/oras-go/v3#pkg-overview) in the documentation.
* Learn about how `oras-go` v3 [models artifacts](https://github.com/oras-project/oras-go/blob/main/docs/Modeling-Artifacts.md).
* Learn about [Targets and Content Stores](https://github.com/oras-project/oras-go/blob/main/docs/Targets.md) in `oras-go` v3.

## Completed Code

The [completed program](./quickstart/main.go) is compiled by CI so the tutorial
stays aligned with the current `oras-go` API.
