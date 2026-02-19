# Migrating from ORAS Go v2 to v3

This guide covers the breaking changes and migration steps for upgrading from ORAS Go v2 to v3.

## Summary of Breaking Changes

| Change | Impact | Migration Guide |
|--------|--------|-----------------|
| Module path changed | All imports | [Module Name](module-name.md) |
| Credential type relocated | Auth code | [Credential Relocation](credential-relocation.md) |
| Auth client API changes | Auth configuration | [Auth Client Changes](auth-client-changes.md) |
| Reference struct enhanced | Reference parsing | [Reference Struct](reference-struct.md) |
| Registry/Repository refactor | Repository configuration | [Registry/Repository Refactor](registry-repository-refactor.md) |

## Quick Start

1. **Update module path** in all imports:
   ```go
   // v2
   import "oras.land/oras-go/v2"

   // v3
   import "github.com/oras-project/oras-go/v3"
   ```

2. **Update credential imports**:
   ```go
   // v2
   import "oras.land/oras-go/v2/registry/remote/auth"
   cred := auth.Credential{Username: "user"}

   // v3
   import "github.com/oras-project/oras-go/v3/registry/remote/credentials"
   cred := credentials.Credential{Username: "user"}
   ```

3. **Update auth client configuration**:
   ```go
   // v2
   client.Credential = auth.StaticCredential(reg, cred)

   // v3
   client.CredentialFunc = credentials.StaticCredentialFunc(reg, cred)
   ```

4. **Update repository field access** (Client, PlainHTTP, etc. moved to Registry):
   ```go
   // v2
   repo.Client = myClient
   repo.PlainHTTP = true

   // v3
   repo.Registry.Client = myClient
   repo.Registry.PlainHTTP = true
   ```

5. Run `go mod tidy` to update dependencies.

## Migration Guides

- [Module Name Change](module-name.md) - Module path migration
- [Credential Relocation](credential-relocation.md) - Credential type package move
- [Auth Client Changes](auth-client-changes.md) - Authentication API changes
- [Reference Struct](reference-struct.md) - Reference parsing enhancements
- [Registry/Repository Refactor](registry-repository-refactor.md) - Repository configuration changes

## Non-Breaking Additions in v3

- **URI Scheme Support**: `ParseReference()` now accepts `oci://`, `http://`, and `https://` schemes
- **Reference List Parsing**: New `ParseReferenceList()` for batch operations
- **Tag/Digest Fields**: Direct access to tag and digest in `Reference` struct
- **Registry Properties**: New `properties` package for registry configuration
- **Cache Support**: New `cache` package for content caching
- **Policy Support**: Container policy (`policy.json`) support
- **TokenFetcher Interface**: Pluggable token acquisition strategies in `auth.Client`
- **ClientBuilder**: Builder pattern for creating `auth.Client` from registry properties
- **Repository Middleware**: Middleware pattern for cross-cutting concerns (policy enforcement, warnings)
- **Composable Interfaces**: New `ReadableRepository`, `WritableRepository`, and `ReadOnlyRepository` interfaces for narrower function signatures
- **Config-to-Properties Bridge**: Convert `registries.conf` settings into `properties.Registry` for seamless builder integration
- **Mirror Support**: Registry mirrors from `registries.conf` are populated into `properties.Registry.Mirrors`
- **Unified Config Loader**: `config.LoadConfigs()` loads both Docker `config.json` and `registries.conf` in one call

## Config-Based Repository Creation

v3 introduces a bridge between container configuration files and the builder pattern. Load a `registries.conf` to get transport settings (insecure, reference rewriting, blocked registries), then feed the resulting properties to the builder:

```go
import (
    "github.com/oras-project/oras-go/v3/registry/remote"
    "github.com/oras-project/oras-go/v3/registry/remote/config"
    "github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

// Load registries.conf for transport settings.
regConf, _ := config.LoadRegistriesConfig("/etc/containers/registries.conf")

// Convert config to properties (resolves aliases, rewrites references,
// applies insecure settings, checks blocked registries).
props, _ := regConf.RegistryProperties("docker.io/library/alpine:latest")

// Create a credential store for authentication.
builder := remote.NewClientBuilder()
builder.CredentialStore = myCredentialStore

// Build a repository from properties.
repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

For unqualified image names, use `SearchRegistryProperties` to try each configured search registry:

```go
results, _ := regConf.SearchRegistryProperties("alpine:latest")
for _, props := range results {
    repo, err := remote.NewRepositoryWithProperties(props, builder)
    // try each registry in order...
}
```

## Community Support

If you encounter challenges during migration, seek assistance from the community by [submitting GitHub issues](https://github.com/oras-project/oras-go/issues/new) or asking in the [#oras](https://cloud-native.slack.com/archives/CJ1KHJM5Z) Slack channel.
