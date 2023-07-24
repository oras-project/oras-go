package oras_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func pullFiles() error {
	// 0. Create a file store
	fs, err := file.New("/tmp/")
	if err != nil {
		return err
	}
	defer fs.Close()

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

	// 2. Copy from the remote repository to the file store
	tag := "latest"
	manifestDescriptor, err := oras.Copy(ctx, repo, tag, fs, tag, oras.DefaultCopyOptions)
	if err != nil {
		return err
	}
	fmt.Println("manifest descriptor:", manifestDescriptor)
	return nil
}

func Example_pullFilesFromRemoteRepository() {
	if err := pullFiles(); err != nil {
		panic(err)
	}
}
