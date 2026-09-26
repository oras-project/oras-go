# ORAS Objects

The ORAS objects package provides a type-safe, object-oriented interface for working with OCI artifacts, container images, blobs, and manifests. It sits on top of the existing ORAS Core APIs and provides an intuitive, high-level abstraction while maintaining full compatibility with all ORAS storage implementations.

## Features

- **Type Safety**: Strongly-typed models (Artifact, Image, Index, Blob) prevent common errors
- **Lazy Loading**: Content is fetched only when accessed, reducing unnecessary I/O
- **Identity Map**: Digest-based caching prevents redundant fetches and ensures object identity
- **Fluent Builders**: Declarative, chainable API for constructing manifests
- **Relationship Navigation**: Easy traversal of manifest relationships (layers, configs, referrers)
- **Full Compatibility**: Works with all existing ORAS storage implementations

## Quick Start

```go
package main

import (
    "context"
    "log"

    "oras.land/oras-go/v2/content/memory"
    "oras.land/oras-go/v2/objects"
)

func main() {
    ctx := context.Background()

    // Create objects client
    store := memory.New()
    client := objects.NewClient(store)

    // Create and push an artifact
    configBlob := client.NewBlob("application/json", []byte(`{"version": "1.0"}`))
    dataBlob := client.NewBlob("application/octet-stream", []byte("payload"))

    artifact, err := client.BuildArtifact("application/vnd.example+type").
        AddBlob(configBlob).
        AddBlob(dataBlob).
        WithAnnotation("version", "1.0.0").
        BuildAndPush(ctx, "myartifact:v1.0.0")

    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Created artifact: %s", artifact.Digest())
}
```

## Core Concepts

### Models

The objects package provides four main model types:

#### Blob

Represents binary content (layers, configs, arbitrary data).

```go
// Create from bytes
blob := client.NewBlob("application/json", data)

// Push to storage
err := blob.Push(ctx)

// Read content (streaming)
reader, err := blob.Read(ctx)

// Get content as bytes (cached)
bytes, err := blob.Bytes(ctx)
```

#### Artifact

Represents OCI artifact manifests with typed blobs.

```go
artifact, err := client.BuildArtifact("application/vnd.example+type").
    AddBlob(blob1).
    AddBlob(blob2).
    WithSubject(parentManifest).
    WithAnnotation("key", "value").
    BuildAndPush(ctx, "myartifact:v1")

// Access blobs
blobs, err := artifact.Blobs(ctx)

// Get artifact type
artifactType, err := artifact.ArtifactType(ctx)

// Find referrers
referrers, err := artifact.Predecessors(ctx)
```

#### Image

Represents container images (OCI or Docker) with config and layers.

```go
image, err := client.BuildImage().
    WithConfig(configBlob).
    AddLayer(layer1).
    AddLayer(layer2).
    WithPlatform(&ocispec.Platform{
        Architecture: "amd64",
        OS:           "linux",
    }).
    BuildAndPush(ctx, "myimage:latest")

// Navigate to config
config, err := image.Config(ctx)

// Navigate to layers
layers, err := image.Layers(ctx)

// Get platform
platform, err := image.Platform(ctx)
```

#### Index

Represents manifest lists/indexes for multi-platform images.

```go
index, err := client.BuildIndex().
    AddManifest(amd64Image).
    AddManifest(arm64Image).
    BuildAndPush(ctx, "myimage:latest")

// Get all manifests
manifests, err := index.Manifests(ctx)

// Filter by platform
arm64Manifests, err := index.FilterByPlatform(ctx, &ocispec.Platform{
    Architecture: "arm64",
    OS:           "linux",
})
```

## Usage Examples

### Creating an Artifact

```go
ctx := context.Background()
client := objects.NewClient(store)

// Create blobs
configBlob := client.NewBlob("application/json", configData)
dataBlob := client.NewBlob("application/octet-stream", payload)

// Push blobs
configBlob.Push(ctx)
dataBlob.Push(ctx)

// Build and push artifact
artifact, err := client.BuildArtifact("application/vnd.example+type").
    AddBlob(configBlob).
    AddBlob(dataBlob).
    WithAnnotation("version", "1.0.0").
    BuildAndPush(ctx, "example.com/myartifact:v1.0.0")
```

### Creating a Container Image

```go
// Create config and layers
config := client.NewBlob("application/vnd.oci.image.config.v1+json", configJSON)
layer1 := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer1Data)
layer2 := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer2Data)

// Push blobs
config.Push(ctx)
layer1.Push(ctx)
layer2.Push(ctx)

// Build image
image, err := client.BuildImage().
    WithConfig(config).
    AddLayer(layer1).
    AddLayer(layer2).
    WithPlatform(&ocispec.Platform{
        Architecture: "amd64",
        OS:           "linux",
    }).
    BuildAndPush(ctx, "example.com/myimage:latest")
```

### Fetching and Navigating Relationships

```go
// Fetch image by reference
manifest, err := client.FetchByReference(ctx, "example.com/myimage:latest")
image := manifest.(*models.Image)

// Navigate to config
config, err := image.Config(ctx)
fmt.Printf("Config: %s (%d bytes)\n", config.Digest(), config.Size())

// Navigate to layers
layers, err := image.Layers(ctx)
for i, layer := range layers {
    fmt.Printf("Layer %d: %s (%d bytes)\n", i, layer.Digest(), layer.Size())
}

// Find referrers (signatures, SBOMs, etc.)
referrers, err := image.Predecessors(ctx)
for _, ref := range referrers {
    fmt.Printf("Referrer: %s\n", ref.Digest())
}
```

### Creating Multi-Platform Images

```go
// Build platform-specific images
amd64Image, _ := client.BuildImage().
    WithConfig(amd64Config).
    AddLayer(amd64Layer).
    WithPlatform(&ocispec.Platform{
        Architecture: "amd64",
        OS:           "linux",
    }).
    Build(ctx)

arm64Image, _ := client.BuildImage().
    WithConfig(arm64Config).
    AddLayer(arm64Layer).
    WithPlatform(&ocispec.Platform{
        Architecture: "arm64",
        OS:           "linux",
    }).
    Build(ctx)

// Create index
index, err := client.BuildIndex().
    AddManifest(amd64Image).
    AddManifest(arm64Image).
    BuildAndPush(ctx, "example.com/myimage:latest")
```

## Client Options

The objects client can be configured with various options:

```go
client := objects.NewClient(store,
    objects.WithCache(true),           // Enable identity map (default: true)
    objects.WithMaxCacheSize(100),     // Limit cache entries (default: 0 = unlimited)
)
```

### Cache (Identity Map)

The identity map ensures that only one instance of each piece of content exists in memory, based on its digest. This provides:

- Object identity (same digest = same object instance)
- Prevents redundant fetches
- Consistent state across relationships

Disable caching for memory-constrained environments:

```go
client := objects.NewClient(store, objects.WithCache(false))
```

### Lazy Loading

By default, the objects package uses lazy loading - manifest content and relationships are only fetched when accessed. This minimizes unnecessary I/O operations.

## Architecture

```
Application Code
      ↓
Objects Layer (Client, Builders, Models)
      ↓
ORAS Core APIs (unchanged)
      ↓
Storage Implementations (Memory, OCI, Remote)
```

The objects layer:
- Wraps ORAS core APIs with object-oriented models
- Provides fluent builders for manifest construction
- Manages caching and lazy loading
- Handles relationship navigation
- Maintains full compatibility with existing ORAS code

## Examples

See the [examples](./examples/) directory for complete, runnable examples:

- [create_artifact](./examples/create_artifact/) - Creating and pushing an artifact
- [create_image](./examples/create_image/) - Creating and pushing a container image

## Design Document

For detailed design decisions and implementation details, see [ORM_DESIGN_PLAN.md](../ORM_DESIGN_PLAN.md).

## Compatibility

- No breaking changes to existing ORAS APIs
- Works with all existing storage implementations (Memory, OCI, File, Remote)
- Full OCI Image Spec v1.1 compliance
- OCI Artifact Spec support
- Docker Manifest v2 support

## Status

**Phase 1 (Core Models)**: Complete
- Content interface and base types
- Blob model with lazy loading
- Artifact, Image, Index models
- Reference model
- Objects Client with identity map
- Fluent builders

**Next Phases**:
- Repository pattern and query builder
- Comprehensive testing
- Performance optimization
- Documentation and examples

## Contributing

This is an experimental feature under active development. Feedback and contributions are welcome!

## License

Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0.
