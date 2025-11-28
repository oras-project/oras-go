# Migrating from oras-go v2 to v3

This document covers the breaking changes, deprecations, and migration steps for upgrading from oras-go v2 to v3.

## Module Path Change

**PR:** [#1051](https://github.com/oras-project/oras-go/pull/1051)

The module path has changed from `oras.land/oras-go/v2` to `github.com/oras-project/oras-go/v3`.

### Migration

Update all import statements in your code:

```go
// Before (v2)
import "oras.land/oras-go/v2"
import "oras.land/oras-go/v2/registry/remote"

// After (v3)
import "github.com/oras-project/oras-go/v3"
import "github.com/oras-project/oras-go/v3/registry/remote"
```

Update your `go.mod`:

```bash
go get github.com/oras-project/oras-go/v3
```

## Go Version Requirement

**PR:** [#991](https://github.com/oras-project/oras-go/pull/991)

The minimum supported Go version has been updated to Go 1.24. The library supports Go 1.24 and 1.25.

## Registry Reference Changes

### Reference Struct Field Changes

The `Reference` struct has been updated with new fields for better access to parsed components:

```go
type Reference struct {
    Registry   string
    Repository string
    Reference  string  // Deprecated: Use GetReference() instead
    Tag        string  // New: Direct access to tag value
    Digest     string  // New: Direct access to digest string (renamed from DigestString)
}
```

### Method Renames

The `Digest()` method has been renamed to `GetDigest()` to avoid confusion with the new `Digest` field:

```go
// Before (v2)
ref, _ := registry.ParseReference("ghcr.io/repo@sha256:abc123...")
d, err := ref.Digest()

// After (v3)
ref, _ := registry.ParseReference("ghcr.io/repo@sha256:abc123...")
d, err := ref.GetDigest()

// Or access the digest string directly:
digestStr := ref.Digest
```

### New Reference Fields

The `Reference` struct now includes `Tag` and `Digest` fields that are populated during parsing:

```go
// Form A: digest only
ref, _ := registry.ParseReference("registry.io/repo@sha256:abc...")
// ref.Digest = "sha256:abc..."
// ref.Tag = ""

// Form B: tag with digest
ref, _ := registry.ParseReference("registry.io/repo:v1@sha256:abc...")
// ref.Digest = "sha256:abc..."
// ref.Tag = "v1"

// Form C: tag only
ref, _ := registry.ParseReference("registry.io/repo:v1")
// ref.Digest = ""
// ref.Tag = "v1"

// Form D: no reference
ref, _ := registry.ParseReference("registry.io/repo")
// ref.Digest = ""
// ref.Tag = ""
```

### Deprecations

The `Reference.Reference` field is deprecated. Use the `GetReference()` method or access `Tag`/`Digest` fields directly:

```go
// Deprecated
reference := ref.Reference

// Recommended
reference := ref.GetReference()
// Or for specific access:
tag := ref.Tag
digest := ref.Digest
```

## Authentication and Credentials Reorganization

The authentication and credentials packages have been reorganized for better modularity.

### Package Moves

Several types and functions have been moved to new packages:

| v2 Location | v3 Location |
|-------------|-------------|
| `registry/remote/auth.Credential` | `registry/remote/properties.Credential` |
| `registry/remote/credentials/internal/config` | `registry/remote/internal/configuration` |

### New Packages

- `registry/remote/properties` - Contains `Credential` type and related properties
- `registry/remote/internal/configuration` - Internal configuration handling

### Migration Example

```go
// Before (v2)
import "oras.land/oras-go/v2/registry/remote/auth"

cred := auth.Credential{
    Username: "user",
    Password: "pass",
}

// After (v3)
import "github.com/oras-project/oras-go/v3/registry/remote/properties"

cred := properties.Credential{
    Username: "user",
    Password: "pass",
}
```

### CredentialFunc Changes

The `CredentialFunc` type has moved and its signature uses the new `properties.Credential`:

```go
// Before (v2)
import "oras.land/oras-go/v2/registry/remote/auth"

client := &auth.Client{
    CredentialFunc: func(ctx context.Context, hostname string) (auth.Credential, error) {
        return auth.Credential{Username: "user", Password: "pass"}, nil
    },
}

// After (v3)
import (
    "github.com/oras-project/oras-go/v3/registry/remote/auth"
    "github.com/oras-project/oras-go/v3/registry/remote/credentials"
    "github.com/oras-project/oras-go/v3/registry/remote/properties"
)

client := &auth.Client{
    CredentialFunc: func(ctx context.Context, hostname string) (properties.Credential, error) {
        return properties.Credential{Username: "user", Password: "pass"}, nil
    },
}
```

### StaticCredentialFunc

The `StaticCredentialFunc` helper has moved to the credentials package:

```go
// Before (v2)
import "oras.land/oras-go/v2/registry/remote/auth"

credFunc := auth.StaticCredential(registry, auth.Credential{...})

// After (v3)
import (
    "github.com/oras-project/oras-go/v3/registry/remote/credentials"
    "github.com/oras-project/oras-go/v3/registry/remote/properties"
)

credFunc := credentials.StaticCredentialFunc(registry, properties.Credential{...})
```

## Summary of Import Changes

Here's a quick reference for updating your imports:

| v2 Import | v3 Import |
|-----------|-----------|
| `oras.land/oras-go/v2` | `github.com/oras-project/oras-go/v3` |
| `oras.land/oras-go/v2/content` | `github.com/oras-project/oras-go/v3/content` |
| `oras.land/oras-go/v2/content/file` | `github.com/oras-project/oras-go/v3/content/file` |
| `oras.land/oras-go/v2/content/memory` | `github.com/oras-project/oras-go/v3/content/memory` |
| `oras.land/oras-go/v2/content/oci` | `github.com/oras-project/oras-go/v3/content/oci` |
| `oras.land/oras-go/v2/registry` | `github.com/oras-project/oras-go/v3/registry` |
| `oras.land/oras-go/v2/registry/remote` | `github.com/oras-project/oras-go/v3/registry/remote` |
| `oras.land/oras-go/v2/registry/remote/auth` | `github.com/oras-project/oras-go/v3/registry/remote/auth` |
| `oras.land/oras-go/v2/registry/remote/credentials` | `github.com/oras-project/oras-go/v3/registry/remote/credentials` |
| N/A | `github.com/oras-project/oras-go/v3/registry/remote/properties` (new) |

## Automated Migration

You can use `sed` or similar tools to update imports across your codebase:

```bash
# Update import paths
find . -name "*.go" -exec sed -i '' 's|oras.land/oras-go/v2|github.com/oras-project/oras-go/v3|g' {} \;

# Update Digest() to GetDigest()
find . -name "*.go" -exec sed -i '' 's|\.Digest()|.GetDigest()|g' {} \;

# Update go.mod
go mod edit -replace oras.land/oras-go/v2=github.com/oras-project/oras-go/v3@latest
go mod tidy
```

## Getting Help

If you encounter issues during migration:

- Open an issue at [github.com/oras-project/oras-go/issues](https://github.com/oras-project/oras-go/issues)
- Check the [examples](https://github.com/oras-project/oras-go/tree/main/example_test.go) for updated usage patterns
- Review the [API documentation](https://pkg.go.dev/github.com/oras-project/oras-go/v3)
