# ORAS Object-Relational Model (ORM) Design Plan

## Executive Summary

This document outlines the design for an object-oriented ORM layer for ORAS-Go that provides high-level, type-safe abstractions for working with OCI artifacts, container images, blobs, and manifests. The ORM will sit above the existing ORAS core APIs and provide an intuitive, object-oriented interface while maintaining full compatibility with the underlying storage and registry protocols.

## 1. Architecture Overview

### 1.1 Layer Architecture

```
┌─────────────────────────────────────────────────┐
│          Application Code                       │
│  - Use typed models (Image, Artifact, Blob)     │
│  - Query and navigate relationships             │
│  - Create and persist content                   │
├─────────────────────────────────────────────────┤
│    ORM Layer (NEW)                              │
│  ├── Models (orm/models/)                       │
│  │   ├── Base Model                             │
│  │   ├── Artifact, Image, Index, Blob           │
│  │   └── Reference                              │
│  ├── Client (orm/client.go)                     │
│  │   ├── Session management                     │
│  │   ├── Identity map (caching)                 │
│  │   └── Lazy loading                           │
│  ├── Repository (orm/repository.go)             │
│  │   ├── Query builders                         │
│  │   ├── CRUD operations                        │
│  │   └── Relationship loading                   │
│  └── Builders (orm/builders/)                   │
│      ├── Fluent API for manifest creation       │
│      └── Type-safe artifact construction        │
├─────────────────────────────────────────────────┤
│  ORAS Core APIs (Existing)                      │
│  ├── Copy, Pack, Fetch, Push, Tag               │
│  ├── Registry Client                            │
│  └── Storage implementations                    │
└─────────────────────────────────────────────────┘
```

### 1.2 Design Principles

1. **Type Safety**: Strong typing for different artifact types
2. **Immutability**: Content-addressed objects are immutable once created
3. **Lazy Loading**: Fetch manifest details only when accessed
4. **Relationship Navigation**: Intuitive navigation between related objects
5. **Compatibility**: Works with all existing ORAS storage implementations
6. **Extensibility**: Easy to add new artifact types
7. **Performance**: Minimal overhead, efficient caching

## 2. Object Model Design

### 2.1 Core Model Hierarchy

```
Content (interface)
├── Descriptor() ocispec.Descriptor
├── Digest() digest.Digest
├── MediaType() string
├── Size() int64
└── Annotations() map[string]string

Manifest (interface extends Content)
├── Subject() *Manifest
├── SetSubject(*Manifest)
├── Config() *Blob
├── Layers() []*Blob
└── Predecessors() ([]*Manifest, error)

Blob (struct implements Content)
├── descriptor ocispec.Descriptor
├── content []byte (lazy)
├── client *Client
└── Methods:
    ├── Read() (io.ReadCloser, error)
    ├── Bytes() ([]byte, error)
    └── Push(ctx) error

Artifact (struct implements Manifest)
├── descriptor ocispec.Descriptor
├── manifest *artifactspec.Manifest (lazy)
├── artifactType string
├── blobs []*Blob (lazy)
├── subject *Manifest (lazy)
├── client *Client
└── Methods:
    ├── ArtifactType() string
    ├── Blobs() ([]*Blob, error)
    ├── AddBlob(*Blob)
    ├── Referrers() ([]*Manifest, error)
    └── Push(ctx, reference) error

Image (struct implements Manifest)
├── descriptor ocispec.Descriptor
├── manifest *ocispec.Manifest (lazy)
├── config *Blob (lazy)
├── layers []*Blob (lazy)
├── subject *Manifest (lazy)
├── platform *ocispec.Platform
├── client *Client
└── Methods:
    ├── Config() (*Blob, error)
    ├── Layers() ([]*Blob, error)
    ├── Platform() *ocispec.Platform
    ├── AddLayer(*Blob)
    ├── SetConfig(*Blob)
    └── Push(ctx, reference) error

Index (struct implements Manifest)
├── descriptor ocispec.Descriptor
├── index *ocispec.Index (lazy)
├── manifests []*Manifest (lazy)
├── subject *Manifest (lazy)
├── client *Client
└── Methods:
    ├── Manifests() ([]*Manifest, error)
    ├── AddManifest(Manifest)
    ├── FilterByPlatform(*ocispec.Platform) ([]*Manifest, error)
    └── Push(ctx, reference) error

Reference (struct)
├── name string
├── manifest Manifest
├── client *Client
└── Methods:
    ├── Name() string
    ├── Resolve() (Manifest, error)
    └── Tag(Manifest) error
```

### 2.2 Model Responsibilities

#### Blob
- Represents binary content (layers, configs, arbitrary data)
- Lazy loads actual content bytes
- Provides streaming and byte access
- Tracks media type and annotations

#### Artifact
- Represents OCI artifact manifests
- Contains typed blobs with specific purpose
- Supports subject references (referrers pattern)
- Provides artifact type metadata

#### Image
- Represents container images (OCI or Docker)
- Separates config from layers
- Platform-aware (architecture, OS)
- Supports image-specific operations

#### Index
- Represents manifest lists/indexes
- Contains multiple manifests (multi-platform images)
- Platform-based filtering
- Supports hierarchical artifact organization

#### Reference
- Represents tags and named references
- Resolves to manifests
- Manages tag operations

## 3. Client Design

### 3.1 ORM Client Interface

```go
package orm

type Client struct {
    target      oras.Target          // Underlying ORAS target
    identityMap map[digest.Digest]Content  // Cache loaded objects
    options     ClientOptions
}

type ClientOptions struct {
    Cache        bool              // Enable identity map
    PreloadDepth int               // Auto-preload relationships (0=lazy)
    Concurrency  int               // Concurrent fetch operations
}

// Constructor
func NewClient(target oras.Target, opts ...ClientOption) *Client

// Factory methods
func (c *Client) NewBlob(mediaType string, content []byte) *Blob
func (c *Client) NewArtifact(artifactType string) *Artifact
func (c *Client) NewImage() *Image
func (c *Client) NewIndex() *Index

// Fetch operations
func (c *Client) FetchByDigest(ctx context.Context, digest digest.Digest) (Content, error)
func (c *Client) FetchByReference(ctx context.Context, ref string) (Manifest, error)
func (c *Client) FetchBlob(ctx context.Context, desc ocispec.Descriptor) (*Blob, error)
func (c *Client) FetchManifest(ctx context.Context, desc ocispec.Descriptor) (Manifest, error)

// Query operations
func (c *Client) Query() *QueryBuilder

// Relationship operations
func (c *Client) LoadPredecessors(ctx context.Context, content Content) ([]*Manifest, error)
func (c *Client) LoadSuccessors(ctx context.Context, manifest Manifest) ([]Content, error)

// Persistence
func (c *Client) Flush(ctx context.Context) error
```

### 3.2 Identity Map Pattern

The client maintains an identity map to ensure:
- Single instance per digest (object identity)
- Avoid redundant fetches
- Consistent state across relationships
- Memory-efficient caching

```go
// When fetching content
func (c *Client) getOrFetch(ctx context.Context, desc ocispec.Descriptor) (Content, error) {
    // Check cache first
    if cached, ok := c.identityMap[desc.Digest]; ok {
        return cached, nil
    }

    // Fetch and cache
    content := c.fetch(ctx, desc)
    c.identityMap[desc.Digest] = content
    return content, nil
}
```

## 4. Repository Pattern

### 4.1 Repository Interface

```go
package orm

type Repository struct {
    client *Client
}

// CRUD operations
func (r *Repository) Create(ctx context.Context, content Content) error
func (r *Repository) FindByDigest(ctx context.Context, digest digest.Digest) (Content, error)
func (r *Repository) FindByReference(ctx context.Context, ref string) (Manifest, error)
func (r *Repository) Delete(ctx context.Context, digest digest.Digest) error

// Query operations
func (r *Repository) FindArtifacts(ctx context.Context, artifactType string) ([]*Artifact, error)
func (r *Repository) FindImages(ctx context.Context) ([]*Image, error)
func (r *Repository) FindByAnnotation(ctx context.Context, key, value string) ([]Content, error)

// Relationship queries
func (r *Repository) FindReferrers(ctx context.Context, subject Manifest) ([]*Manifest, error)
func (r *Repository) FindReferences(ctx context.Context, blob *Blob) ([]*Manifest, error)

// Tag operations
func (r *Repository) Tag(ctx context.Context, manifest Manifest, ref string) error
func (r *Repository) ListTags(ctx context.Context) ([]string, error)
func (r *Repository) ResolveTag(ctx context.Context, tag string) (Manifest, error)
```

### 4.2 Query Builder

```go
type QueryBuilder struct {
    repo       *Repository
    filters    []Filter
    preloads   []string
    limit      int
    offset     int
}

func (qb *QueryBuilder) Where(filter Filter) *QueryBuilder
func (qb *QueryBuilder) MediaType(mt string) *QueryBuilder
func (qb *QueryBuilder) Annotation(key, value string) *QueryBuilder
func (qb *QueryBuilder) ArtifactType(at string) *QueryBuilder
func (qb *QueryBuilder) Preload(relationships ...string) *QueryBuilder
func (qb *QueryBuilder) Limit(n int) *QueryBuilder
func (qb *QueryBuilder) Offset(n int) *QueryBuilder
func (qb *QueryBuilder) Find(ctx context.Context) ([]Content, error)
func (qb *QueryBuilder) First(ctx context.Context) (Content, error)

// Example usage:
// client.Query().
//     ArtifactType("application/vnd.example+type").
//     Annotation("version", "1.0").
//     Preload("Blobs", "Subject").
//     Find(ctx)
```

## 5. Builder Pattern

### 5.1 Fluent Builders

```go
// Artifact Builder
type ArtifactBuilder struct {
    client       *Client
    artifactType string
    blobs        []*Blob
    subject      *Manifest
    annotations  map[string]string
}

func (c *Client) BuildArtifact(artifactType string) *ArtifactBuilder
func (ab *ArtifactBuilder) WithBlob(blob *Blob) *ArtifactBuilder
func (ab *ArtifactBuilder) WithSubject(subject Manifest) *ArtifactBuilder
func (ab *ArtifactBuilder) WithAnnotation(key, value string) *ArtifactBuilder
func (ab *ArtifactBuilder) Build(ctx context.Context) (*Artifact, error)
func (ab *ArtifactBuilder) BuildAndPush(ctx context.Context, ref string) (*Artifact, error)

// Image Builder
type ImageBuilder struct {
    client      *Client
    config      *Blob
    layers      []*Blob
    platform    *ocispec.Platform
    subject     *Manifest
    annotations map[string]string
}

func (c *Client) BuildImage() *ImageBuilder
func (ib *ImageBuilder) WithConfig(config *Blob) *ImageBuilder
func (ib *ImageBuilder) AddLayer(layer *Blob) *ImageBuilder
func (ib *ImageBuilder) WithPlatform(platform *ocispec.Platform) *ImageBuilder
func (ib *ImageBuilder) WithSubject(subject Manifest) *ImageBuilder
func (ib *ImageBuilder) WithAnnotation(key, value string) *ImageBuilder
func (ib *ImageBuilder) Build(ctx context.Context) (*Image, error)
func (ib *ImageBuilder) BuildAndPush(ctx context.Context, ref string) (*Image, error)

// Index Builder
type IndexBuilder struct {
    client      *Client
    manifests   []Manifest
    subject     *Manifest
    annotations map[string]string
}

func (c *Client) BuildIndex() *IndexBuilder
func (ib *IndexBuilder) AddManifest(manifest Manifest) *IndexBuilder
func (ib *IndexBuilder) WithSubject(subject Manifest) *IndexBuilder
func (ib *IndexBuilder) WithAnnotation(key, value string) *IndexBuilder
func (ib *IndexBuilder) Build(ctx context.Context) (*Index, error)
func (ib *IndexBuilder) BuildAndPush(ctx context.Context, ref string) (*Index, error)
```

## 6. Usage Examples

### 6.1 Creating and Pushing an Artifact

```go
// Create ORM client from ORAS target
client := orm.NewClient(target)

// Create blobs
configBlob := client.NewBlob("application/json", configData)
dataBlob := client.NewBlob("application/octet-stream", payload)

// Build artifact
artifact := client.BuildArtifact("application/vnd.example+type").
    WithBlob(configBlob).
    WithBlob(dataBlob).
    WithAnnotation("version", "1.0.0").
    BuildAndPush(ctx, "example.com/myartifact:v1.0.0")

fmt.Printf("Pushed artifact: %s\n", artifact.Digest())
```

### 6.2 Creating and Pushing a Container Image

```go
client := orm.NewClient(target)

// Create config
config := client.NewBlob("application/vnd.oci.image.config.v1+json", configJSON)

// Create layers
layer1 := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer1Data)
layer2 := client.NewBlob("application/vnd.oci.image.layer.v1.tar+gzip", layer2Data)

// Build image
image := client.BuildImage().
    WithConfig(config).
    AddLayer(layer1).
    AddLayer(layer2).
    WithPlatform(&ocispec.Platform{
        Architecture: "amd64",
        OS:           "linux",
    }).
    BuildAndPush(ctx, "example.com/myimage:latest")

fmt.Printf("Pushed image: %s\n", image.Digest())
```

### 6.3 Fetching and Navigating Relationships

```go
client := orm.NewClient(target)

// Fetch image by reference
image, err := client.FetchByReference(ctx, "example.com/myimage:latest")
if err != nil {
    log.Fatal(err)
}

// Navigate to config
config, err := image.(*orm.Image).Config()
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Config: %s (%d bytes)\n", config.Digest(), config.Size())

// Navigate to layers
layers, err := image.(*orm.Image).Layers()
if err != nil {
    log.Fatal(err)
}
for i, layer := range layers {
    fmt.Printf("Layer %d: %s (%d bytes)\n", i, layer.Digest(), layer.Size())
}

// Find referrers (signatures, SBOMs, etc.)
referrers, err := image.Predecessors()
if err != nil {
    log.Fatal(err)
}
for _, ref := range referrers {
    if artifact, ok := ref.(*orm.Artifact); ok {
        fmt.Printf("Referrer: %s (type: %s)\n",
            artifact.Digest(), artifact.ArtifactType())
    }
}
```

### 6.4 Querying Artifacts

```go
client := orm.NewClient(target)
repo := orm.NewRepository(client)

// Find all SBOMs
sboms, err := repo.FindArtifacts(ctx, "application/spdx+json")
if err != nil {
    log.Fatal(err)
}

// Find artifacts by annotation
artifacts, err := client.Query().
    Annotation("org.opencontainers.image.version", "1.0.0").
    Preload("Blobs", "Subject").
    Find(ctx)

// Find all container images
images, err := repo.FindImages(ctx)
```

### 6.5 Creating Multi-Platform Image Index

```go
client := orm.NewClient(target)

// Build image for linux/amd64
amd64Image := client.BuildImage().
    WithConfig(amd64Config).
    AddLayer(amd64Layer1).
    AddLayer(amd64Layer2).
    WithPlatform(&ocispec.Platform{
        Architecture: "amd64",
        OS:           "linux",
    }).
    Build(ctx)

// Build image for linux/arm64
arm64Image := client.BuildImage().
    WithConfig(arm64Config).
    AddLayer(arm64Layer1).
    AddLayer(arm64Layer2).
    WithPlatform(&ocispec.Platform{
        Architecture: "arm64",
        OS:           "linux",
    }).
    Build(ctx)

// Create index
index := client.BuildIndex().
    AddManifest(amd64Image).
    AddManifest(arm64Image).
    BuildAndPush(ctx, "example.com/myimage:latest")

// Query by platform
arm64Manifests, err := index.FilterByPlatform(&ocispec.Platform{
    Architecture: "arm64",
    OS:           "linux",
})
```

## 7. Implementation Plan

### Phase 1: Core Models (Week 1-2)
1. Implement base `Content` interface
2. Implement `Blob` model with lazy loading
3. Implement `Artifact` model
4. Implement `Image` model
5. Implement `Index` model
6. Implement `Reference` model
7. Write unit tests for each model

**Deliverables:**
- `orm/models/content.go`
- `orm/models/blob.go`
- `orm/models/artifact.go`
- `orm/models/image.go`
- `orm/models/index.go`
- `orm/models/reference.go`
- `orm/models/*_test.go`

### Phase 2: Client and Identity Map (Week 2-3)
1. Implement `Client` struct
2. Implement identity map caching
3. Implement factory methods (NewBlob, NewArtifact, etc.)
4. Implement fetch operations
5. Implement relationship loading
6. Write integration tests with memory store

**Deliverables:**
- `orm/client.go`
- `orm/client_test.go`
- `orm/options.go`

### Phase 3: Repository Pattern (Week 3-4)
1. Implement `Repository` struct
2. Implement CRUD operations
3. Implement query operations
4. Implement relationship queries
5. Write integration tests

**Deliverables:**
- `orm/repository.go`
- `orm/repository_test.go`

### Phase 4: Query Builder (Week 4-5)
1. Implement `QueryBuilder` struct
2. Implement filter predicates
3. Implement preloading logic
4. Implement pagination
5. Optimize query performance
6. Write comprehensive tests

**Deliverables:**
- `orm/query.go`
- `orm/filters.go`
- `orm/query_test.go`

### Phase 5: Builders (Week 5-6)
1. Implement `ArtifactBuilder`
2. Implement `ImageBuilder`
3. Implement `IndexBuilder`
4. Add validation logic
5. Write builder tests

**Deliverables:**
- `orm/builders/artifact.go`
- `orm/builders/image.go`
- `orm/builders/index.go`
- `orm/builders/*_test.go`

### Phase 6: Documentation and Examples (Week 6-7)
1. Write comprehensive godoc documentation
2. Create example programs
3. Write migration guide from raw ORAS API
4. Performance benchmarking
5. Integration testing with real registries

**Deliverables:**
- `orm/README.md`
- `orm/examples/`
- `orm/MIGRATION.md`
- Performance benchmarks

### Phase 7: Polish and Release (Week 7-8)
1. Code review and refactoring
2. Performance optimization
3. API stability review
4. Create release notes
5. Update main ORAS documentation

**Deliverables:**
- Stable ORM API
- Release notes
- Updated documentation

## 8. File Structure

```
oras-go/
├── orm/
│   ├── client.go              # ORM client with identity map
│   ├── client_test.go
│   ├── options.go             # Client options and configuration
│   ├── repository.go          # Repository pattern implementation
│   ├── repository_test.go
│   ├── query.go               # Query builder
│   ├── query_test.go
│   ├── filters.go             # Query filter predicates
│   ├── models/
│   │   ├── content.go         # Content interface
│   │   ├── blob.go            # Blob model
│   │   ├── blob_test.go
│   │   ├── artifact.go        # Artifact model
│   │   ├── artifact_test.go
│   │   ├── image.go           # Image model
│   │   ├── image_test.go
│   │   ├── index.go           # Index model
│   │   ├── index_test.go
│   │   ├── reference.go       # Reference model
│   │   └── reference_test.go
│   ├── builders/
│   │   ├── artifact.go        # Artifact builder
│   │   ├── artifact_test.go
│   │   ├── image.go           # Image builder
│   │   ├── image_test.go
│   │   ├── index.go           # Index builder
│   │   └── index_test.go
│   ├── examples/
│   │   ├── create_artifact/
│   │   │   └── main.go
│   │   ├── create_image/
│   │   │   └── main.go
│   │   ├── query_artifacts/
│   │   │   └── main.go
│   │   └── navigate_relationships/
│   │       └── main.go
│   ├── README.md              # ORM documentation
│   └── MIGRATION.md           # Migration guide
└── ...existing ORAS files...
```

## 9. Design Decisions and Rationale

### 9.1 Why Lazy Loading?

**Decision**: Manifests and their relationships are loaded lazily.

**Rationale**:
- Manifests can be large and deeply nested
- Many use cases only need descriptor metadata
- Reduces unnecessary network/disk I/O
- Maintains compatibility with streaming operations
- Users can opt-in to preloading via query builder

### 9.2 Why Identity Map?

**Decision**: Client maintains a digest-based identity map.

**Rationale**:
- Ensures object identity (one instance per digest)
- Prevents redundant fetches
- Maintains consistency in object graphs
- Aligns with content-addressable storage model
- Can be disabled for memory-constrained environments

### 9.3 Why Immutable Models?

**Decision**: Content objects are immutable after creation.

**Rationale**:
- Aligns with content-addressable storage
- Digest changes if content changes
- Prevents accidental mutations
- Thread-safe by design
- Reflects OCI spec semantics

### 9.4 Why Separate Artifact/Image/Index Types?

**Decision**: Distinct types instead of generic Manifest.

**Rationale**:
- Type safety prevents errors (e.g., adding layers to artifacts)
- IDE autocomplete and documentation
- Different semantic meaning
- Clear API contracts
- Extensible for future manifest types

### 9.5 Why Builder Pattern?

**Decision**: Fluent builders for constructing manifests.

**Rationale**:
- Complex object construction with many optional fields
- Validation at build time
- Readable and self-documenting code
- Common pattern in Go ecosystem
- Prevents invalid state

## 10. Performance Considerations

### 10.1 Caching Strategy

- **Identity Map**: Digest-based caching per client session
- **TTL**: Optional time-to-live for cached entries
- **Memory Limits**: Configurable max cache size with LRU eviction
- **Preloading**: Batch fetch related content to reduce round trips

### 10.2 Concurrency

- **Parallel Fetching**: Concurrent loading of independent relationships
- **Lock-Free Reads**: Immutable objects enable concurrent access
- **Batch Operations**: Group multiple operations for efficiency

### 10.3 Memory Efficiency

- **Lazy Loading**: Don't load content until accessed
- **Streaming**: Support streaming large blobs
- **Weak References**: Consider weak references for cached objects
- **Pooling**: Reuse buffers for JSON marshaling/unmarshaling

## 11. Testing Strategy

### 11.1 Unit Tests
- Test each model in isolation
- Mock underlying ORAS operations
- Test lazy loading behavior
- Test relationship navigation
- Test builder validation

### 11.2 Integration Tests
- Test with memory store
- Test with OCI layout store
- Test with remote registry (using test registry)
- Test concurrent operations
- Test caching behavior

### 11.3 Performance Tests
- Benchmark model creation
- Benchmark relationship traversal
- Benchmark query operations
- Memory profiling
- Concurrency stress tests

## 12. Future Enhancements

### 12.1 Advanced Queries
- Full-text search in annotations
- Complex filter expressions (AND/OR/NOT)
- Aggregation queries (count, group by)
- Graph traversal queries (find all descendants)

### 12.2 Change Tracking
- Track modifications to models
- Unit of work pattern for batch commits
- Transaction support
- Rollback capability

### 12.3 Validation
- Schema validation for artifact types
- Custom validation rules
- Referential integrity checks

### 12.4 Hooks and Events
- Pre/post create hooks
- Pre/post fetch hooks
- Change notification events
- Audit logging

### 12.5 Migration Tools
- Import from other formats
- Export to different manifest types
- Schema migration utilities

## 13. Compatibility and Backward Compatibility

### 13.1 ORAS Core Compatibility
- ORM layer built on top of existing ORAS APIs
- No changes to core ORAS interfaces
- Works with all existing storage implementations
- No breaking changes to existing code

### 13.2 OCI Spec Compliance
- Full OCI Image Spec v1.1 support
- OCI Artifact Spec support
- Docker Manifest v2 support
- Extensible for future spec versions

### 13.3 Interoperability
- ORM-created content readable by standard tools
- Standard content readable by ORM
- No proprietary extensions or metadata

## 14. Security Considerations

### 14.1 Content Verification
- Digest verification on fetch
- Size verification
- Media type validation

### 14.2 Input Validation
- Sanitize annotations
- Validate media types
- Check for malformed manifests
- Prevent injection attacks

### 14.3 Resource Limits
- Max manifest size
- Max relationship depth
- Max cache size
- Timeout configurations

## 15. Success Criteria

The ORM implementation will be considered successful when:

1. **Functionality**: All core operations (create, read, query, navigate) work correctly
2. **Performance**: <10% overhead compared to raw ORAS API calls
3. **Usability**: Reduces code complexity by >50% for common use cases
4. **Testing**: >90% code coverage with comprehensive integration tests
5. **Documentation**: Complete API documentation and usage examples
6. **Adoption**: Positive feedback from early adopters and contributors

## 16. Conclusion

This ORM layer will provide a significant improvement in developer experience for working with OCI artifacts in Go. By providing type-safe, object-oriented abstractions while maintaining full compatibility with the underlying ORAS APIs and OCI specifications, we enable developers to build artifact-centric applications with less code and fewer errors.

The phased implementation approach allows for incremental delivery and feedback, while the comprehensive testing strategy ensures reliability and performance. The design is extensible and future-proof, ready to accommodate new manifest types and OCI specification updates.
