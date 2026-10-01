package oras_test

import (
	"testing"

	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestRegistryRepositorySupportsExtendedVerify(t *testing.T) {
	var (
		_ oras.ReadOnlyGraphTarget     = (*remote.Repository)(nil)
		_ content.ReadOnlyGraphStorage = (*remote.Repository)(nil)
	)
}
