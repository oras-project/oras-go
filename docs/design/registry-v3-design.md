# ORAS Go v3 Registry Package Design Document

## Executive Summary

This document provides a critical analysis of the current registry package architecture and proposes a path forward for v3 that emphasizes separation of concerns, improved testability, and gradual adoption. The focus is on enabling non-breaking changes where possible, with clear deprecation paths for unavoidable breaking changes.

---

## 1. Current State Analysis

### 1.1 Package Structure Overview

```
registry/
├── registry.go           # Registry interface (minimal)
├── repository.go         # Repository interface + helpers (dense)
├── reference.go          # Reference parsing
├── reference_list.go     # Batch reference parsing
└── remote/
    ├── registry.go       # Remote Registry implementation
    ├── repository.go     # Remote Repository (1725 lines)
    ├── auth.go           # Login/Logout functions
    ├── referrers.go      # Referrers API
    ├── auth/
    │   ├── client.go     # Auth client (449 lines)
    │   ├── cache.go      # Token caching
    │   ├── challenge.go  # WWW-Authenticate parsing
    │   └── scope.go      # OAuth2 scope management
    ├── credentials/
    │   ├── credential.go # Credential type + functions
    │   ├── store.go      # Store interface
    │   └── ...           # Various store implementations
    ├── properties/       # NEW: Registry configuration
    └── internal/
        └── configuration/ # NEW: Policy/registries.conf support
```

### 1.2 Key Design Decisions in Current Branch

| Feature | Status | Breaking? |
|---------|--------|-----------|
| URI scheme stripping in Reference | Implemented | No (additive) |
| Tag/Digest fields in Reference | Implemented | No (deprecated Reference field) |
| ParseReferenceList | Implemented | No (additive) |
| Credential moved to credentials pkg | Implemented | Yes |
| auth.Client.CredentialFunc renamed | Implemented | Yes |
| ForceAttemptOAuth2 → SetLegacyMode | Implemented | Yes |
| properties package | Implemented | No (additive) |
| configuration package | Implemented | No (additive) |

---

## 2. Separation of Concerns: Critical Analysis

### 2.1 Problem: auth.Client is Overloaded

**Current Responsibilities (449 lines):**
1. HTTP request/response handling
2. Credential resolution
3. Challenge parsing (WWW-Authenticate)
4. Token caching integration
5. Bearer token fetching (OAuth2 + distribution spec)
6. Basic auth encoding
7. Request body rewinding
8. Header management

**Issues:**
- Hard to test individual behaviors
- Token fetching logic embedded in client
- Cache is optional but code paths diverge significantly
- Legacy vs OAuth2 mode handled via boolean flag

**Recommendation: Extract TokenFetcher Interface**

```go
// auth/token.go (NEW)
package auth

// TokenFetcher abstracts the token acquisition strategy.
type TokenFetcher interface {
    // FetchToken acquires an access token for the given parameters.
    FetchToken(ctx context.Context, params TokenParams) (string, error)
}

type TokenParams struct {
    Registry string
    Realm    string
    Service  string
    Scopes   []string
    Cred     credentials.Credential
}

// DistributionTokenFetcher implements the distribution spec token endpoint.
type DistributionTokenFetcher struct {
    Client *http.Client
    Header http.Header
}

// OAuth2TokenFetcher implements RFC 6749 password/refresh_token grants.
type OAuth2TokenFetcher struct {
    Client   *http.Client
    Header   http.Header
    ClientID string
}
```

**Migration Path (Non-Breaking):**
1. Add `TokenFetcher` interface and implementations
2. Add `Client.TokenFetcher` field (optional, defaults to current behavior)
3. Deprecate embedded logic over multiple minor versions
4. Remove in v4

---

### 2.2 Problem: Global DefaultClient Shared State

**Current State:**
```go
var DefaultClient = &Client{
    Client: retry.DefaultClient,
    Header: http.Header{headerUserAgent: {"oras-go"}},
    Cache:  DefaultCache,
}
```

**Issues:**
- All repositories share the same token cache by default
- Different registries may require different auth strategies
- Testing requires careful cache clearing
- Cannot easily have per-registry configuration

**Recommendation: Registry-Scoped Clients**

Instead of global defaults, use the new `properties.Registry` to build configured clients:

```go
// remote/client.go (NEW)
package remote

// ClientBuilder creates auth clients from registry properties.
type ClientBuilder struct {
    // BaseTransport is the underlying HTTP transport.
    BaseTransport http.RoundTripper

    // RetryPolicy configures retry behavior.
    RetryPolicy *retry.Policy

    // CacheFactory creates caches for each registry.
    // If nil, a per-registry cache is created.
    CacheFactory func(registry string) auth.Cache
}

// Build creates an auth.Client configured for the given registry.
func (b *ClientBuilder) Build(props *properties.Registry) *auth.Client {
    // ... configure transport, auth, cache per registry
}
```

**Migration Path (Non-Breaking):**
1. Introduce `ClientBuilder` in v3.0
2. Add `Repository.SetClient()` method (already exists as field)
3. Document best practices for per-registry configuration
4. `DefaultClient` remains for backward compatibility

---

### 2.3 Problem: Repository as Monolithic Union Type

**Current Definition:**
```go
type Repository interface {
    content.Storage        // Fetch, Push, Exists
    content.Deleter        // Delete
    content.TagResolver    // Resolve, Tag
    ReferenceFetcher       // FetchReference
    ReferencePusher        // PushReference
    ReferrerLister         // Referrers
    TagLister              // Tags

    Blobs() BlobStore
    Manifests() ManifestStore
}
```

**Issues:**
- 12+ methods in one interface
- Consumers must implement everything or embed
- Optional features (Mounter) require type assertions
- Hard to create read-only or write-only wrappers

**Recommendation: Compose Smaller Interfaces**

```go
// repository.go - Core read operations
type ReadableRepository interface {
    content.Fetcher         // Fetch
    content.Resolver        // Resolve
    ReferenceFetcher        // FetchReference
}

// repository.go - Core write operations
type WritableRepository interface {
    content.Pusher          // Push
    content.Tagger          // Tag
    ReferencePusher         // PushReference
}

// repository.go - Full repository (backward compatible)
type Repository interface {
    ReadableRepository
    WritableRepository
    content.Storage         // Exists (read), Delete (write)
    content.Deleter
    ReferrerLister
    TagLister

    Blobs() BlobStore
    Manifests() ManifestStore
}

// Optional interfaces remain as type assertions
type Mounter interface { ... }
type BlobMountable interface { ... }
```

**Migration Path (Non-Breaking):**
1. Add `ReadableRepository` and `WritableRepository` interfaces
2. Helper functions accept narrower interfaces where possible
3. Existing code continues to work with full `Repository`

---

### 2.4 Problem: Scattered Policy Enforcement

**Current State:**
Policy checks are scattered across repository.go:
- `Fetch()` calls `checkPolicy()`
- `Push()` calls `checkPolicy()`
- `Resolve()` calls `checkPolicy()`
- `Tag()` calls `checkPolicy()`
- `FetchReference()` calls `checkPolicy()`
- `PushReference()` calls `checkPolicy()`

**Issues:**
- Easy to miss adding policy check to new methods
- Cannot easily disable for testing
- Policy evaluation happens per-operation (potentially redundant)

**Recommendation: Middleware Pattern**

```go
// remote/middleware.go (NEW)
package remote

// RepositoryMiddleware wraps repository operations.
type RepositoryMiddleware func(Repository) Repository

// WithPolicyEnforcement returns middleware that enforces container policy.
func WithPolicyEnforcement(evaluator *configuration.PolicyEvaluator) RepositoryMiddleware {
    return func(repo Repository) Repository {
        return &policyEnforcingRepository{
            Repository: repo,
            evaluator:  evaluator,
        }
    }
}

// WithWarningHandler returns middleware that processes RFC 7234 warnings.
func WithWarningHandler(handler func(Warning)) RepositoryMiddleware {
    return func(repo Repository) Repository {
        return &warningHandlingRepository{
            Repository: repo,
            handler:    handler,
        }
    }
}
```

**Migration Path:**
1. Extract policy logic into middleware
2. Add `Repository.WithMiddleware()` or constructor option
3. Default behavior remains unchanged
4. Document how to customize/disable

---

### 2.5 Problem: Credential Type Location

**Current State (v3 branch):**
- `credentials.Credential` - the type definition
- `credentials.CredentialFunc` - the resolver function type
- `auth.Client.CredentialFunc` - uses credentials package type
- `properties.Registry.Credential` - uses `auth.Credential` (BUG: should be `credentials.Credential`)

**Issues:**
- Import cycle risk between auth and credentials
- `properties.Registry` references wrong type (`auth.Credential` undefined)
- Confusion about canonical location

**Recommendation: Single Source of Truth**

```go
// credentials/credential.go - CANONICAL location
package credentials

type Credential struct { ... }
type CredentialFunc func(ctx context.Context, hostport string) (Credential, error)

// auth/client.go - imports credentials
package auth
import "...credentials"
type Client struct {
    CredentialFunc credentials.CredentialFunc
}

// properties/registry.go - imports credentials (FIX NEEDED)
package properties
import "...credentials"
type Registry struct {
    Credential credentials.Credential  // NOT auth.Credential
}
```

**Immediate Fix Required:**
```diff
// properties/registry.go
-import "github.com/oras-project/oras-go/v3/registry/remote/auth"
+import "github.com/oras-project/oras-go/v3/registry/remote/credentials"

 type Registry struct {
-    Credential auth.Credential
+    Credential credentials.Credential
 }
```

---

### 2.6 Problem: Referrers State Machine Complexity

**Current State:**
The referrers implementation tracks API capability via atomic state:
- `referrerStateUnknown`
- `referrerStateSupported`
- `referrerStateUnsupported`

Plus complex merge pool logic for tag schema updates.

**Issues:**
- State transitions spread across multiple functions
- Concurrent access requires careful synchronization
- Hard to test state transitions in isolation

**Recommendation: Encapsulate State Machine**

```go
// remote/referrers_state.go (NEW)
package remote

// ReferrerCapability tracks a registry's Referrers API support.
type ReferrerCapability struct {
    mu    sync.RWMutex
    state referrerState
}

func (c *ReferrerCapability) IsSupported() bool { ... }
func (c *ReferrerCapability) MarkSupported() { ... }
func (c *ReferrerCapability) MarkUnsupported() { ... }
func (c *ReferrerCapability) Reset() { ... }

// ReferrerMergePool manages concurrent tag schema updates.
type ReferrerMergePool struct { ... }
```

**Migration Path (Internal Only):**
This is an internal refactoring with no public API changes.

---

## 3. Proposed Package Reorganization

### 3.1 Target Structure for v3

```
registry/
├── registry.go           # Registry interface
├── repository.go         # Repository interfaces (smaller, composable)
├── reference.go          # Reference parsing
├── reference_list.go     # Batch operations
│
└── remote/
    ├── registry.go       # Remote Registry
    ├── repository.go     # Remote Repository (refactored)
    ├── client.go         # NEW: ClientBuilder
    ├── middleware.go     # NEW: Repository middleware
    │
    ├── auth/
    │   ├── client.go     # Auth client (simplified)
    │   ├── cache.go      # Token caching
    │   ├── challenge.go  # Challenge parsing
    │   ├── scope.go      # Scope management
    │   └── token.go      # NEW: TokenFetcher interface
    │
    ├── credentials/
    │   ├── credential.go # CANONICAL Credential type
    │   ├── store.go      # Store interface
    │   ├── file_store.go
    │   ├── memory_store.go
    │   └── native_store.go
    │
    ├── properties/       # Registry configuration
    │   ├── registry.go
    │   ├── transport.go
    │   └── attributes.go
    │
    └── internal/
        ├── configuration/  # Policy, registries.conf
        ├── referrers/      # NEW: Referrers state machine
        └── errutil/
```

### 3.2 Dependency Direction

```
                    ┌─────────────────────┐
                    │      registry/      │
                    │  (interfaces only)  │
                    └──────────┬──────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
              ▼                ▼                ▼
     ┌────────────────┐ ┌────────────┐ ┌───────────────┐
     │ remote/auth/   │ │ remote/    │ │ remote/       │
     │ (auth client)  │ │credentials/│ │ properties/   │
     └───────┬────────┘ │ (stores)   │ │ (config)      │
             │          └──────┬─────┘ └───────────────┘
             │                 │
             └────────┬────────┘
                      │
                      ▼
            ┌─────────────────┐
            │ remote/         │
            │ repository.go   │
            │ (implementation)│
            └─────────────────┘
```

**Key Principle:** Lower packages never import higher packages. `credentials` is foundational; `auth` depends on `credentials`; `repository` composes both.

---

## 4. Breaking vs Non-Breaking Changes

### 4.1 Non-Breaking Additions (v3.0)

| Change | Impact |
|--------|--------|
| Add `TokenFetcher` interface | Extensibility |
| Add `ClientBuilder` | Better configuration |
| Add `ReadableRepository`/`WritableRepository` | Narrower function signatures |
| Add repository middleware support | Policy customization |
| Add `Credential.IsEmpty()` method | Convenience |

### 4.2 Deprecations (v3.0, Remove in v4)

| Deprecated | Replacement |
|------------|-------------|
| `Reference.Reference` field | `Reference.Tag` + `Reference.Digest` |
| Direct `DefaultClient` modification | `ClientBuilder` |
| `auth.Client` embedded token logic | `TokenFetcher` interface |

### 4.3 Breaking Changes (v3.0)

| Change | Migration |
|--------|-----------|
| `Credential` canonical location | Update imports from `auth` to `credentials` |
| `auth.Client.Credential` → `CredentialFunc` | Rename field usage |
| `ForceAttemptOAuth2` removed | Use `SetLegacyMode()` |

---

## 5. Immediate Fixes Required

The following issues exist in the current branch and need resolution:

### 5.1 Compiler Errors

```
registry.go:33:18: undefined: auth.Credential
```
**Fix:** Change `properties/registry.go` to import `credentials.Credential`

```
reference_list.go:46:5: non-boolean condition in if statement
```
**Fix:** Review conditional logic in `ParseReferenceList`

```
memory.go, memory_test.go: unused imports
```
**Fix:** Remove unused `descriptor` imports

### 5.2 Type Consistency

The `properties.Registry.Credential` field references `auth.Credential` which doesn't exist. This should be `credentials.Credential`.

---

## 6. Implementation Priorities

### Phase 1: Fix Compilation (Immediate)
1. Fix `properties/registry.go` import
2. Fix `reference_list.go` conditional
3. Remove unused imports

### Phase 2: Stabilize v3 API (Before Release)
1. Finalize `Credential` location in `credentials` package
2. Document breaking changes in migration guide
3. Add deprecation notices

### Phase 3: Separation Improvements (v3.x)
1. Extract `TokenFetcher` interface
2. Add `ClientBuilder`
3. Add repository middleware support

### Phase 4: Interface Refinement (v3.x/v4)
1. Add `ReadableRepository`/`WritableRepository`
2. Encapsulate referrers state machine
3. Remove deprecated APIs

---

## 7. Testing Strategy

### 7.1 Unit Test Improvements

With better separation:
- `TokenFetcher` implementations testable in isolation
- Policy middleware testable without full repository
- Cache behavior testable independently

### 7.2 Integration Test Patterns

```go
// Example: Testing with custom token fetcher
func TestCustomTokenFetcher(t *testing.T) {
    fetcher := &MockTokenFetcher{
        Token: "test-token",
    }
    client := &auth.Client{
        TokenFetcher: fetcher,
    }
    // ... test auth flow
}

// Example: Testing without policy
func TestRepositoryWithoutPolicy(t *testing.T) {
    repo := remote.NewRepository(ref)
    // No policy middleware = no policy checks
    // ... test repository operations
}
```

---

## 8. Conclusion

The v3 registry package is on a good trajectory with several valuable additions:
- URI scheme flexibility
- Improved reference parsing
- Registry properties abstraction
- Policy/configuration support

The primary areas for improvement are:
1. **Credential type consolidation** - Ensure single canonical location
2. **Auth client decomposition** - Extract token fetching for testability
3. **Repository middleware** - Enable policy customization
4. **Interface composition** - Allow narrower function signatures

By introducing these changes incrementally with deprecation notices, we can improve separation of concerns while minimizing disruption to existing users.

---

## Appendix A: Current Diagnostic Issues

```
reference_list.go:46:5    non-boolean condition in if statement
memory_test.go:29:2       unused import "descriptor"
registry.go:33:18         undefined: auth.Credential
registry_test.go:65:28    undefined: auth.EmptyCredential
registry_test.go:127:20   undefined: auth.Credential
memory.go:28:2            unused import "descriptor"
```

## Appendix B: Related PRs

- PR #1095: fix: graph.Memory should use digest as map key
- PR #1091: Add URI Scheme Stripping Support
- PR #1087: refactor: add registries properties
- PR #1086: feat: add cache support
- PR #1045: Add ParseReferenceList method
- PR #1042: Add support for tag and digest in Reference
- PR #1038: Remove ForceAttemptOAuth2, add SetLegacyMode
- PR #1013: Add support for policy.json allow/deny
