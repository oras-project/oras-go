package oras_test

import (
	"context"
	"fmt"
	"testing"

	"oras.land/oras-go/v2/registry/remote"
)

func TestUse(t *testing.T) {
	ref := "localhost:8080/test0720:v1"
	repo, err := remote.NewRepository(ref)
	if err != nil {
		panic(err)
	}
	repo.PlainHTTP = true
	repo.HandleWarning = func(w remote.Warning) {
		fmt.Println("%s/%s: %s: %s", w.Reference.Registry, w.Reference.Repository, w.URL.Path, w.Text)
	}

	ctx := context.Background()
	desc, err := repo.Resolve(ctx, ref)
	if err != nil {
		panic(err)
	}
	fmt.Println(desc)
}
