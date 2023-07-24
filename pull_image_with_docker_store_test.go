package oras_test

import (
	"context"
	"fmt"

	credentials "github.com/oras-project/oras-credentials-go"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func pullImageUsingDockerCredentialStore() error {

	// 0. Create an OCI layout store
	store, err := oci.New("/tmp/oci-layout-root")
	if err != nil {
		return err
	}

	// 1. Connect to a remote repository
	ctx := context.Background()
	reg := "docker.io"
	repo, err := remote.NewRepository(reg + "/user/my-repo")
	if err != nil {
		return err
	}

	// 2. Get credentials from the docker credential store
	storeOpts := credentials.StoreOptions{}
	credStore, err := credentials.NewStoreFromDocker(storeOpts)
	if err != nil {
		return err
	}

	// Prepare the auth client for the registry and credential store
	repo.Client = &auth.Client{
		Client:     retry.DefaultClient,
		Cache:      auth.DefaultCache,
		Credential: credentials.Credential(credStore), // Use the credential store
	}

	// 3. Copy from the remote repository to the OCI layout store
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

func Example_pullImageUsingDockerCredentialStore() {
	if err := pullImageUsingDockerCredentialStore(); err != nil {
		panic(err)
	}
}
