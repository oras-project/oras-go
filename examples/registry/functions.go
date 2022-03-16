package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

func PushBlob(ctx context.Context, mediaType string, blob []byte, registryUri string, repositoryName string, useHttp bool) (desc ocispec.Descriptor, err error) {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return desc, err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return desc, err
	}
	desc = ocispec.Descriptor{ // Generate descriptor based on the media type and blob content
		MediaType: mediaType,
		Digest:    digest.FromBytes(blob), // Calculate digest
		Size:      int64(len(blob)),       // Include blob size
	}
	return desc, repository.Push(ctx, desc, bytes.NewReader(blob)) // Push the blob to the registry target
}

func GenerateManifestContent(config ocispec.Descriptor, layers ...ocispec.Descriptor) ([]byte, error) {
	content := ocispec.Manifest{
		Config:    config, // Set config blob
		Layers:    layers, // Set layer blobs
		Versioned: specs.Versioned{SchemaVersion: 2},
	}
	return json.Marshal(content) // Get json content
}

func PullBlob(ctx context.Context, descriptor ocispec.Descriptor, registryUri string, repositoryName string, useHttp bool) (io.ReadCloser, error) {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return nil, err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp              // whether HTTP or HTTPS is used
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return nil, err
	}
	return repository.Fetch(ctx, descriptor) // Pull the blob
}

func PullBlobSafely(ctx context.Context, desc ocispec.Descriptor, registryUri string, repositoryName string, useHttp bool) ([]byte, error) {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return nil, err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return nil, err
	}
	return content.FetchAll(ctx, repository, desc) // Pull blob with
}

func TagManifest(ctx context.Context, descToTag ocispec.Descriptor, tagName string, registryUri string, repositoryName string, useHttp bool) error {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return err
	}
	return repository.Tag(ctx, descToTag, tagName)
}

func PushTagManifest(ctx context.Context, tagName string, manifestBlob []byte, registryUri string, repositoryName string, useHttp bool) error {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return err
	}

	descriptorToTag := ocispec.Descriptor{ // Generate descriptor based on the media type and blob content
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBlob), // Calculate digest
		Size:      int64(len(manifestBlob)),       // Include blob size
	}
	return repository.PushTag(ctx, descriptorToTag, bytes.NewReader(manifestBlob), tagName)
}

func ResolveTag(ctx context.Context, tagName string, registryUri string, repositoryName string, useHttp bool) (ret ocispec.Descriptor, err error) {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return ret, err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return ret, err
	}
	return repository.Resolve(ctx, tagName)
}

func CopyManifest(ctx context.Context, sourceResigtryUri string, sourceRepoName string, sourceTagName string, sourceUseHttp bool, targetRegistryUri string, targetRepoName string, targetTagName string, targetUseHttp bool) (copied ocispec.Descriptor, err error) {
	// 1. Get source registry and repository
	srcRegistry, err := remote.NewRegistry(sourceResigtryUri) // Get registry
	if err != nil {
		return copied, err
	}
	srcRegistry.RepositoryOptions.PlainHTTP = sourceUseHttp
	sourceRepository, err := srcRegistry.Repository(ctx, sourceRepoName) // Get respository
	if err != nil {
		return copied, err
	}

	// 2. Get target registry and repository
	targetRegistry, err := remote.NewRegistry(targetRegistryUri) // Get registry
	if err != nil {
		return copied, err
	}
	targetRegistry.RepositoryOptions.PlainHTTP = targetUseHttp
	targetRepository, err := targetRegistry.Repository(ctx, targetRepoName) // Get respository
	if err != nil {
		return copied, err
	}

	// 3. copy
	return oras.Copy(ctx, sourceRepository, sourceTagName, targetRepository, targetTagName)
}

func GetRegistryCatalog(ctx context.Context, registryUri string, useHttp bool) (ret []string, err error) {
	targetRegistry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return ret, err
	}
	targetRegistry.RepositoryOptions.PlainHTTP = useHttp
	return registry.Repositories(ctx, targetRegistry)
}

func GetRepositoryTagList(ctx context.Context, registryUri string, repositoryName string, useHttp bool, fn func([]string) error) (err error) {
	registry, err := remote.NewRegistry(registryUri) // Get registry
	if err != nil {
		return err
	}
	registry.RepositoryOptions.PlainHTTP = useHttp
	repository, err := registry.Repository(ctx, repositoryName) // Get respository
	if err != nil {
		return err
	}
	return repository.Tags(ctx, fn)
}
