# ORAS Go Library — Usage Scenarios

This document describes the primary scenarios where oras-go is used and how the library's features map to each scenario. It is targeted for contributors, integrators, and anyone evaluating oras-go for their project.

---

## 1. Full Configuration Stack

Loading the full container ecosystem configuration provides credentials, TLS certificates, registry mirrors, and more for interacting with remote registries.

### Capabilities Used

- **`config.LoadConfigs`** — Unified loader for Docker config.json, containers auth.json, registries.conf, registries.d, and certs.d.
- **`oras.Copy`** — Copy artifacts between registries, or from a registry to local OCI layout.
- **`oras.PackManifest`** — Build OCI image manifests (v1.0 or v1.1) from local files before pushing.
- **`oras.Tag` / `oras.TagN`** — Apply one or more tags to a manifest already present in a registry.
- **`oras.Fetch` / `oras.FetchBytes`** — Pull content by reference, optionally selecting a specific platform.
- **`remote.Repository`** — Low-level access to a single repository (resolve, push, fetch, delete, list tags/referrers).
- **`remote.Registry`** — Enumerate repositories within a registry.
- **TLS configuration via certs.d** — Per-registry TLS certificates without requiring manual `--ca-file` flags.
- **Registry mirrors via registries.conf** — Automatic mirror resolution for enterprise and air-gapped environments.

### Typical Flow

```go
// 1. Load all container ecosystem configs (credentials, TLS, mirrors, etc.).
configs, _ := config.LoadConfigs()

// 2. Get registry properties (resolves aliases, rewrites, TLS from certs.d).
props, _ := configs.RegistryProperties("registry.example.com/myapp")

// 3. Build a configured client with credentials.
//    Resolution order: OS credential helpers → Docker config.json → containers auth.json
builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})

// 4. Create repository with full config-driven settings.
repo, _ := remote.NewRepositoryWithProperties(props, builder)

// 5. Pack local files into an OCI manifest.
fs, _ := file.New("/tmp/workspace")
defer fs.Close()
// layerDescriptors are the []ocispec.Descriptor returned by fs.Add() for each file.
desc, _ := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, "application/vnd.myapp.config.v1", oras.PackManifestOptions{
    Layers: layerDescriptors,
})

// 6. Push to registry.
_, _ = oras.Copy(ctx, fs, desc.Digest.String(), repo, "latest", oras.DefaultCopyOptions)
```

Not all use cases require the full configuration stack. The remaining scenarios below demonstrate using individual features when simpler setups suffice.

### Benefits of Loading Full Configs

Loading the full configuration stack provides significant benefits:

- **Broader credential coverage** — Reads both Docker `config.json` and Containers Tools `auth.json`, so credentials stored by either Docker or Podman are found automatically.
- **Per-registry TLS** — Utilizes custom CA certificates and client certs from `certs.d` without requiring CLI flags.
- **Mirror support** — Respects registry mirrors configured in `registries.conf`, which is essential for enterprise and air-gapped environments.
- **Ecosystem consistency** — Users configure these files once and expect all registry-interacting tools to respect them.

### Configuration Loading Options

There are three ways to build a `Configs`, each offering a different level of control.

**`LoadConfigs`** searches all default locations automatically. It reads Docker
`config.json`, containers `auth.json`, `registries.conf`, `policy.json`,
`registries.d`, and `certs.d` from their standard system and user paths.
Files that do not exist are silently skipped — the call succeeds even when
none of the files are present.

```go
configs, _ := config.LoadConfigs()
```

Note that the accessors are stricter than the loader: while `LoadConfigs`
succeeds with no files present, `Configs.CredentialStore` returns an error
when neither Docker `config.json` nor containers `auth.json` was loaded.
The examples below elide that error for brevity, but production callers
should check it rather than silently ending up with a nil store.

**`LoadConfigsWithOptions`** lets you override specific paths. Any path you
set is used instead of the default for that config type. However, fields left
empty still trigger the default search — for example, omitting
`DockerConfigPath` still checks `$DOCKER_CONFIG` and `~/.docker/config.json`.
Missing files (whether default or overridden) are silently skipped.

```go
configs, _ := config.LoadConfigsWithOptions(config.LoadConfigsOptions{
    DockerConfigPath:     "/opt/myapp/docker-config.json",
    RegistriesConfigPath: "/opt/myapp/registries.conf",
    PolicyConfigPath:     "/opt/myapp/policy.json",
    CertsDirPaths:        []string{"/opt/myapp/certs.d"},
})
```

**Direct construction** gives full control. No default paths are searched
and no files are read — the struct contains only what you explicitly provide.
This is useful when you want a subset of configs or are loading them from
non-file sources.

```go
pol, _ := policy.LoadPolicy("/opt/myapp/policy.json")
dockerCfg, _ := config.Load("/opt/myapp/docker-config.json")

configs := &config.Configs{
    DockerConfig: dockerCfg,
    PolicyConfig: pol,
}
```

### Path Resolution Strategies

`LoadConfigsOptions.Strategy` selects *how* the default locations are searched
when a path is not overridden. It does not change which files are read, only
where they are looked for and how multiple copies are combined.

- **`config.StrategyContainersImage`** (default, zero value) — the current
  containers/image layout: two tiers (system + user) with merge-all semantics,
  so a user-level `registries.conf.d` drop-in adds to the system config rather
  than replacing it.
- **`config.StrategyUAPI`** — the Podman 6 layout based on the UAPI
  configuration files specification: three tiers (vendor + system + user),
  first-found-wins for main config files, and rootful/rootless drop-in
  directories.

```go
configs, _ := config.LoadConfigsWithOptions(config.LoadConfigsOptions{
    Strategy: config.StrategyUAPI,
})
```

`StrategyUAPI` is **experimental**. It tracks a draft upstream specification
and its behaviour may change; pin to `StrategyContainersImage` (or simply omit
the field) if you need stability. The same choice is available to the
individual loaders through `LoadSystemRegistriesConfigWithStrategy` and
`LoadSystemRegistriesDConfigWithStrategy`.

Reference: <https://uapi-group.org/specifications/specs/configuration_files_specification/>

---

## 2. CLI Tool with Flag Overrides

CLI tools typically load the full configuration stack and then override specific settings from command-line flags. The library's layered credential resolution and mutable property fields make this straightforward.

### Capabilities Used

- **`config.LoadConfigs`** — Load all container ecosystem configs as a baseline.
- **`properties.Registry`** — Mutable struct whose transport, credential, and attribute fields can be overridden after creation.
- **`credentials.Credential`** — Direct credential that takes priority over the credential store when set on properties.
- **`remote.ClientBuilder.CredentialStore`** — Credential store acts as a fallback when no explicit credential is set on properties.

### Typical Flow

```go
// CLI flag declarations (typically at package level or in a setup function).
plainHTTP := flag.Bool("plain-http", false, "Allow plain HTTP connections")
insecure  := flag.Bool("insecure", false, "Skip TLS verification")
caFile    := flag.String("ca-file", "", "Path to CA certificate file")
username  := flag.String("username", "", "Registry username")
password  := flag.String("password", "", "Registry password")
flag.Parse()

// 1. Load all configs from default locations as a baseline.
configs, _ := config.LoadConfigs()

// 2. Get config-driven properties for the target reference.
ref := "registry.example.com/myapp:v1.0"
props, _ := configs.RegistryProperties(ref)

// 3. Override transport settings from CLI flags.
if *plainHTTP {
    props.Transport.PlainHTTP = true
}
if *insecure {
    props.Transport.Insecure = true
}
if *caFile != "" {
    props.Transport.CACerts = append(props.Transport.CACerts, *caFile)
}

// 4. Override credentials from CLI flags.
//    When set, props.Credential takes priority over the credential store.
if *username != "" {
    props.Credential = credentials.Credential{
        Username: *username,
        Password: *password,
    }
}

// 5. Build client — config-file credentials act as automatic fallback.
builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})

// 6. Create repository and operate.
repo, _ := remote.NewRepositoryWithProperties(props, builder)
_, _ = oras.Copy(ctx, repo, ref, localStore, "", oras.DefaultCopyOptions)
```

### Credential Resolution Order

The `ClientBuilder` resolves credentials in this order:

1. **`props.Credential`** (highest priority) — Explicit credential from CLI flags like `--username`/`--password`.
2. **`builder.CredentialStore`** (fallback) — Credentials from Docker config.json, containers auth.json, or OS credential helpers.
3. **Empty credential** — No authentication if neither source provides credentials.

This means CLI flags always win when provided, and config-file credentials are used automatically otherwise.

---

## 3. Policy Enforcement

Policy evaluation and signature verification can be added to the configuration-driven workflow to enforce trust decisions before pulling images.

### Capabilities Used

- **`config.LoadConfigs`** — Unified loader for Docker config.json, containers auth.json, registries.conf, policy.json, registries.d, and certs.d.
- **`config.RegistriesConfig`** — Registry mirrors, blocked registries, unqualified search registries, and prefix-based rewriting.
- **`policy.Policy` / `policy.Evaluator`** — containers-policy.json evaluation (accept, reject, signedBy, sigstoreSigned).
- **`signature.NewSignedByVerifier`** — OpenPGP signature verification via lookaside storage.
- **`signature.LookasideStore`** — Fetch/store simple signing signatures from file:// or https:// lookaside locations configured in registries.d.
- **TLS configuration via certs.d** — Per-registry TLS certificates.

### Typical Flow

```go
// 1. Load all container ecosystem configs at once.
configs, _ := config.LoadConfigs()

// 2. Build a configured client with credentials and policy enforcement.
ref := "docker.io/library/nginx:latest"
builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})
// scope identifies the registry/repository being accessed (e.g. "registry.example.com/app").
// It is used to select the matching policy rule and signature lookaside configuration.
builder.PolicyEvaluator, _ = configs.PolicyEvaluator(
    policy.WithSignedByVerifier(signature.NewSignedByVerifierFromConfig(configs.RegistriesDConfig, scope)),
)

// 3. Set up repository — policy is enforced automatically on all operations.
props, _ := configs.RegistryProperties(ref)
repo, _ := remote.NewRepositoryWithProperties(props, builder)

// 4. Pull the image (policy checked automatically before fetch).
_, _ = oras.Copy(ctx, repo, ref, localStore, "", oras.DefaultCopyOptions)
```

---

## 4. Artifact Packing and Distribution

OCI artifacts such as binaries, SBOMs, Helm charts, and WASM modules can be packed into manifests and pushed to registries.

### Capabilities Used

- **`oras.PackManifest`** with `PackManifestVersion1_1` — Attach custom artifact types and annotations.
- **`oras.Copy`** with `CopyOptions.MapRoot` — Transform manifests during promotion (e.g., platform selection).
- **`oras.TagN`** — Tag a single artifact with multiple versions simultaneously (e.g., `v1.2.3`, `v1.2`, `v1`, `latest`).
- **`oras.TagBytes` / `oras.TagBytesN`** — Push raw bytes as an artifact and tag it in one call (shorthand for `PushBytes` + `TagN`).
- **`content/memory`** — Stage artifacts in-memory before pushing to avoid disk I/O.
- **Cross-repository blob mounting** — Efficient promotion between repositories using `MountFrom` in copy hooks.

### Typical Flow

```go
// Stage in memory, then push.
memStore := memory.New()
desc, _ := oras.PackManifest(ctx, memStore, oras.PackManifestVersion1_1,
    "application/vnd.example.sbom.v1",
    oras.PackManifestOptions{
        ManifestAnnotations: map[string]string{
            "org.opencontainers.image.created": time.Now().Format(time.RFC3339),
        },
        // sbomLayers is a []ocispec.Descriptor of blobs already pushed to memStore,
        // e.g. the SPDX or CycloneDX document content.
        Layers: sbomLayers,
    },
)

// Push to registry with multiple tags.
repo, _ := remote.NewRepository("registry.example.com/builds/sbom")
_, _ = oras.Copy(ctx, memStore, desc.Digest.String(), repo, "v1.2.3", oras.DefaultCopyOptions)
oras.TagN(ctx, repo, desc.Digest.String(), []string{"v1.2", "v1", "latest"}, oras.DefaultTagNOptions)

// Shorthand: push raw bytes and tag atomically.
configData := []byte(`{"key":"value"}`)
_, _ = oras.TagBytes(ctx, repo, "application/vnd.example.config.v1+json", configData, "config-v1")
```

---

## 5. Object-Oriented Artifacts (Experimental)

The `objects` package provides a higher-level, type-safe API for building and navigating OCI artifacts. It uses fluent builders and typed models instead of raw descriptors, and handles blob pushing and manifest construction in a single step.

### Capabilities Used

- **`objects.Client`** — Entry point wrapping any ORAS storage implementation with caching and lazy loading.
- **`objects.BuildArtifact`** — Fluent builder for OCI artifact manifests.
- **`objects.BuildImage`** — Fluent builder for container images with config, layers, and platform.
- **`objects.BuildIndex`** — Fluent builder for multi-platform manifest indexes.
- **`objects.FetchByReference`** — Fetch and navigate typed models (Artifact, Image, Index, Blob).

### Typical Flow

```go
// Create an objects client wrapping any ORAS store.
client := objects.NewClient(store)

// Build and push an artifact in one step — no separate memory store or Copy needed.
// payload is the raw []byte content of the artifact layer (e.g. a binary or document).
artifact, _ := client.BuildArtifact("application/vnd.example.sbom.v1").
    AddBlob(client.NewBlob("application/json", configData)).
    AddBlob(client.NewBlob("application/octet-stream", payload)).
    WithAnnotation("org.opencontainers.image.created", time.Now().Format(time.RFC3339)).
    BuildAndPush(ctx, "registry.example.com/builds/sbom:v1.2.3")

// Fetch and navigate relationships.
manifest, _ := client.FetchByReference(ctx, "registry.example.com/myimage:latest")
image := manifest.(*models.Image)
layers, _ := image.Layers(ctx)
config, _ := image.Config(ctx)
```

### Multi-Platform Images

Build individual per-architecture images, then combine them into a manifest index. Pulling the index reference lets the runtime automatically select the correct platform variant.

```go
amd64Image, _ := client.BuildImage().
    WithConfig(amd64Config).
    AddLayer(amd64Layer).
    WithPlatform(&ocispec.Platform{Architecture: "amd64", OS: "linux"}).
    Build(ctx)

arm64Image, _ := client.BuildImage().
    WithConfig(arm64Config).
    AddLayer(arm64Layer).
    WithPlatform(&ocispec.Platform{Architecture: "arm64", OS: "linux"}).
    Build(ctx)

index, _ := client.BuildIndex().
    AddManifest(amd64Image).
    AddManifest(arm64Image).
    BuildAndPush(ctx, "registry.example.com/myimage:latest")
```

### Comparison with Core APIs

The `objects` package sits on top of the core ORAS APIs. Use the core APIs (`PackManifest` + `Copy`) when you need fine-grained control over the copy graph, hooks, or cross-repository blob mounting. Use the `objects` package when you want a simpler, more declarative interface for building and navigating artifacts.

---

## 6. Registry Mirroring and Replication

Registries can be mirrored for air-gapped environments, caching, or compliance.

### Capabilities Used

- **`oras.Copy` / `oras.CopyGraph`** — Deep copy of artifacts including all referenced blobs and manifests.
- **`oras.ExtendedCopy`** — Copy an artifact and all of its referrers (signatures, SBOMs, attestations) in a single call.
- **`CopyGraphOptions.PreCopy` / `PostCopy`** — Hook into copy operations for progress reporting, logging, or custom validation.
- **`CopyGraphOptions.MountFrom`** — Cross-mount blobs instead of re-uploading when source and destination are on the same registry.
- **`remote.Registry.Repositories`** — Enumerate all repositories in a source registry.

### Typical Flow

```go
srcRepo, _ := remote.NewRepository("public.ecr.aws/library/nginx")
dstRepo, _ := remote.NewRepository("internal.corp.com/mirror/nginx")

opts := oras.CopyOptions{
    CopyGraphOptions: oras.CopyGraphOptions{
        PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
            log.Printf("Copying %s (%d bytes)", desc.Digest, desc.Size)
            return nil
        },
    },
}
desc, _ := oras.Copy(ctx, srcRepo, "latest", dstRepo, "latest", opts)
```

### Copying Referrers

`oras.ExtendedCopy` copies an artifact and all of its referrers (signatures, SBOMs,
attestations) in one call. Use it when mirroring images that carry attached artifacts:

```go
// Copy nginx:latest and every referrer attached to it (e.g. Cosign signatures, SBOMs).
_, _ = oras.ExtendedCopy(ctx, srcRepo, "latest", dstRepo, "latest", oras.DefaultExtendedCopyOptions)
```

`ExtendedCopyOptions` also accepts a `FindPredecessors` function so you can filter which
referrers are copied (e.g. only signatures, or only a specific artifact type).

---

## 7. OCI Layout and Local Storage

OCI artifacts can be stored and manipulated offline using local storage backends.

### Capabilities Used

- **`content/oci.Store`** — Read and write OCI image layouts on disk.
- **`content/file.Store`** — Map files on disk to OCI blob layers for packing/unpacking.
- **`content/memory.Store`** — Ephemeral in-memory storage for testing or transient operations.
- **`oras.Copy`** — Transfer between any combination of local and remote stores.

### Typical Flow

```go
// Export from registry to OCI layout on disk.
ociStore, _ := oci.New("/var/lib/images/nginx")
defer ociStore.Close()

repo, _ := remote.NewRepository("docker.io/library/nginx")
_, _ = oras.Copy(ctx, repo, "latest", ociStore, "latest", oras.DefaultCopyOptions)

// Later: import from OCI layout to a different registry.
dstRepo, _ := remote.NewRepository("internal.corp.com/images/nginx")
_, _ = oras.Copy(ctx, ociStore, "latest", dstRepo, "latest", oras.DefaultCopyOptions)
```

### Use Cases

- Air-gapped deployments: Export on connected machine. Transfer media. Import on isolated machine.
- Local testing and development without a running registry.
- Build caches stored as OCI layouts.

---

## 8. Transparent Content Caching

Content fetched from remote registries can be cached locally to avoid redundant downloads. The `content/cache` package wraps any `ReadOnlyTarget` with a cache layer backed by any `content.Storage` (OCI layout, memory, etc.).

### Capabilities Used

- **`cache.CacheReadOnlyTarget`** — Wraps a `ReadOnlyTarget` with a cache: checks local cache before fetching from source, and caches content while reading.
- **`cache.Cache` / `cache.NewFromEnv`** — Helper that reads the `ORAS_CACHE` environment variable and creates a file-backed cached target using an OCI storage backend.
- **`content/oci.NewStorage`** — Process-safe OCI storage used as the cache backing store (unlike `oci.New`, it omits `index.json` writes so concurrent processes do not corrupt each other).

### Typical Flow

```go
// Option 1: environment variable-driven (mirrors ORAS CLI behaviour).
// Returns nil if ORAS_CACHE is unset, so callers can skip wrapping.
c := cache.NewFromEnv()
if c != nil {
    repo, err = c.ReadOnlyTarget(repo)
}

// Option 2: explicit cache directory.
c := &cache.Cache{Root: "/var/cache/oras"}
cachedRepo, err := c.ReadOnlyTarget(repo)
if err != nil {
    log.Fatal(err)
}

// Option 3: bring your own storage (e.g. in-memory for tests).
memCache := memory.New()
cachedRepo := cache.CacheReadOnlyTarget(repo, memCache)

// Use cachedRepo like any ReadOnlyTarget.
desc, rc, err := cachedRepo.(registry.ReferenceFetcher).FetchReference(ctx, "latest")
```

### How Caching Works

- **`Fetch`** — Checks the cache first. On a miss, streams content from the source and writes it to the cache while the caller reads. Subsequent fetches of the same digest are served entirely from cache.
- **`FetchReference`** — Resolves the reference to a descriptor via a lightweight HEAD request, then checks the cache. On a cache hit, no content body is downloaded from the source. On a miss, fetches from source and caches while reading.
- **`Exists`** — Returns `true` if content is present in either cache or source.

### When to Use `oci.NewStorage` vs `oci.New`

Use `oci.NewStorage` (not `oci.New`) when the cache directory may be accessed by multiple processes concurrently. `oci.New` maintains an `index.json` file that is not safe for concurrent writes; `oci.NewStorage` omits it, making it safe for shared use as a content-addressed cache.

### Limitations

- The cache wraps `ReadOnlyTarget` only — push and tag operations always go directly to the source.
- If the source implements `registry.ReferenceFetcher`, the cached target also exposes `FetchReference` with caching. Other optional interfaces are not promoted.

---

## 9. Credential Management

Registry credentials can be managed across Docker, Podman, and native platform keystores.

### Capabilities Used

- **`credentials.NewStoreFromDocker`** — Detects and uses Docker's credential helpers (docker-credential-osxkeychain, docker-credential-secretservice, etc.).
- **`credentials.NewFileStore`** — Direct file-based credential storage.
- **`credentials.Store` interface** — Pluggable credential backends with `Get`, `Put`, `Delete`.
- **`config.LoadConfigs`** — Load Docker config.json and containers auth.json simultaneously, with hierarchical namespace matching for Podman-style auth.
- **`auth.Client`** — HTTP client with automatic credential resolution, OAuth2 token exchange, and scope-based auth.

### Typical Flow

```go
// Create a credential store that checks multiple sources.
dockerStore, _ := credentials.NewStoreFromDocker(credentials.StoreOptions{})
fileStore, _ := credentials.NewFileStore("/custom/auth.json")
fallback := credentials.NewStoreWithFallbacks(dockerStore, fileStore)

client := &auth.Client{
    CredentialFunc: remote.NewCredentialFunc(fallback),
}

repo, _ := remote.NewRepository("ghcr.io/org/repo")
repo.Registry.Client = client
```

---

## 10. Image Signature Verification

Image provenance and integrity can be enforced before pulling or running images.

### Capabilities Used

- **`policy.Policy`** — Load and evaluate containers-policy.json with requirement types: `insecureAcceptAnything`, `reject`, `signedBy`, `sigstoreSigned`.
- **`policy.Evaluator`** — Apply policy rules to image references.
- **`signature.SimpleSigningPayload`** — Parse and validate "atomic container signature" payloads.
- **`signature.VerifyOpenPGPSignature`** — Verify OpenPGP (GPG) detached signatures.
- **`signature.MatchSignedIdentity`** — Apply identity matching rules (exact, repository, remap, etc.).
- **`signature.LookasideStore`** — Fetch signatures from lookaside servers or local directories.

### Typical Flow

```go
// Load policy and registries.d config.
configs, _ := config.LoadConfigs()

// Create evaluator with signature verification from registries.d config.
evaluator, _ := configs.PolicyEvaluator(
    policy.WithSignedByVerifier(signature.NewSignedByVerifierFromConfig(configs.RegistriesDConfig, scope)),
)

// Check policy before allowing the image.
allowed, _ := evaluator.IsImageAllowed(ctx, policy.ImageReference{
    Transport: "docker",
    Scope:     "registry.example.com/app",
    Reference: "registry.example.com/app:v1.0@sha256:abc...",
})
if !allowed {
    log.Fatal("image rejected by policy")
}
```

---

## 11. Library Integration and Middleware

oras-go can be wrapped with middleware to add cross-cutting concerns.

### Capabilities Used

- **`remote.RepositoryMiddleware`** — Wrap repositories with additional behavior (metrics, policy, tracing).
- **`remote.Compose`** — Chain multiple middlewares together.
- **`remote.WithPolicyEnforcement`** — Built-in middleware for applying container policy checks.
- **`Registry.HandleWarning`** — Callback invoked for each RFC 7234 `Warning` header returned by the registry.
- **`remote.NewWarningLogger`** — Creates a `HandleWarning` callback that logs each unique warning from a given registry exactly once at `slog.LevelWarn`, suppressing duplicates.
- **`CopyOptions.PolicyCheck`** — Callback hook for policy enforcement in the copy path.
- **`CopyGraphOptions.PreCopy` / `PostCopy` / `OnCopySkipped`** — Hooks for custom logic during graph traversal.

### Typical Flow

```go
baseRepo, _ := remote.NewRepository("registry.example.com/app")

// Handle registry deprecation warnings. Configure the concrete *remote.Repository
// before composing — middlewares return the registry.Repository interface, which
// does not expose the Registry field.
// NewWarningLogger deduplicates: each unique warning text is logged only once.
baseRepo.Registry.HandleWarning = remote.NewWarningLogger(
    baseRepo.Registry.Reference.Registry, slog.Default())

// Compose middlewares for a production repository client.
middleware := remote.Compose(
    remote.WithPolicyEnforcement(evaluator, "docker", scope),
    myMetricsMiddleware(),
)

// repo is a registry.Repository — the composed view used by callers.
repo := middleware(baseRepo)
```

For manual warning handling without deduplication:

```go
baseRepo.Registry.HandleWarning = func(w remote.Warning) {
    log.Printf("Registry warning: %s", w.Text)
}
```

---

## 12. Retry Transport

The `retry` package provides an `http.RoundTripper` that automatically retries failed requests with exponential backoff. It is a thin wrapper around any inner transport and is safe to compose with other transports.

### Capabilities Used

- **`retry.NewTransport`** — Wraps an `http.RoundTripper` with the default retry policy (retries on 408, 429, any 5xx, and network timeouts).
- **`retry.NewClient`** — Convenience function returning an `*http.Client` with the retry transport already wired.
- **`retry.Policy`** — Interface for custom retry/backoff logic; replace `Transport.Policy` to override defaults.

### Typical Flow

The recommended way to use retry with full TLS support (certs.d, insecure flag) is through
`ClientBuilder`, which builds the `retry.Transport` internally and wires TLS automatically:

```go
configs, _ := config.LoadConfigs()
props, _ := configs.RegistryProperties("registry.example.com/app")

builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})
// ClientBuilder.Build() internally calls buildTLSConfig() (applies props.Transport.CACerts
// from certs.d and props.Transport.Insecure) then wraps the result in retry.Transport.
repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

When you need retry without the full config stack, wire it manually. Note that in this case
TLS configuration (CA certificates, insecure skip verify) must be set up by the caller — it
is not picked up from certs.d or `registries.conf` automatically:

```go
// Minimal retry with no custom TLS (uses http.DefaultTransport).
repo, _ := remote.NewRepository("registry.example.com/app")
repo.Registry.Client = retry.NewClient()

// Custom retry policy.
repo.Registry.Client = &auth.Client{
    Client: &http.Client{
        Transport: &retry.Transport{
            Base: http.DefaultTransport,
            Policy: func() retry.Policy { return myPolicy },
        },
    },
    CredentialFunc: remote.NewCredentialFunc(store),
}
```

### Default Retry Behaviour

The default policy (`retry.DefaultPolicy`) retries up to 5 times with exponential
backoff — base 250 ms, factor 2, 10 % jitter — clamped to the `[MinWait, MaxWait]`
range of 200 ms to 3 s. It retries on:
- HTTP 429 Too Many Requests (respects `Retry-After` header, which overrides the backoff)
- HTTP 408 Request Timeout
- Any HTTP status ≥ 500
- Network timeouts — errors implementing `net.Error` whose `Timeout()` reports true

Note that non-timeout network failures such as connection refused and unexpected EOF
are *not* retried by `DefaultPredicate`; supply a custom `retry.Policy` if you need
those covered.

Requests with bodies are only retried when `Request.GetBody` is set, so the body can be rewound.

---

## 13. Debug Logging Transport

The `LoggingTransport` wraps any `http.RoundTripper` and logs every HTTP request and response at `slog.LevelDebug` using the standard library `log/slog` package — no additional dependencies required.

Because oras-go performs concurrent HTTP requests (parallel blob fetches, manifest resolution, referrers listing), each request/response pair is assigned a sequential integer ID so that log lines from different goroutines can be correlated even when interleaved.

### Capabilities Used

- **`remote.NewLoggingTransport`** — Wraps an inner transport with debug logging; accepts an optional `*slog.Logger` (defaults to `slog.Default()`).
- **`log/slog`** (stdlib) — Structured logging; configure a handler to write JSON, text, or any custom format.

### Typical Flow

The easiest way to enable debug logging is via `ClientBuilder.Logger`. When set,
`ClientBuilder` wraps the transport stack automatically — logging sits outside retry
so each attempt is individually logged:

```go
configs, _ := config.LoadConfigs()
props, _ := configs.RegistryProperties("registry.example.com/app")

builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})
builder.Logger = slog.Default() // enable HTTP debug logging

repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

For a custom JSON logger routed to a specific output:

```go
builder.Logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
```

When using `ClientBuilder`, the transport stack is:
`LoggingTransport → retry.Transport → http.Transport (TLS from certs.d)`

If you need to add logging without `ClientBuilder`, use `NewLoggingTransport` directly:

```go
transport := remote.NewLoggingTransport(retry.NewTransport(nil), slog.Default())
repo.Registry.Client = &auth.Client{
    Client:     &http.Client{Transport: transport},
    CredentialFunc: remote.NewCredentialFunc(store),
}
```

### Safety

- `Authorization` and `Set-Cookie` headers are replaced with `*****`.
- Response bodies containing `"token"` or `"access_token"` fields are redacted.
- Only `application/json`, `text/plain`, `text/html`, and `*+json` content types are printed.
- Body reads are capped at 16 KiB; larger bodies are truncated.
- The response body is fully restored after logging so callers see the complete body.

---

## 14. Referrers

OCI Referrers allow attaching metadata artifacts (signatures, SBOMs, attestations, provenance) to an existing subject manifest. oras-go provides both low-level `remote.Repository` methods and the higher-level `oras.ExtendedCopy` for working with referrers.

### Capabilities Used

- **`oras.PackManifest`** with `Subject` field — Create a referrer manifest linked to a subject digest.
- **`remote.Repository.Referrers`** — List all referrers for a given subject digest, with optional artifact type filtering.
- **`oras.ExtendedCopy`** — Copy an artifact and all of its attached referrers in one call.
- **`ocispec.Descriptor.ArtifactType`** — Filter referrers by type (e.g. only signatures, only SBOMs).

### Typical Flow

```go
// 1. Push the subject image first.
repo, _ := remote.NewRepository("registry.example.com/myapp")
subjectDesc, _ := oras.Copy(ctx, srcStore, "latest", repo, "latest", oras.DefaultCopyOptions)

// 2. Build a referrer manifest (e.g. an SBOM) linked to the subject.
sbomContent := []byte(`{"spdxVersion":"SPDX-2.3",...}`)
sbomDesc, _ := oras.PushBytes(ctx, repo, "application/spdx+json", sbomContent)

memStore := memory.New()
sbomManifestDesc, _ := oras.PackManifest(ctx, memStore, oras.PackManifestVersion1_1,
    "application/vnd.example.sbom.v1",
    oras.PackManifestOptions{
        Subject: &subjectDesc,
        Layers:  []ocispec.Descriptor{sbomDesc},
    },
)

// 3. Push the referrer manifest.
_, _ = oras.Copy(ctx, memStore, sbomManifestDesc.Digest.String(), repo, "", oras.DefaultCopyOptions)

// 4. List referrers for the subject.
err := repo.Referrers(ctx, subjectDesc, "", func(referrers []ocispec.Descriptor) error {
    for _, ref := range referrers {
        fmt.Printf("Referrer: %s (type: %s)\n", ref.Digest, ref.ArtifactType)
    }
    return nil
})

// 5. Filter by artifact type — only list SBOMs.
err = repo.Referrers(ctx, subjectDesc, "application/vnd.example.sbom.v1",
    func(referrers []ocispec.Descriptor) error {
        // Only referrers matching the artifact type are returned.
        return nil
    },
)
```

### Copying an Artifact with All Its Referrers

Use `oras.ExtendedCopy` to mirror an image and everything attached to it:

```go
srcRepo, _ := remote.NewRepository("registry.example.com/myapp")
dstRepo, _ := remote.NewRepository("mirror.example.com/myapp")

// Copies the subject manifest and all referrers (signatures, SBOMs, etc.).
_, _ = oras.ExtendedCopy(ctx, srcRepo, "latest", dstRepo, "latest", oras.DefaultExtendedCopyOptions)
```

---

## 15. Structured Error Handling

oras-go returns typed errors that allow callers to distinguish between categories of failure and act accordingly — for example, retrying on network errors but treating a missing artifact as a user error.

### Capabilities Used

- **`errdef.ErrNotFound`** — The referenced artifact, tag, or blob does not exist in the registry.
- **`errdef.ErrAlreadyExists`** — The content was already present (push is idempotent, but callers may log or skip).
- **`errdef.ErrSizeExceedsLimit`** — Content exceeds the configured size limit.
- **`oras.CopyError`** — Wraps errors from `oras.Copy` / `oras.CopyGraph`, reporting the failing operation (`Op`, e.g. `"Fetch"`, `"Push"`, `"Mount"`) and whether the error originated from the source or destination.
- **`oras.CopyErrorOrigin`** — Enum distinguishing `CopyErrorOriginSource` from `CopyErrorOriginDestination`.
- **`errors.As`** (stdlib) — Unwrap typed errors for structured handling.

### Typical Flow

```go
// Check whether a manifest exists before attempting a pull.
_, err := repo.Resolve(ctx, "myapp:v1.0")
if errors.Is(err, errdef.ErrNotFound) {
    log.Println("image not found — skipping")
} else if err != nil {
    return fmt.Errorf("resolve failed: %w", err)
}

// Push content and handle the already-exists case gracefully.
desc, err := oras.PushBytes(ctx, repo, mediaType, content)
if errors.Is(err, errdef.ErrAlreadyExists) {
    log.Printf("content %s already present", desc.Digest)
} else if err != nil {
    return err
}

// Copy with structured error reporting — distinguish source from destination failures.
_, err = oras.Copy(ctx, src, "latest", dst, "latest", oras.DefaultCopyOptions)
if err != nil {
    var copyErr *oras.CopyError
    if errors.As(err, &copyErr) {
        switch copyErr.Origin {
        case oras.CopyErrorOriginSource:
            log.Printf("source operation %q failed: %v", copyErr.Op, copyErr.Err)
        case oras.CopyErrorOriginDestination:
            log.Printf("destination operation %q failed: %v", copyErr.Op, copyErr.Err)
        }
    }
    return err
}
```

---

## 16. Registry Mirror Fallback

Where scenario 6 copies content *into* a mirror you control, this scenario reads
*through* mirrors declared in `registries.conf`. A `Repository` built from
registry properties tries each applicable mirror in order and falls back to the
primary registry, which is what makes pulls work in air-gapped, rate-limited,
and pull-through-cache environments without changing any references in your code.

### Capabilities Used

- **`config.RegistriesConfig`** — Parses the `[[registry.mirror]]` blocks of `registries.conf`.
- **`properties.Mirror`** — A resolved mirror endpoint: `Location`, per-mirror `Transport`, and `PullFromMirror` policy.
- **`properties.Registry.Mirrors`** — The ordered mirror list attached to the target registry.
- **`remote.NewRepositoryWithProperties`** — Builds a `Repository` wired with the mirror list; a `Repository` from `remote.NewRepository` has no mirrors.
- **`remote.PullFromMirrorAll` / `PullFromMirrorDigestOnly` / `PullFromMirrorTagOnly`** — The pull-policy constants.

### Configuration

```toml
# /etc/containers/registries.conf
[[registry]]
prefix = "docker.io"
location = "docker.io"

  [[registry.mirror]]
  location = "mirror.corp.internal"
  pull-from-mirror = "all"

  [[registry.mirror]]
  location = "backup-mirror.corp.internal"
  insecure = true
```

### Typical Flow

Mirrors require no API of their own — load the config, build the repository from
properties, and read as usual:

```go
configs, _ := config.LoadConfigs()

// props.Mirrors is populated from the [[registry.mirror]] blocks matching
// this reference. ErrRegistryBlocked is returned here if the registry is
// marked blocked = true in registries.conf.
props, _ := configs.RegistryProperties("docker.io/library/nginx")

builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})

// The Repository carries the mirror list; reads transparently use it.
repo, _ := remote.NewRepositoryWithProperties(props, builder)

// Tries mirror.corp.internal, then backup-mirror.corp.internal,
// then docker.io — first success wins.
_, _ = oras.Copy(ctx, repo, "latest", localStore, "latest", oras.DefaultCopyOptions)
```

### Fallback Semantics

- **Reads only.** `Resolve`, `Fetch`, `FetchReference`, and `Exists` consult mirrors. Pushes, tags, and deletes always go to the primary registry, so a read-only mirror never receives writes.
- **In order, first success wins.** Mirrors are tried in the order they appear in `registries.conf`, and the primary registry is the final fallback.
- **Not every error falls through.** Context cancellation and deadline expiry stop the chain immediately and are returned as-is, rather than being retried against every remaining mirror.
- **Per-mirror transport.** Each mirror carries its own `Transport`, so an `insecure = true` mirror does not relax TLS for the primary.
- **References are rewritten.** A fully-qualified reference is reduced to a bare tag or `@digest` before being handed to a mirror, so the mirror combines it with its own base instead of rejecting it as a host mismatch.

### Restricting When a Mirror Is Used

`pull-from-mirror` controls which references a mirror may serve:

| Value | Behaviour |
|---|---|
| `"all"` (or empty) | Mirror handles both tag and digest references. |
| `"digest-only"` | Mirror handles only digest references. |
| `"tag-only"` | Mirror handles only tag references. |

`digest-only` is the safe default for an untrusted cache: a digest reference is
content-addressed, so a mirror cannot substitute different content, whereas a
mirror serving a tag decides for itself what `latest` means. Setting
`mirror-by-digest-only = true` on the registry applies `digest-only` to every
mirror under it that does not set its own policy.

---

## 17. Bearer Token Flow Selection

When a registry answers with a `Bearer` challenge and the credential is a
username/password pair, there are two ways to exchange it for a token at the
token endpoint. oras-go defaults to OAuth2, but the OAuth2 password grant is a
Docker extension rather than part of the OCI distribution spec, so registries
that implement only the spec'd endpoint need the other flow.

This selects only the *token exchange*. The authentication scheme itself is
always whatever the registry advertises in `WWW-Authenticate`, and under both
flows the registry is authenticated with a bearer token. Anonymous,
access-token, and refresh-token credentials are unaffected.

### Capabilities Used

- **`properties.TokenFlow`** — `TokenFlowDefault`, `TokenFlowOAuth2`, `TokenFlowDistribution`.
- **`properties.Registry.Attributes.TokenFlow`** — Per-registry selection, settable from config or from a CLI flag.
- **`registries.conf` `token-flow` key** — Declarative per-registry selection (an ORAS-specific extension).
- **`auth.TokenFetcher`** — Interface for supplying a token acquisition strategy of your own.
- **`auth.NewCompositeTokenFetcher`** — Builds the standard fetcher pair; the `legacyMode` argument selects the distribution flow for credentialed access.
- **`remote.ClientBuilder.TokenFetcher`** — Overrides the flow for every registry the builder serves.

### Via registries.conf

```toml
[[registry]]
prefix = "registry.internal.corp"
token-flow = "distribution"
```

An invalid value is rejected rather than ignored — `RegistryProperties` returns
an error naming the offending registry. Silently falling back to the default
would leave a user who typo'd the flow authenticating a way they did not ask
for, and the resulting failure looks like bad credentials.

### Via Properties

```go
configs, _ := config.LoadConfigs()
props, _ := configs.RegistryProperties("registry.internal.corp/app")

// Override from a CLI flag, after config has been applied.
if *tokenFlow == "distribution" {
    props.Attributes.TokenFlow = properties.TokenFlowDistribution
}

builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})
repo, _ := remote.NewRepositoryWithProperties(props, builder)
```

### Supplying a Custom Fetcher

`ClientBuilder.TokenFetcher` takes precedence over `props.Attributes.TokenFlow`
for every registry the builder serves — it is the caller's explicit choice, so
it wins over configuration. Use it to force the distribution flow globally, or
to plug in an entirely different strategy:

```go
// Force the distribution flow (the oras-go v1/v2 behaviour) everywhere.
builder.TokenFetcher = auth.NewCompositeTokenFetcher(
    http.DefaultClient, nil, "", true)
```

Any type implementing `auth.TokenFetcher` works, which is the hook for
federated or workload-identity token exchange:

```go
type TokenFetcher interface {
    FetchToken(ctx context.Context, params TokenParams, cred credentials.Credential) (string, error)
}
```

### Which Flow to Use

| Situation | Flow |
|---|---|
| Docker Hub, GHCR, ECR, ACR, GAR, most hosted registries | `TokenFlowDefault` (OAuth2) |
| Registry implements only the distribution-spec token endpoint | `TokenFlowDistribution` |
| Token exchange fails with 404 or 405 at the token endpoint | Try `TokenFlowDistribution` |
| Migrating from oras-go v1 or v2 and auth regressed | `TokenFlowDistribution` — that was the old default |

---

## 18. Bounded Listing and Pagination Limits

Catalog, tag, and referrer listings are paginated, and the number of pages is
controlled by the registry, not by the caller. A registry that returns a
`Link` header on every page — whether through misconfiguration or malice —
makes an unbounded listing loop forever. The page-limit fields cap that work
and surface a typed error when the cap is hit.

### Capabilities Used

- **`Registry.RepositoryListMaxPages`** — Caps pages fetched during catalog listing.
- **`Registry.TagListMaxPages` / `Registry.ReferrerListMaxPages`** — Registry-wide defaults for tag and referrer listings.
- **`Repository.TagListMaxPages` / `Repository.ReferrerListMaxPages`** — Per-repository overrides, applied when greater than zero.
- **`errdef.ErrTooManyPages`** — Returned (wrapped) when a listing exceeds its cap.
- **`Registry.MaxMetadataBytes`** — Complementary cap on the response size of a single metadata call.

### Typical Flow

```go
repo, _ := remote.NewRepository("registry.example.com/myapp")

// Registry-wide defaults.
repo.Registry.RepositoryListMaxPages = 100
repo.Registry.TagListMaxPages = 50
repo.Registry.ReferrerListMaxPages = 20

// Per-repository override for a repository known to have many tags.
repo.TagListMaxPages = 200

err := repo.Tags(ctx, "", func(tags []string) error {
    for _, tag := range tags {
        fmt.Println(tag)
    }
    return nil
})
if errors.Is(err, errdef.ErrTooManyPages) {
    // Partial results were already delivered to the callback.
    log.Printf("tag listing truncated: %v", err)
}
```

### Resolution Order

For tags and referrers, the effective limit is:

1. The `Repository` field, if greater than zero.
2. Otherwise the `Registry` field.
3. If both are zero, the listing is **unlimited**.

Zero means unlimited rather than "fetch nothing", so the default behaviour is
unchanged and these fields are strictly opt-in. Catalog listing has no
per-repository tier — it is a registry-level operation, so only
`Registry.RepositoryListMaxPages` applies.

### Partial Results

The limit is enforced between pages, so the callback has already been invoked
for every page fetched before the cap was reached. Callers that need
all-or-nothing semantics should accumulate into a local slice and discard it
when `errdef.ErrTooManyPages` is returned, rather than acting on what the
callback already received.

---

## 19. Namespace-Scoped Authentication (Proposed)

Registries that partition access by namespace — ECR, Artifact Registry, Harbor
projects, GitLab groups — issue different credentials for different paths under
the same host. Authenticating against `example.com/team-a` and
`example.com/team-b` independently requires a value that names a registry
*optionally narrowed to a namespace or repository*, and credential storage keyed
on that value rather than on the host alone.

> **Status:** not yet implemented. Tracked in
> [oras-project/oras-go#1348](https://github.com/oras-project/oras-go/issues/1348).
> The library today is host-only end to end: `remote.Login` keys the store on
> `Reference.Registry`, and `credentials.CredentialFunc` receives only a
> `hostport`, so a namespaced key could never be read back.

### Capabilities Used

- **`RegistryScope`** — A registry host plus an optional namespace or repository
  path, never carrying a tag or digest. Named after the existing `scope`
  vocabulary in `config.RegistriesDConfig.GetLookasideURLs`, which already
  matches `registry.example.com/namespace` by longest prefix.
- **`auth.ScopeRepository` / `auth.AppendScopesForHost`** — Carry the namespace
  into the token request. `auth.Client` merges context scope hints with the
  scope named in the `WWW-Authenticate` challenge.
- **`credentials.StoreOptions.Hierarchical`** — Longest-prefix credential
  lookup, as used by containers-auth.json: `host/ns/repo` falls back to
  `host/ns`, then to `host`.

### Typical Flow

```go
// "example.com", "example.com/myspace", and "example.com/myspace/app" are all
// valid; a tag or digest is rejected.
scope, err := ParseRegistryScope(target)
if err != nil {
    return err
}

// Hint the namespace so the token is requested for it, not for the bare host.
// Pull-only: a login is a credential check, and demanding push would reject a
// legitimate read-only account.
if scope.Path != "" {
    ctx = auth.AppendScopesForHost(ctx, scope.Registry,
        auth.ScopeRepository(scope.Path, auth.ActionPull))
}

// Store under the scope, so two namespaces on one host do not collide.
store, _ := credentials.NewStore(configPath, credentials.StoreOptions{
    Hierarchical: true,
})
```

### Namespace and Repository Are Not Distinguishable

`repositoryRegexp` accepts multi-segment paths, so `myspace` and `myspace/app`
are syntactically identical. The library cannot tell a namespace from a
repository and does not try — `RegistryScope` carries a single `Path`, and what
it means is the registry's business.

Parsing splits the host off before inspecting the path. Splitting the other way
makes the port in `localhost:5000` parse as a tag.

### What a Scoped Login Proves

Less than it appears. The token fetch only fires on a `401`, so a registry that
serves `GET /v2/` anonymously validates nothing. And the distribution spec
permits a token server to grant a narrower scope than requested without
erroring, so a successful ping is not proof the caller holds the namespace.
There is no registry endpoint for a namespace, so this may be the ceiling.

---

## Summary Matrix

| Scenario | Key Packages | Config Loading | Policy | Signatures |
|---|---|---|---|---|
| Full config stack | `oras`, `remote`, `config`, `credentials` | Full stack | Optional | No |
| CLI tool with flag overrides | `oras`, `remote`, `config`, `credentials`, `properties` | Full stack + overrides | Optional | No |
| Policy enforcement | `oras`, `remote`, `config`, `policy`, `signature` | Full stack | Yes | Yes |
| Artifact distribution | `oras`, `remote`, `memory` | Optional | No | No |
| Object-oriented artifacts | `objects`, `memory` | Optional | No | No |
| Registry mirroring | `oras`, `remote` | Optional | No | No |
| OCI local storage | `oras`, `oci`, `file`, `memory` | None | No | No |
| Content caching | `content/cache`, `content/oci` | Optional (env var) | No | No |
| Credential management | `credentials`, `auth`, `config` | Docker + containers auth | No | No |
| Signature verification | `config`, `policy`, `signature` | Policy + registries.d | Yes | Yes |
| Middleware | `remote`, `policy` | Varies | Optional | Optional |
| Retry transport | `remote/retry` | None | No | No |
| Debug logging transport | `remote` | None | No | No |
| Referrers | `oras`, `remote` | Optional | No | No |
| Structured error handling | `oras`, `errdef` | None | No | No |
| Registry mirror fallback | `remote`, `config`, `properties` | registries.conf | Optional | No |
| Bearer token flow selection | `remote`, `remote/auth`, `config`, `properties` | registries.conf | No | No |
| Bounded listing and pagination | `remote`, `errdef` | None | No | No |
| Namespace-scoped authentication *(proposed)* | `remote/auth`, `credentials`, `config` | containers auth (hierarchical) | No | No |

---

## Glossary

| Term | Definition |
|---|---|
| **Artifact** | Any piece of content stored in an OCI registry — container images, Helm charts, WASM modules, SBOMs, signatures, etc. In OCI Image Manifest v1.1, the `artifactType` field declares what kind of artifact a manifest represents. |
| **Blob** | An opaque, content-addressable chunk of data stored in a registry. Blobs hold layer data, configuration objects, and other binary content. Each blob is identified by its digest. |
| **Blob Mounting** | A registry optimization that copies a blob between repositories on the same registry without re-uploading the bytes. Also called *cross-repository mounting*. |
| **Descriptor** | A small JSON object (`ocispec.Descriptor`) that references a piece of content by its media type, digest, and size. Descriptors appear in manifests and indexes to point to blobs, configs, and layers. |
| **Digest** | A content-addressable identifier of the form `algorithm:hex` (e.g., `sha256:abc123…`). Two pieces of content with the same bytes always produce the same digest. |
| **Index** | An OCI Image Index (also called a *manifest list*) that groups multiple manifests — typically one per platform (OS + architecture) — under a single reference. |
| **Lookaside** | An external storage location (file path or HTTPS URL) configured in `registries.d` where detached image signatures are stored and fetched, separate from the registry itself. |
| **Manifest** | A JSON document that describes a single OCI artifact — its config descriptor, layer descriptors, annotations, and optional subject. A manifest is itself stored as a blob and addressed by digest. |
| **Media Type** | A MIME-type string (e.g., `application/vnd.oci.image.manifest.v1+json`) that declares the format of a blob or manifest. |
| **Mirror** | An alternate registry endpoint, declared in `registries.conf`, that is tried before the primary registry when reading content. Mirrors serve reads only; writes always go to the primary. |
| **Namespace** | A path prefix within a registry that groups repositories (e.g., `myspace` in `example.com/myspace/app`). Namespaces are not distinguishable from repositories by syntax alone — both are multi-segment paths — so the distinction is enforced by the registry, not by a reference parser. |
| **OCI** | The Open Container Initiative — a set of specifications for container image formats, runtime behavior, and distribution (registry) APIs. |
| **OCI Layout** | An on-disk directory structure defined by the OCI Image Layout Specification for storing image content offline. Contains an `index.json`, an `oci-layout` marker, and a `blobs/` directory. |
| **Platform** | An OS and architecture combination (e.g., `linux/amd64`, `linux/arm64`) used to select the correct manifest from an index. |
| **Reference** | A string that identifies content in a registry. A reference can be a tag (e.g., `latest`) or a digest (e.g., `sha256:abc…`). A fully qualified reference includes the registry and repository (e.g., `registry.example.com/myapp:v1.0`). |
| **Referrer** | An artifact whose manifest contains a `subject` field pointing to another manifest's digest. Referrers attach metadata (signatures, SBOMs, attestations) to a subject artifact. |
| **Registry** | A server that hosts OCI repositories and implements the OCI Distribution Specification API for pushing, pulling, and discovering content. |
| **Repository** | A named collection of manifests within a registry (e.g., `registry.example.com/myapp`). A repository can contain multiple tags and digests. |
| **Scope** | In auth, the resource and actions a bearer token is requested for, written `repository:<name>:<actions>` (e.g., `repository:myspace/app:pull,push`). In `registries.d` and containers-auth.json, *scope* instead means a registry host with an optional namespace path (e.g., `example.com/myspace`), matched by longest prefix. |
| **Subject** | The manifest that a referrer points to via its `subject` field. For example, if an SBOM manifest references an image manifest, the image manifest is the subject. |
| **Tag** | A mutable, human-readable name (e.g., `latest`, `v1.0`) that points to a single manifest digest within a repository. Tags can be reassigned to different digests over time. |
