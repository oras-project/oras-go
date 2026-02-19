# Credential Type Relocation

**PR:** [#1006](https://github.com/oras-project/oras-go/pull/1006)

The `Credential` type and related functions have been moved from the `auth` package to a dedicated `credentials` package for better separation of concerns.

## What Changed

| v2 (auth package) | v3 (credentials package) |
|-------------------|--------------------------|
| `auth.Credential` | `credentials.Credential` |
| `auth.EmptyCredential` | `credentials.EmptyCredential` |
| `auth.CredentialFunc` | `credentials.CredentialFunc` |
| `auth.StaticCredential()` | `credentials.StaticCredentialFunc()` |

## Migration

### 1. Update Imports

```go
// Before (v2)
import "oras.land/oras-go/v2/registry/remote/auth"

// After (v3)
import "github.com/oras-project/oras-go/v3/registry/remote/credentials"
```

If you also use `auth.Client`, you'll need both imports:

```go
import (
    "github.com/oras-project/oras-go/v3/registry/remote/auth"
    "github.com/oras-project/oras-go/v3/registry/remote/credentials"
)
```

### 2. Update Type References

```go
// Before (v2)
var cred auth.Credential = auth.Credential{
    Username: "user",
    Password: "token",
}

// After (v3)
var cred credentials.Credential = credentials.Credential{
    Username: "user",
    Password: "token",
}
```

### 3. Update EmptyCredential References

```go
// Before (v2)
if cred == auth.EmptyCredential {
    // no credentials
}

// After (v3)
if cred == credentials.EmptyCredential {
    // no credentials
}

// Or use the new IsEmpty method
if cred.IsEmpty() {
    // no credentials
}
```

### 4. Update StaticCredential Calls

Note: The function was also renamed from `StaticCredential` to `StaticCredentialFunc`.

```go
// Before (v2)
credFunc := auth.StaticCredential("registry.example.com", cred)

// After (v3)
credFunc := credentials.StaticCredentialFunc("registry.example.com", cred)
```

### 5. Update CredentialFunc Type References

```go
// Before (v2)
var resolver auth.CredentialFunc = func(ctx context.Context, host string) (auth.Credential, error) {
    return auth.Credential{Username: "user"}, nil
}

// After (v3)
var resolver credentials.CredentialFunc = func(ctx context.Context, host string) (credentials.Credential, error) {
    return credentials.Credential{Username: "user"}, nil
}
```

## Search and Replace Patterns

For automated migration, use these patterns:

| Find | Replace |
|------|---------|
| `auth.Credential{` | `credentials.Credential{` |
| `auth.EmptyCredential` | `credentials.EmptyCredential` |
| `auth.CredentialFunc` | `credentials.CredentialFunc` |
| `auth.StaticCredential(` | `credentials.StaticCredentialFunc(` |

## AuthConfig Type Relocation

The `AuthConfig` type and related functions have been moved from the `config` package to the `credentials` package. The `config` package now imports from `credentials` and provides deprecated type aliases for backward compatibility.

### What Changed

| config package (deprecated) | credentials package (new location) |
|-----------------------------|-----------------------------------|
| `config.AuthConfig` | `credentials.AuthConfig` |
| `config.NewAuthConfig()` | `credentials.NewAuthConfig()` |
| `config.EncodeAuth()` | `credentials.EncodeAuth()` |
| `config.ErrInvalidAuthConfig` | `credentials.ErrInvalidAuthConfig` |

### Migration

Update your imports and type references:

```go
// Before
import "github.com/oras-project/oras-go/v3/registry/remote/config"

authCfg := config.AuthConfig{
    Auth: config.EncodeAuth("user", "pass"),
}

// After
import "github.com/oras-project/oras-go/v3/registry/remote/credentials"

authCfg := credentials.AuthConfig{
    Auth: credentials.EncodeAuth("user", "pass"),
}
```

### Backward Compatibility

The `config` package provides deprecated type aliases that forward to `credentials`:
- `config.AuthConfig` is now `credentials.AuthConfig`
- `config.NewAuthConfig` forwards to `credentials.NewAuthConfig`
- `config.EncodeAuth` forwards to `credentials.EncodeAuth`
- `config.ErrInvalidAuthConfig` forwards to `credentials.ErrInvalidAuthConfig`

Existing code using `config.AuthConfig` will continue to work but will generate deprecation warnings. Update to use `credentials.AuthConfig` directly.

## New Features

The `credentials` package also provides:

- `Credential.IsEmpty()` method for checking empty credentials
- `NewCredential(AuthConfig)` for creating credentials from Docker auth config
- Integration with credential stores (file, memory, native keychain)
