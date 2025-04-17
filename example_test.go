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

package oras_test

import (
	"context"
	"fmt"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// ExamplePullFilesFromRemoteRepository gives an example of pulling files from
// a remote repository to the local file system.
func Example_pullFilesFromRemoteRepository() {
	// 0. Create a file store
	fs, err := file.New("/tmp/")
	if err != nil {
		panic(err)
	}
	defer fs.Close()

	// 1. Connect to a remote repository
	ctx := context.Background()
	reg := "myregistry.example.com"
	repo, err := remote.NewRepository(reg + "/myrepo")
	if err != nil {
		panic(err)
	}
	// Note: The below code can be omitted if authentication is not required
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
		Credential: auth.StaticCredential(reg, auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 2. Copy from the remote repository to the file store
	tag := "latest"
	manifestDescriptor, err := oras.Copy(ctx, repo, tag, fs, tag, oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest descriptor:", manifestDescriptor)
}

// ExamplePullImageFromRemoteRepository gives an example of pulling an image
// from a remote repository to an OCI Image layout folder.
func Example_pullImageFromRemoteRepository() {
	// 0. Create an OCI layout store
	store, err := oci.New("/tmp/oci-layout-root")
	if err != nil {
		panic(err)
	}

	// 1. Connect to a remote repository
	ctx := context.Background()
	reg := "myregistry.example.com"
	repo, err := remote.NewRepository(reg + "/myrepo")
	if err != nil {
		panic(err)
	}
	// Note: The below code can be omitted if authentication is not required
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
		Credential: auth.StaticCredential(reg, auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 2. Copy from the remote repository to the OCI layout store
	tag := "latest"
	manifestDescriptor, err := oras.Copy(ctx, repo, tag, store, tag, oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest descriptor:", manifestDescriptor)
}

// ExamplePullImageUsingDockerCredentials gives an example of pulling an image
// from a remote repository to an OCI Image layout folder using Docker
// credentials.
func Example_pullImageUsingDockerCredentials() {
	// 0. Create an OCI layout store
	store, err := oci.New("/tmp/oci-layout-root")
	if err != nil {
		panic(err)
	}

	// 1. Connect to a remote repository
	ctx := context.Background()
	reg := "docker.io"
	repo, err := remote.NewRepository(reg + "/user/my-repo")
	if err != nil {
		panic(err)
	}

	// prepare authentication using Docker credentials
	storeOpts := credentials.StoreOptions{}
	credStore, err := credentials.NewStoreFromDocker(storeOpts)
	if err != nil {
		panic(err)
	}
	repo.Client = &auth.Client{
		Client:     retry.DefaultClient,
		Cache:      auth.NewCache(),
		Credential: credentials.Credential(credStore), // Use the credentials store
	}

	// 2. Copy from the remote repository to the OCI layout store
	tag := "latest"
	manifestDescriptor, err := oras.Copy(ctx, repo, tag, store, tag, oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}

	fmt.Println("manifest pulled:", manifestDescriptor.Digest, manifestDescriptor.MediaType)
}

// ExamplePushFilesToRemoteRepository gives an example of pushing local files
// to a remote repository.
func Example_pushFilesToRemoteRepository() {
	// 0. Create a file store
	fs, err := file.New("/tmp/")
	if err != nil {
		panic(err)
	}
	defer fs.Close()
	ctx := context.Background()

	// 1. Add files to the file store
	mediaType := "application/vnd.test.file"
	fileNames := []string{"/tmp/myfile"}
	fileDescriptors := make([]v1.Descriptor, 0, len(fileNames))
	for _, name := range fileNames {
		fileDescriptor, err := fs.Add(ctx, name, mediaType, "")
		if err != nil {
			panic(err)
		}
		fileDescriptors = append(fileDescriptors, fileDescriptor)
		fmt.Printf("file descriptor for %s: %v\n", name, fileDescriptor)
	}

	// 2. Pack the files and tag the packed manifest
	artifactType := "application/vnd.test.artifact"
	opts := oras.PackManifestOptions{
		Layers: fileDescriptors,
	}
	manifestDescriptor, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, artifactType, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest descriptor:", manifestDescriptor)

	tag := "latest"
	if err = fs.Tag(ctx, manifestDescriptor, tag); err != nil {
		panic(err)
	}

	// 3. Connect to a remote repository
	reg := "myregistry.example.com"
	repo, err := remote.NewRepository(reg + "/myrepo")
	if err != nil {
		panic(err)
	}
	// Note: The below code can be omitted if authentication is not required
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
		Credential: auth.StaticCredential(reg, auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 4. Copy from the file store to the remote repository
	_, err = oras.Copy(ctx, fs, tag, repo, tag, oras.DefaultCopyOptions)
	if err != nil {
		panic(err)
	}
}

// ExampleAttachBlobToRemoteRepository gives an example of attaching a blob to an
// existing artifact in a remote repository. The blob is packed as a manifest whose
// subject is the existing artifact.
func Example_attachBlobToRemoteRepository() {
	// 0. Connect to a remote repository with basic authentication
	registry := "myregistry.example.com"
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

	// 1. Resolve the subject descriptor
	ctx := context.Background()
	subjectDescriptor, err := repo.Resolve(ctx, "sha256:f3a0356fe9f82b925c2f15106d3932252f36c1c56fd35be6c369d274f433d177")
	if err != nil {
		panic(err)
	}

	// 2. Prepare the blob to be attached
	blob := []byte("example blob")

	// 3. Push the blob to the repository
	blobDescriptor, err := oras.PushBytes(ctx, repo, v1.MediaTypeImageLayer, blob)
	if err != nil {
		panic(err)
	}
	fmt.Println("pushed the blob to the repository")

	// 4. Pack the blob as a manifest with version v1.1 and push it to the repository
	packOpts := oras.PackManifestOptions{
		Layers:  []v1.Descriptor{blobDescriptor},
		Subject: &subjectDescriptor,
	}
	artifactType := "application/vnd.example+type"
	referrerDescriptor, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, packOpts)
	if err != nil {
		panic(err)
	}
	fmt.Printf("attached %s to %s\n", referrerDescriptor.Digest, subjectDescriptor.Digest)
}
