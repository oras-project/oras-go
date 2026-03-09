# Migrating from ORAS Go v2 to v3

This guide covers the breaking changes and migration steps for upgrading from ORAS Go v2 to v3.

## AI-Assisted Migration

For bulk migration of a codebase, use the ready-made AI prompt:

**[ai-migration-prompt.md](ai-migration-prompt.md)** — copy the prompt into Claude, Copilot, or any AI assistant along with your source files. It covers all breaking changes, search-and-replace patterns, and guidance for adopting new v3 features.

## Summary of Breaking Changes

| Change | Impact |
|--------|--------|
| Module path changed (`oras.land/oras-go/v2` → `github.com/oras-project/oras-go/v3`) | All imports |
| `auth.Credential` → `credentials.Credential` | Auth code |
| `auth.Client.Credential` → `auth.Client.CredentialFunc` | Auth configuration |
| `ForceAttemptOAuth2` removed; use `SetLegacyMode()` (inverted semantics) | Auth configuration |
| `repo.Client/PlainHTTP/HandleWarning/Policy` → `repo.Registry.*` | Repository configuration |
| `repo.Reference.Repository` → `repo.RepositoryName` | Repository configuration |
| `ref.Reference` field deprecated → use `ref.GetReference()` | Reference parsing |
| `RepositoryOptions` type removed | Repository construction |

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

## New Packages in v3

| Package | Description |
|---------|-------------|
| `registry/remote/policy` | `containers-policy.json` enforcement (`Evaluator.IsImageAllowed`) |
| `registry/remote/signature` | Atomic container signature signing/verification (`LookasideStore`, OpenPGP) |
| `registry/remote/config` | Unified config loader (`LoadConfigsWithOptions`) for policy, registries.d, Docker config, certs |
| `registry/remote/properties` | Typed registry configuration (`Registry`, `Transport`, `Mirror`) |
| `registry/remote/builder.go` | `ClientBuilder` factory for auth clients and repositories |
| `registry/remote/middleware.go` | `RepositoryMiddleware`, `WithPolicyEnforcement`, `Compose` |
| `registry/remote/mirror.go` | Mirror fallback logic with `PullFromMirror*` policies |
| `content/cache` | Caching wrapper (`cache.New`) for content stores |
| `objects` | ORM-like API for images, artifacts, and image indexes |

## Reference Docs

- [Config-to-Properties Mapping](config-properties-mapping.md) - Field-by-field `registries.conf` → `properties.Registry` mapping

## Config-Based Repository Creation

v3 introduces a bridge between container configuration files and the builder pattern:

```go
import (
    "github.com/oras-project/oras-go/v3/registry/remote"
    "github.com/oras-project/oras-go/v3/registry/remote/config"
)

// Load registries.conf for transport settings.
regConf, _ := config.LoadRegistriesConfig("/etc/containers/registries.conf")

// Convert config to properties (resolves aliases, rewrites references,
// applies insecure settings, checks blocked registries).
props, _ := regConf.RegistryProperties("docker.io/library/alpine:latest")

builder := remote.NewClientBuilder()
builder.CredentialStore = myCredentialStore

repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

## Community Support

If you encounter challenges during migration, seek assistance from the community by [submitting GitHub issues](https://github.com/oras-project/oras-go/issues/new) or asking in the [#oras](https://cloud-native.slack.com/archives/CJ1KHJM5Z) Slack channel.
