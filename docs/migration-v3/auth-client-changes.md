# Auth Client Changes

**PRs:**
- [#1006](https://github.com/oras-project/oras-go/pull/1006) - Credential field rename
- [#1038](https://github.com/oras-project/oras-go/pull/1038) - Legacy mode changes

The `auth.Client` struct has several breaking changes in v3 to improve API clarity and encapsulation.

## Summary of Changes

| v2 | v3 |
|----|-----|
| `Client.Credential` field | `Client.CredentialFunc` field |
| `Client.ForceAttemptOAuth2` field | `Client.SetLegacyMode()` method |

## Migration

### 1. Credential Field Rename

The field was renamed from `Credential` to `CredentialFunc` to better describe its purpose.

```go
// Before (v2)
client := &auth.Client{
    Credential: auth.StaticCredential("registry.example.com", cred),
}

// After (v3)
client := &auth.Client{
    CredentialFunc: credentials.StaticCredentialFunc("registry.example.com", cred),
}
```

### 2. ForceAttemptOAuth2 Replacement

The `ForceAttemptOAuth2` boolean field has been replaced with the `SetLegacyMode()` method.

**Important: The semantics are inverted!**

| v2 | v3 | Behavior |
|----|-----|----------|
| `ForceAttemptOAuth2 = true` | `SetLegacyMode(false)` | Use OAuth2 with password grant |
| `ForceAttemptOAuth2 = false` (default) | `SetLegacyMode(true)` | Use legacy distribution spec |

```go
// Before (v2) - Force OAuth2
client := &auth.Client{}
client.ForceAttemptOAuth2 = true

// After (v3) - Use OAuth2 (default behavior, no action needed)
client := &auth.Client{}
// OAuth2 is now the default

// Before (v2) - Use legacy (default)
client := &auth.Client{}
// ForceAttemptOAuth2 defaults to false

// After (v3) - Use legacy (must explicitly set)
client := &auth.Client{}
client.SetLegacyMode(true)
```

### Understanding the Behavior

**OAuth2 Mode (v3 default, `SetLegacyMode(false)`):**
- Uses OAuth2 with password grant when username/password are provided
- Uses OAuth2 with refresh token grant when refresh token is provided
- Compliant with [Distribution Spec OAuth2](https://distribution.github.io/distribution/spec/auth/oauth/)

**Legacy Mode (`SetLegacyMode(true)`):**
- Uses Basic authentication when username/password are provided
- Falls back to distribution spec token endpoint for anonymous access
- Matches v1/v2 behavior
- Compliant with [Distribution Spec JWT](https://distribution.github.io/distribution/spec/auth/jwt/)

### Complete Migration Example

```go
// Before (v2)
import "oras.land/oras-go/v2/registry/remote/auth"

client := &auth.Client{
    Client: http.DefaultClient,
    Header: http.Header{
        "User-Agent": {"my-app/1.0"},
    },
    Credential: auth.StaticCredential("ghcr.io", auth.Credential{
        Username: "user",
        Password: "token",
    }),
    Cache: auth.DefaultCache,
}
client.ForceAttemptOAuth2 = true

// After (v3)
import (
    "github.com/oras-project/oras-go/v3/registry/remote/auth"
    "github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

client := &auth.Client{
    Client: http.DefaultClient,
    Header: http.Header{
        "User-Agent": {"my-app/1.0"},
    },
    CredentialFunc: credentials.StaticCredentialFunc("ghcr.io", credentials.Credential{
        Username: "user",
        Password: "token",
    }),
    Cache: auth.DefaultCache,
}
// OAuth2 is now the default, so no action needed
// If you need legacy mode: client.SetLegacyMode(true)
```

## Search and Replace Patterns

| Find | Replace |
|------|---------|
| `.Credential =` (on auth.Client) | `.CredentialFunc =` |
| `ForceAttemptOAuth2 = true` | Remove (OAuth2 is now default) |
| `ForceAttemptOAuth2 = false` | `SetLegacyMode(true)` |

## New Features in v3

### TokenFetcher Interface

v3 introduces a pluggable `TokenFetcher` interface for custom token acquisition strategies:

```go
// TokenFetcher abstracts token acquisition, allowing custom implementations
type TokenFetcher interface {
    FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error)
}

// Built-in implementations:
// - DistributionTokenFetcher: Distribution spec token endpoint (GET)
// - OAuth2TokenFetcher: OAuth2 password/refresh_token grants (POST)
// - CompositeTokenFetcher: Selects strategy based on credential type
```

To use a custom token fetcher:

```go
client := &auth.Client{
    CredentialFunc: myCredFunc,
    TokenFetcher:   myCustomFetcher,  // Optional: defaults to CompositeTokenFetcher
}
```

## Additional Notes

- The `SetUserAgent()` method remains unchanged
- The `Cache` field remains unchanged
- The `Client` field (underlying HTTP client) remains unchanged
- The `ClientID` field remains unchanged
