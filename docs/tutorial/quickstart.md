# Tutorial: Get started with oras-go v2

This tutorial introduces the basics of managing OCI artifacts with the [oras-go v2](https://pkg.go.dev/oras.land/oras-go/v2) package.

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

Open a command prompt and cd to your home directory.

On Linux or Mac:
```shell
cd
```
On Windows:
```shell
cd %HOMEPATH%
```

Create a directory for your code.
```shell
mkdir oras-go-v2-quickstart
cd oras-go-v2-quickstart
```
Create a module in which you can manage dependencies.

```shell
go mod init quickstart/oras-go-v2
go: creating new go.mod: quickstart/oras-go-v2
```

In your text editor, create a file `main.go` in which to write your code.

## Connect to a remote repository with basic authentication

Paste the following into `main.go` and save the file. This code demonstrates how to use [NewRepository](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#NewRepository) from the [remote](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote) package to connect to a remote repository. Basic authentication (using a username and password) is handled by the [auth](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote/auth) package. Other authentication methods, such as identity token authentication, are also supported.

```
package main

import (
	"fmt"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func main() {
	// 1. Connect to a remote repository with basic authentication
	registry := "example.registry.com"
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
}
```

## Show tags in the repository
The following code snippet uses the [(*Repository) Tags](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Tags) method from the [remote](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote) package to list the tags in the repository. Paste the code into `main.go` after the last section.

```
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

At the command line, use [`go get`](https://golang.org/cmd/go/#hdr-Add_dependencies_to_current_module_and_install_them) to add the `oras.land/oras-go/v2` module as a dependency for your module. Use a dot argument to mean "get dependencies for code in the current directory."

```
go get .
go: added github.com/opencontainers/go-digest v1.0.0
go: added github.com/opencontainers/image-spec v1.1.0
go: added golang.org/x/sync v0.6.0
go: added oras.land/oras-go/v2 v2.5.0
```

Run the code.
```
go run .
```

You should see the tags in the repository.

## Push a layer to the repository

All referenced layers must exist in the repository before a manifest can be pushed. The following code snippet demonstrates how to push a manifest layer using the [(*Repository) Push](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Push) method. Paste the code into `main.go` after the last section.

```
// 3. push a layer to the repository
layer := []byte("example manifest layer")
layerDescriptor := content.NewDescriptorFromBytes(v1.MediaTypeImageLayer, layer)
err = repo.Push(ctx, layerDescriptor, bytes.NewReader(layer))
if err != nil {
	panic(err)
}
fmt.Println("Pushed manifest layer:", layerDescriptor.Digest)
```

## Push a manifest to the repository with the tag "quickstart"

The following code snippet demonstrates how to pack a manifest and push it to the repository with the tag "quickstart" using the [PackManifest](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0#PackManifest) and the [(*Repository) Tag](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Tag) methods. Paste the code into `main.go` after the last section.

```
// 4. Push a manifest to the repository with the tag "quickstart"
packOpts := oras.PackManifestOptions{
	Layers: []v1.Descriptor{layerDescriptor},
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

The following code snippet demonstrates how to fetch a manifest from the repository by its tag. First, the descriptor associated with the tag is resolved using [(*Repository) Resolve](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/registry/remote#Repository.Resolve). Then, the content is fetched and read using [FetchAll](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/content#FetchAll) from the [content](https://pkg.go.dev/oras.land/oras-go/v2@v2.5.0/content) package. Paste the code into `main.go` after the last section.

```
// 5. Fetch the manifest from the repository by tag
desc, err = repo.Resolve(ctx, tag)
if err != nil {
	panic(err)
}
fetchedManifestContent, err := content.FetchAll(ctx, repo, desc)
if err != nil {
	panic(err)
}
fmt.Println(string(fetchedManifestContent))
```

## Parse the fetched manifest content and get the layers
The following code snippet demonstrates how to parse the fetched manifest content and get the layers. Paste the code into `main.go` after the last section.

```
// 6. Parse the fetched manifest content and get the layers
var manifest v1.Manifest
if err := json.Unmarshal(fetchedManifestContent, &manifest); err != nil {
	panic(err)
}
for _, layer := manifest.Layers {
	layerContent, err := content.FetchAll(ctx, repo, layer)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(layerContent))
}
```

## Copy the artifact to local OCI layout directory from the repository

The following code snippet demonstrates how to copy an artifact from the repository by its tag and save it to the current directory in the [OCI layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format. The copy operation is performed using the [Copy](https://pkg.go.dev/oras.land/oras-go/v2#Copy) function. Paste the code into `main.go` after the last section.

```
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
```
go run .
```

You should see the following output on the terminal and an OCI layout folder in the current directory.
```
tag1
tag2
tag3
Pushed manifest layer: sha256:4f19474743ecb04b60156ea41b73e06fdf6a5b758e007b788aaa92595dcd3a49
Pushed and tagged manifest
{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.example+type","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2,"data":"e30="},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:4f19474743ecb04b60156ea41b73e06fdf6a5b758e007b788aaa92595dcd3a49","size":22}],"annotations":{"org.opencontainers.image.created":"2025-04-18T03:15:26Z"}}
example manifest layer
Copied the artifact
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
	"fmt"
	"os"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func main() {
	// 1. Connect to a remote repository with basic authentication
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
	layerDescriptor := content.NewDescriptorFromBytes(v1.MediaTypeImageLayer, layer)
	err = repo.Push(ctx, layerDescriptor, bytes.NewReader(layer))
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed manifest layer:", layerDescriptor.Digest)

	// 4. Push a manifest to the repository with the tag "quickstart"
	packOpts := oras.PackManifestOptions{
		Layers: []v1.Descriptor{layerDescriptor},
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
	desc, err = repo.Resolve(ctx, tag)
	if err != nil {
		panic(err)
	}
	fetchedManifestContent, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(fetchedManifestContent))

	// 6. Parse the fetched manifest content and get the layers
	var manifest v1.Manifest
	if err := json.Unmarshal(fetchedManifestContent, &manifest); err != nil {
		panic(err)
	}
	for _, layer := manifest.Layers {
		layerContent, err := content.FetchAll(ctx, repo, layer)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(layerContent))
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
```