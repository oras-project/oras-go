package oras_test

import (
	"context"
	"fmt"
	"testing"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/warning"
)

func TestUse(t *testing.T) {
	ref := "localhost:8080/test0720:v1"
	repo, err := remote.NewRepository(ref)
	if err != nil {
		panic(err)
	}

	client := warning.Client{
		Client: auth.DefaultClient,
		HandleWarning: func(warning warning.Warning) {
			fmt.Println(warning.Text)
		},
	}
	repo.Client = &client
	repo.PlainHTTP = true

	ctx := context.Background()
	desc, err := repo.Resolve(ctx, ref)
	if err != nil {
		panic(err)
	}
	fmt.Println(desc)
}
