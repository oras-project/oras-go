# Reference Struct Changes

**PRs:**
- [#1042](https://github.com/oras-project/oras-go/pull/1042) - Tag and Digest fields
- [#1091](https://github.com/oras-project/oras-go/pull/1091) - URI scheme stripping
- [#1045](https://github.com/oras-project/oras-go/pull/1045) - ParseReferenceList

The `Reference` struct has been enhanced with new fields and methods while maintaining backward compatibility.

## Important: New properties.Reference Type

For new code, prefer using `properties.Reference` from `registry/remote/properties` instead of the original `registry.Reference`. The `registry.Reference` type is deprecated and will be removed in a future version.

```go
// New code should use:
import "github.com/oras-project/oras-go/v3/registry/remote/properties"

ref, err := properties.NewReference("ghcr.io/repo:v1")
// ref is properties.Reference

// Create a registry configuration with the reference
registry, err := properties.NewRegistry("ghcr.io/repo:v1")
// registry.Reference is properties.Reference

// Parse multiple references
refs, err := properties.NewReferenceList("ghcr.io/repo:v1,v2,v3")
```

The `properties.Reference` type has the same fields and methods as `registry.Reference`, but without the deprecated `Reference` field.

## Summary of Changes

| Change | Type |
|--------|------|
| Added `Tag` field | New feature |
| Added `Digest` field | New feature |
| Added `GetReference()` method | New feature |
| Deprecated `Reference` field | Deprecation |
| URI scheme stripping | New feature |
| `ParseReferenceList()` function | New feature |
| New `properties.Reference` type | New feature |
| Deprecated `registry.Reference` type | Deprecation |

## Reference Struct

### v2 Structure

```go
type Reference struct {
    Registry   string
    Repository string
    Reference  string  // Could be tag OR digest
}
```

### v3 Structure

```go
type Reference struct {
    Registry   string
    Repository string
    Reference  string  // Deprecated: Use GetReference() or Tag/Digest fields
    Tag        string  // New: The tag if provided
    Digest     string  // New: The digest if provided
}
```

## Migration

### 1. Reading Reference Values

The `Reference` field still works but is deprecated. Use `GetReference()` for the combined value, or access `Tag`/`Digest` directly.

```go
// Before (v2)
ref, _ := registry.ParseReference("ghcr.io/repo:v1")
fmt.Println(ref.Reference) // "v1"

// After (v3) - Option 1: Use GetReference()
ref, _ := registry.ParseReference("ghcr.io/repo:v1")
fmt.Println(ref.GetReference()) // "v1"

// After (v3) - Option 2: Use specific fields
fmt.Println(ref.Tag)    // "v1"
fmt.Println(ref.Digest) // ""
```

### 2. Handling Form B References (Tag + Digest)

In v2, Form B references (`repo:tag@digest`) would lose the tag. In v3, both are preserved.

```go
input := "ghcr.io/repo:v1@sha256:abc123..."

// v2 behavior
ref, _ := registry.ParseReference(input)
fmt.Println(ref.Reference) // "sha256:abc123..." (tag lost!)

// v3 behavior
ref, _ := registry.ParseReference(input)
fmt.Println(ref.Tag)           // "v1"
fmt.Println(ref.Digest)        // "sha256:abc123..."
fmt.Println(ref.GetReference()) // "sha256:abc123..." (digest takes precedence)
fmt.Println(ref.Reference)     // "sha256:abc123..." (deprecated, same as GetReference)
```

### 3. Checking Reference Type

```go
// Before (v2)
ref, _ := registry.ParseReference(input)
if strings.HasPrefix(ref.Reference, "sha256:") {
    // It's a digest
} else {
    // It's a tag
}

// After (v3)
ref, _ := registry.ParseReference(input)
if ref.Digest != "" {
    // It's a digest (or Form B with both)
}
if ref.Tag != "" {
    // It's a tag (or Form B with both)
}
```

## New Features

### URI Scheme Stripping

`ParseReference()` now automatically strips common URI schemes, enabling interoperability with tools that use URI-style references.

```go
// All of these now work:
registry.ParseReference("ghcr.io/repo:v1")         // Standard
registry.ParseReference("oci://ghcr.io/repo:v1")   // Helm, Argo, Kustomize style
registry.ParseReference("http://localhost/repo:v1") // Plain HTTP
registry.ParseReference("https://ghcr.io/repo:v1")  // HTTPS
```

Supported schemes (case-sensitive, lowercase only):
- `oci://`
- `http://`
- `https://`

### ParseReferenceList

New function for parsing comma-separated reference lists.

```go
// Parse multiple tags
refs, _ := registry.ParseReferenceList("ghcr.io/repo:v1,v2,v3")
// Returns 3 References: repo:v1, repo:v2, repo:v3

// Parse multiple digests
refs, _ := registry.ParseReferenceList("ghcr.io/repo@sha256:aaa,sha256:bbb")
// Returns 2 References with those digests
```

## Reference Forms

The Reference struct supports four forms as defined by the OCI Distribution Spec:

| Form | Example | Tag | Digest | Reference |
|------|---------|-----|--------|-----------|
| A | `repo@sha256:...` | `""` | `sha256:...` | `sha256:...` |
| B | `repo:tag@sha256:...` | `tag` | `sha256:...` | `sha256:...` |
| C | `repo:tag` | `tag` | `""` | `tag` |
| D | `repo` | `""` | `""` | `""` |

## Search and Replace Patterns

| Find | Replace |
|------|---------|
| `.Reference` (for reading) | `.GetReference()` or `.Tag`/`.Digest` |
| `if ref.Reference == ""` | `if ref.GetReference() == ""` |
