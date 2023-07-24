package oras_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func pullImageFromRemote() error {
	// 0. Create an OCI layout store
	store, err := oci.New("/tmp/oci-layout-root")
	if err != nil {
		return err
	}

	// 1. Connect to a remote repository
	ctx := context.Background()
	reg := "myregistry.example.com"
	repo, err := remote.NewRepository(reg + "/myrepo")
	if err != nil {
		return err
	}
	// Note: The below code can be omitted if authentication is not required
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.DefaultCache,
		Credential: auth.StaticCredential(reg, auth.Credential{
			Username: "username",
			Password: "password",
		}),
	}

	// 2. Copy from the remote repository to the OCI layout store
	tag := "latest"
	manifestDescriptor, err := oras.Copy(ctx, repo, tag, store, tag, oras.DefaultCopyOptions)
	if err != nil {
		return err
	}
	fmt.Println("manifest pulled:", manifestDescriptor.Digest, manifestDescriptor.MediaType)

	// 3. Fetch from OCI layout store to verify
	fetched, err := content.FetchAll(ctx, store, manifestDescriptor)
	if err != nil {
		return err
	}
	fmt.Printf("manifest content:\n%s", fetched)
	return nil
}

func Example_pullImageFromRemoteRepository() {
	if err := pullImageFromRemote(); err != nil {
		panic(err)
	}
}
