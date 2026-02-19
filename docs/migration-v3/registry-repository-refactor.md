# Registry/Repository Refactor

The architecture has been refactored so that `Registry` holds registry-level concerns (Client, Policy, PlainHTTP) and `Repository` references a `Registry` instead of holding its own Client directly.

## Summary of Changes

| v2 | v3 |
|----|-----|
| `Repository.Client` field | `Repository.Registry.Client` |
| `Repository.PlainHTTP` field | `Repository.Registry.PlainHTTP` |
| `Repository.HandleWarning` field | `Repository.Registry.HandleWarning` |
| `Repository.Reference` field (registry.Reference) | `Repository.RepositoryName` (string) + `Repository.Reference()` method |
| `RepositoryOptions` type | Removed |

## Migration

### 1. Accessing Client, PlainHTTP, and HandleWarning

These fields have moved from `Repository` to `Registry`. Access them via the `Registry` pointer.

```go
// Before (v2)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
repo.Client = myClient
repo.PlainHTTP = true
repo.HandleWarning = func(warning remote.Warning) {
    log.Println(warning.Text)
}

// After (v3)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
repo.Registry.Client = myClient
repo.Registry.PlainHTTP = true
repo.Registry.HandleWarning = func(warning remote.Warning) {
    log.Println(warning.Text)
}
```

### 2. Repository Reference Access

The `Reference` field has been replaced with a `RepositoryName` string field and a `Reference()` method.

```go
// Before (v2)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
registryHost := repo.Reference.Registry
repoName := repo.Reference.Repository

// After (v3)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
registryHost := repo.Registry.Reference.Registry
repoName := repo.RepositoryName
// Or use the Reference() method to get the full reference:
ref := repo.Reference() // Returns registry.Reference with both Registry and Repository
```

### 3. Creating Multiple Repositories from Same Registry

The new architecture makes it cleaner to create multiple repositories that share configuration.

```go
// Before (v2) - Configuration duplicated on each repository
repo1, _ := remote.NewRepository("registry.example.com/repo1")
repo1.Client = myClient
repo1.PlainHTTP = true

repo2, _ := remote.NewRepository("registry.example.com/repo2")
repo2.Client = myClient  // Duplicated
repo2.PlainHTTP = true   // Duplicated

// After (v3) - Shared registry configuration
reg, _ := remote.NewRegistry("registry.example.com")
reg.Client = myClient
reg.PlainHTTP = true

ctx := context.Background()
repo1, _ := reg.Repository(ctx, "repo1")  // Shares registry config
repo2, _ := reg.Repository(ctx, "repo2")  // Shares registry config
```

### 4. Policy Configuration

Policy is now configured on the `Registry` and shared by all repositories.

```go
// Before (v2)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
repo.Policy = evaluator

// After (v3)
repo, _ := remote.NewRepository("registry.example.com/myrepo")
repo.Registry.Policy = evaluator

// Or when using Registry directly:
reg, _ := remote.NewRegistry("registry.example.com")
reg.Policy = evaluator
repo, _ := reg.Repository(ctx, "myrepo")  // Inherits policy from registry
```

### 5. RepositoryOptions Removed

The `RepositoryOptions` type alias has been removed. Use `Repository` directly or create repositories via `Registry.Repository()`.

```go
// Before (v2)
opts := remote.RepositoryOptions{
    Client:   myClient,
    PlainHTTP: true,
}

// After (v3)
// Option 1: Configure via Repository.Registry
repo, _ := remote.NewRepository("registry.example.com/myrepo")
repo.Registry.Client = myClient
repo.Registry.PlainHTTP = true

// Option 2: Create via Registry (preferred for multiple repos)
reg, _ := remote.NewRegistry("registry.example.com")
reg.Client = myClient
reg.PlainHTTP = true
repo, _ := reg.Repository(ctx, "myrepo")
```

## Search and Replace Patterns

| Find | Replace |
|------|---------|
| `repo.Client =` | `repo.Registry.Client =` |
| `repo.PlainHTTP =` | `repo.Registry.PlainHTTP =` |
| `repo.HandleWarning =` | `repo.Registry.HandleWarning =` |
| `repo.Policy =` | `repo.Registry.Policy =` |
| `repo.Reference.Registry` | `repo.Registry.Reference.Registry` |
| `repo.Reference.Repository` | `repo.RepositoryName` |

## New Features

### Registry as First-Class Citizen

The `Registry` struct now serves as a proper parent for repositories:

```go
type Registry struct {
    Client             Client                   // HTTP client (nil uses auth.DefaultClient)
    Reference          registry.Reference       // Registry host information
    PlainHTTP          bool                     // Use HTTP instead of HTTPS
    HandleWarning      func(warning Warning)    // Warning handler
    Policy             *policy.Evaluator        // Access control policy
    MaxMetadataBytes   int64                    // Metadata response size limit
    // ... additional fields
}
```

### Repository References Registry

The `Repository` struct now holds a pointer to its parent `Registry`:

```go
type Repository struct {
    Registry       *Registry    // Parent registry (must not be nil)
    RepositoryName string       // Repository name (e.g., "library/alpine")
    // ... repository-specific overrides
}

// Reference() returns the full registry.Reference
func (r *Repository) Reference() registry.Reference {
    return registry.Reference{
        Registry:   r.Registry.Reference.Registry,
        Repository: r.RepositoryName,
    }
}
```

### NewRegistryWithProperties

A new function for creating registries from properties:

```go
props := properties.Properties{
    Registry:  "registry.example.com",
    PlainHTTP: true,
}
reg, err := remote.NewRegistryWithProperties(props)
```
