# ORAS Go v3 Registry Package Design

## Overview

This document describes the architecture of the `registry` and `registry/remote` packages in ORAS Go v3. It covers the package structure, key abstractions, data-flow diagrams, and the design rationale behind major subsystems.

---

## 1. Package Structure

```
registry/
├── interfaces.go         # Repository, BlobStore, ManifestStore, TagLister, etc.
├── reference.go          # Reference parsing and validation
│
└── remote/
    ├── registry.go       # Remote Registry (catalog, ping, repository lookup)
    ├── repository.go     # Remote Repository implementation (~64 KB)
    ├── builder.go        # ClientBuilder: factory for auth.Client + Repository
    ├── middleware.go      # RepositoryMiddleware, WithPolicyEnforcement, Compose
    ├── mirror.go         # Mirror fallback logic (PullFromMirrorAll/DigestOnly/TagOnly)
    ├── referrers.go      # Referrers API implementation
    ├── referrers_state.go # Atomic referrer capability state machine
    ├── auth.go           # Login/Logout helpers
    ├── warning.go        # RFC 7234 warning header handling
    ├── logging_transport.go # slog-based HTTP debug transport
    ├── url.go            # URL builder utilities
    │
    ├── auth/
    │   ├── client.go     # auth.Client: auth-decorated HTTP client
    │   ├── token.go      # TokenFetcher interface + Distribution/OAuth2/Composite impls
    │   ├── cache.go      # Token and auth-header caching
    │   ├── challenge.go  # WWW-Authenticate challenge parsing
    │   └── scope.go      # OAuth2 scope management
    │
    ├── credentials/
    │   ├── credential.go # Credential type (canonical location)
    │   ├── store.go      # Store interface + CredentialFunc
    │   ├── file_store.go # Docker config.json credential store
    │   ├── memory_store.go
    │   └── native_store.go # OS keychain integration
    │
    ├── properties/
    │   ├── registry.go   # Registry (Reference, Transport, Credential, Mirrors)
    │   ├── reference.go  # Reference (Registry, Repository, Tag, Digest)
    │   ├── transport.go  # Transport (TLS, PlainHTTP, HeaderFlags)
    │   ├── attributes.go # Attributes (ReferrersAPI capability hint)
    │   └── mirror.go     # Mirror (Location, Transport, PullFromMirror)
    │
    ├── config/
    │   ├── loader.go     # LoadConfigs / LoadConfigsWithOptions from system paths
    │   ├── config.go     # Docker config.json parser
    │   ├── registries.go # registries.conf / registries.d YAML parser
    │   ├── registriesd.go # registries.d directory support
    │   ├── certsd.go     # /etc/containers/certs.d certificate discovery
    │   └── properties.go # Configs → properties.Registry conversion
    │
    ├── policy/
    │   ├── policy.go     # Policy type + requirements (InsecureAcceptAnything, Reject, PRSignedBy)
    │   ├── evaluator.go  # Evaluator: IsImageAllowed
    │   ├── requirement.go # PolicyRequirement interface
    │   └── transport.go  # TransportName constants (docker, atomic, etc.)
    │
    ├── signature/
    │   ├── verifier.go   # DefaultSignedByVerifier: OpenPGP signature verification
    │   ├── storage.go    # SignatureStorage interface
    │   ├── lookaside.go  # LookasideStore: file:// and HTTP signature backends
    │   ├── simplesigning.go # atomic container signature payload format
    │   ├── openpgp.go    # CreateOpenPGPSignature helper
    │   └── identity.go   # matchRepoDigestOrExact identity matching
    │
    └── retry/
        ├── client.go     # Retry-decorated http.Client
        └── policy.go     # RetryPolicy interface + DefaultPolicy
```

---

## 2. Core Interfaces

### 2.1 Repository Interface Hierarchy

```mermaid
classDiagram
    class Repository {
        <<interface>>
        +Fetch(ctx, desc) ReadCloser
        +Push(ctx, desc, reader) error
        +Exists(ctx, desc) bool
        +Delete(ctx, desc) error
        +Resolve(ctx, ref) Descriptor
        +Tag(ctx, desc, ref) error
        +FetchReference(ctx, ref) Descriptor, ReadCloser
        +PushReference(ctx, desc, reader, ref) error
        +Referrers(ctx, desc, type, fn) error
        +Tags(ctx, last, fn) error
        +Blobs() BlobStore
        +Manifests() ManifestStore
    }

    class BlobStore {
        <<interface>>
        +Fetch(ctx, desc) ReadCloser
        +Push(ctx, desc, reader) error
        +Exists(ctx, desc) bool
        +Delete(ctx, desc) error
        +Resolve(ctx, ref) Descriptor
        +FetchReference(ctx, ref) Descriptor, ReadCloser
    }

    class ManifestStore {
        <<interface>>
        +Tag(ctx, desc, ref) error
        +PushReference(ctx, desc, reader, ref) error
    }

    class Mounter {
        <<interface>>
        +Mount(ctx, desc, fromRepo, getContent) error
    }

    Repository --> BlobStore : Blobs()
    Repository --> ManifestStore : Manifests()
    ManifestStore --|> BlobStore : extends
    Mounter ..|> BlobStore : optional type assertion
```

### 2.2 Content Package Interfaces (embedded)

`Repository` embeds several interfaces from the `content` package:

| Embedded Interface | Methods |
|---|---|
| `content.Storage` | `Fetch`, `Push`, `Exists` |
| `content.Deleter` | `Delete` |
| `content.TagResolver` | `Tag`, `Resolve` |
| `registry.ReferenceFetcher` | `FetchReference` |
| `registry.ReferencePusher` | `PushReference` |
| `registry.ReferrerLister` | `Referrers` |
| `registry.TagLister` | `Tags` |

---

## 3. Authentication Architecture

### 3.1 Component Relationships

```mermaid
graph TD
    subgraph credentials
        Credential["credentials.Credential\n(Username, Password,\nRefreshToken, AccessToken)"]
        CredentialFunc["credentials.CredentialFunc\nfunc(ctx, hostport) Credential"]
        Store["credentials.Store interface\nGet / Put / Delete"]
        FileStore["FileStore\n(Docker config.json)"]
        NativeStore["NativeStore\n(OS keychain)"]
        MemStore["MemoryStore"]
    end

    subgraph auth
        Client["auth.Client\n(auth-decorated http.Client)"]
        TokenFetcher["TokenFetcher interface\nFetchToken(ctx, params, cred)"]
        DistFetcher["DistributionTokenFetcher\n(GET /token)"]
        OAuthFetcher["OAuth2TokenFetcher\n(POST /token)"]
        Composite["CompositeTokenFetcher\n(selects strategy)"]
        Cache["auth.Cache\n(token + auth-header cache)"]
    end

    Store --> FileStore
    Store --> NativeStore
    Store --> MemStore
    Client --> CredentialFunc
    Client --> Cache
    Client --> TokenFetcher
    TokenFetcher --> DistFetcher
    TokenFetcher --> OAuthFetcher
    TokenFetcher --> Composite
    Composite --> DistFetcher
    Composite --> OAuthFetcher
    CredentialFunc --> Credential
```

### 3.2 Authentication Flow

```mermaid
sequenceDiagram
    participant App
    participant auth.Client
    participant Cache
    participant Registry
    participant TokenEndpoint

    App->>auth.Client: Do(request)
    auth.Client->>Registry: HTTP request (no auth)
    Registry-->>auth.Client: 401 WWW-Authenticate: Bearer realm=...
    auth.Client->>Cache: lookup token for scope
    alt cache hit
        Cache-->>auth.Client: cached token
    else cache miss
        auth.Client->>auth.Client: resolve credential (CredentialFunc)
        auth.Client->>TokenEndpoint: FetchToken (Distribution GET or OAuth2 POST)
        TokenEndpoint-->>auth.Client: access token
        auth.Client->>Cache: store token
    end
    auth.Client->>Registry: HTTP request + Authorization: Bearer <token>
    Registry-->>auth.Client: 200 OK
    auth.Client-->>App: response
```

### 3.3 TokenFetcher Strategy Selection

`CompositeTokenFetcher` selects the token acquisition strategy at runtime:

```mermaid
flowchart TD
    A[FetchToken called] --> B{cred.AccessToken != empty?}
    B -- yes --> C[Return AccessToken directly]
    B -- no --> D{cred == EmptyCredential\nor LegacyMode && no RefreshToken?}
    D -- yes --> E[DistributionTokenFetcher\nGET /token?service=...&scope=...]
    D -- no --> F[OAuth2TokenFetcher\nPOST /token grant_type=password\nor refresh_token]
```

---

## 4. ClientBuilder and Registry Construction

`ClientBuilder` is the recommended factory for creating auth-configured clients and repositories. It replaces ad-hoc `auth.DefaultClient` usage.

```mermaid
graph LR
    subgraph Input
        Props["properties.Registry\n(Reference, Transport,\nCredential, Mirrors)"]
        Builder["ClientBuilder\n(BaseTransport, RetryPolicy,\nCacheFactory, CredentialStore,\nTokenFetcher, PolicyEvaluator, Logger)"]
    end

    subgraph Output
        AuthClient["auth.Client"]
        Repo["remote.Repository"]
        Reg["remote.Registry"]
        Mirrors["[]mirrorRepository"]
    end

    Builder -- "Build(props)" --> AuthClient
    Props --> AuthClient
    AuthClient --> Repo
    AuthClient --> Reg
    Props --> Mirrors
    Builder --> Mirrors
    Mirrors --> Repo
```

**Usage pattern:**

```go
builder := remote.NewClientBuilder()
builder.CredentialStore = myStore
builder.PolicyEvaluator = evaluator
builder.Logger = slog.Default()

props, _ := properties.NewRegistry("registry.example.com/app/myimage")
repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

---

## 5. Mirror Fallback

When a repository has mirrors configured, read operations try mirrors in order before falling back to the primary.

```mermaid
flowchart TD
    A[Resolve / Fetch / FetchReference / Exists] --> B{mirrors configured?}
    B -- no --> Z[Primary Registry]
    B -- yes --> C[For each mirror in order]
    C --> D{mirror.shouldUseForReference?}
    D -- no --> C
    D -- yes --> E[Try mirror]
    E -- success --> F[Return result]
    E -- error: context.Canceled\nor DeadlineExceeded --> G[Return error immediately]
    E -- other error --> C
    C -- all mirrors exhausted --> Z[Primary Registry]
    Z --> H[Return result or error]
```

**Pull policies** (`PullFromMirror` field on `properties.Mirror`):

| Value | Behavior |
|---|---|
| `"all"` (default) | Use mirror for both tag and digest references |
| `"digest-only"` | Use mirror only for `@sha256:...` references |
| `"tag-only"` | Use mirror only for `:tag` references |

---

## 6. Middleware Pattern

`RepositoryMiddleware` is a function type that wraps a `registry.Repository`. Middlewares are composed with `Compose`.

```mermaid
graph LR
    App --> MW1["middleware 1\n(outermost)"]
    MW1 --> MW2["middleware 2"]
    MW2 --> MW3["middleware N\n(innermost)"]
    MW3 --> Repo["remote.Repository\n(HTTP calls)"]
```

**Built-in middleware:**

```go
// Apply policy enforcement to an existing repository
enforced := remote.WithPolicyEnforcement(evaluator, policy.TransportNameDocker, scope)(repo)

// Compose multiple middlewares
composed := remote.Compose(
    remote.WithPolicyEnforcement(evaluator, policy.TransportNameDocker, scope),
    myLoggingMiddleware,
)(repo)
```

`policyEnforcingRepository` wraps all read and write methods — `Fetch`, `Push`, `Resolve`, `Tag`, `FetchReference`, `PushReference` — as well as the sub-stores returned by `Blobs()` and `Manifests()`.

---

## 7. Policy and Signature Verification

### 7.1 Package Relationships

```mermaid
graph TD
    subgraph config
        Loader["config.LoadConfigsWithOptions\n(LoadConfigsOptions:\nPolicyConfigPath,\nRegistriesDPath,\nDockerConfigPath, ...)"]
        Configs["config.Configs\n{PolicyConfig, RegistriesDConfig,\nDockerConfig, ...}"]
        Loader --> Configs
    end

    subgraph policy
        Policy["policy.Policy\n(default + per-scope requirements)"]
        Evaluator["policy.Evaluator\nIsImageAllowed(ctx, ImageReference)"]
        PRSignedBy["PRSignedBy\n{KeyType, KeyPath}"]
        Reject["Reject"]
        Insecure["InsecureAcceptAnything"]
        Policy --> PRSignedBy
        Policy --> Reject
        Policy --> Insecure
        Configs -- "PolicyEvaluator(opts...)" --> Evaluator
    end

    subgraph signature
        Verifier["DefaultSignedByVerifier\nVerify(ctx, scope, digest, key)"]
        LookasideStore["LookasideStore\nPutSignature / GetSignatures\n(file:// or HTTP)"]
        SimpleSigning["SimpleSigningPayload\n{critical.image.docker-manifest-digest,\n critical.identity.docker-reference}"]
        Verifier --> LookasideStore
        Verifier --> SimpleSigning
    end

    Configs -- "NewSignedByVerifierFromConfig(cfg, scope)" --> Verifier
    Evaluator -- "WithSignedByVerifier(verifier)" --> Verifier
```

### 7.2 Signature Verification Flow

```mermaid
sequenceDiagram
    participant App
    participant Evaluator
    participant PRSignedBy
    participant DefaultSignedByVerifier
    participant LookasideStore
    participant OpenPGP

    App->>Evaluator: IsImageAllowed(ctx, ImageReference)
    Evaluator->>Evaluator: match scope → policy requirement
    Evaluator->>PRSignedBy: IsSatisfied(ctx, imageRef, verifier)
    PRSignedBy->>DefaultSignedByVerifier: Verify(ctx, scope, digest, keyPath)
    DefaultSignedByVerifier->>LookasideStore: GetSignatures(ctx, scope, digest)
    LookasideStore-->>DefaultSignedByVerifier: []sigData
    loop for each signature
        DefaultSignedByVerifier->>OpenPGP: verify signature against key
        DefaultSignedByVerifier->>DefaultSignedByVerifier: parse SimpleSigningPayload
        DefaultSignedByVerifier->>DefaultSignedByVerifier: matchRepoDigestOrExact(imageRef, signedRef)
    end
    DefaultSignedByVerifier-->>PRSignedBy: verified / not verified
    PRSignedBy-->>Evaluator: satisfied / not satisfied
    Evaluator-->>App: allowed bool, error
```

### 7.3 Config Loading

`config.LoadConfigsWithOptions` aggregates configuration from multiple sources:

| `LoadConfigsOptions` field | Source |
|---|---|
| `PolicyConfigPath` | `containers-policy.json` path (custom override) |
| `RegistriesDPath` | `registries.d` directory path (custom override) |
| `DockerConfigPath` | `config.json` path (custom override) |
| (defaults) | `/etc/containers/policy.json`, `~/.config/containers/registries.d`, `~/.docker/config.json` |

The returned `config.Configs` provides:
- `Configs.PolicyEvaluator(opts...)` — creates a `*policy.Evaluator`
- `config.NewSignedByVerifierFromConfig(cfg.RegistriesDConfig, scope)` — creates a `*signature.DefaultSignedByVerifier` via longest-prefix matching on the registries.d YAML keys

---

## 8. Package Dependency Graph

```mermaid
graph BT
    retry --> auth
    credentials --> auth
    credentials --> builder["remote (builder.go)"]
    auth --> builder
    properties --> builder
    policy --> builder
    retry --> builder

    builder --> registry_remote["remote.Repository\nremote.Registry"]

    config --> policy
    config --> signature
    signature --> policy

    registry_remote --> registry["registry\n(interfaces)"]
```

**Key principle:** dependencies flow upward. `credentials` and `retry` are foundational with no internal dependencies. `auth` depends on `credentials`. `properties` is standalone. `builder` composes them all. `config`, `policy`, and `signature` form the security layer above the transport.

---

## 9. Referrers API State Machine

The `remote.Repository` tracks whether the target registry supports the OCI Referrers API (introduced in Distribution Spec v1.1). This avoids re-probing on every call.

```mermaid
stateDiagram-v2
    [*] --> Unknown : initial state
    Unknown --> Supported : 200 from GET /referrers/
    Unknown --> Unsupported : 404 / fallback to tag schema
    Supported --> Supported : subsequent Referrers() calls
    Unsupported --> Unsupported : use tag schema (_oras.index.*)
    Supported --> Unknown : SetReferrersCapability(reset)
```

---

## 10. Breaking Changes from v2

| Area | v2 | v3 |
|---|---|---|
| `Credential` type location | `auth.Credential` | `credentials.Credential` (canonical) |
| `auth.Client.Credential` field | direct credential | `CredentialFunc credentials.CredentialFunc` |
| `ForceAttemptOAuth2` flag | `bool` field on `auth.Client` | removed; use `SetLegacyMode()` |
| Token fetching | embedded in `auth.Client` | extracted to `TokenFetcher` interface |
| Registry configuration | manual field-by-field setup | `properties.Registry` + `ClientBuilder` |
| Policy enforcement | not available | `policy` package + `WithPolicyEnforcement` middleware |
| Mirror support | not available | `properties.Mirror` + mirror fallback in `Repository` |
| Signature verification | not available | `signature` package + `LookasideStore` |
| Config loading | not available | `config.LoadConfigsWithOptions` |

---

## 11. Testing Strategy

### Unit Tests
- `TokenFetcher` implementations (`DistributionTokenFetcher`, `OAuth2TokenFetcher`) are testable in isolation via the `TokenFetcher` interface.
- `policyEnforcingRepository` is a pure wrapper — policy logic is testable without a live registry.
- `LookasideStore` supports `file://` URIs, enabling signature tests without an HTTP server.

### Functional Tests (`test/functional/`)
The functional test suite (`//go:build functional`) requires a live registry (default: `localhost:5000`):

| Test file | Coverage |
|---|---|
| `registry_test.go` | Ping, catalog, tag listing |
| `repository_test.go` | Push, pull, delete, resolve, referrers |
| `objects_functional_test.go` | `objects` package ORM API |
| `signature_test.go` | GPG sign/verify pipeline via `LookasideStore` |
| `config_test.go` | `LoadConfigsWithOptions` + full policy pipeline |

Run with:
```bash
cd test/functional
go test -tags functional -v ./...
```
