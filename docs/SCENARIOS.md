# ORAS Go Library — Usage Scenarios

This document describes the primary scenarios where oras-go is used and how the library's features map to each scenario. It is intended for contributors, integrators, and anyone evaluating oras-go for their project.

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
desc, _ := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, "application/vnd.myapp.config.v1", oras.PackManifestOptions{
    Layers: layerDescriptors,
})

// 6. Push to registry.
_, _ = oras.Copy(ctx, fs, desc.Digest.String(), repo, "latest", oras.DefaultCopyOptions)
```

Not all use cases require the full configuration stack. The remaining scenarios below demonstrate using individual features when simpler setups suffice.

### Benefits of Loading Full Configs

Loading the full configuration stack provides significant benefits:

- **Broader credential coverage** — Reads both Docker `config.json` and containers `auth.json`, so credentials stored by either Docker or Podman are found automatically.
- **Per-registry TLS** — Picks up custom CA certificates and client certs from `certs.d` without requiring CLI flags.
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

**`LoadConfigsWithOptions`** lets you override specific paths. Any path you
set is used instead of the default for that config. However, fields left
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

---

## 2. CLI Tool with Flag Overrides

CLI tools typically load the full configuration stack and then override specific settings from command-line flags. The library's layered credential resolution and mutable property fields make this straightforward.

### Capabilities Used

- **`config.LoadConfigs`** — Load all container ecosystem configs as a baseline.
- **`properties.Registry`** — Mutable struct whose transport, credential, and attribute fields can be overridden after creation.
- **`credentials.Credential`** — Direct credential that takes priority over the credential store when set on properties.
- **`remote.ClientBuilder`** — Credential store acts as a fallback when no explicit credential is set on properties.

### Typical Flow

```go
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

This means CLI flags always win when provided, and config-file credentials are used automatically when they are not.

---

## 3. Policy Enforcement

Policy evaluation and signature verification can be added to the configuration-driven workflow to enforce allow/deny decisions before pulling images.

### Capabilities Used

- **`config.LoadConfigs`** — Unified loader for Docker config.json, containers auth.json, registries.conf, policy.json, registries.d, and certs.d.
- **`config.RegistriesConfig`** — Registry mirrors, blocked registries, unqualified search registries, and prefix-based rewriting.
- **`policy.Policy` / `policy.Evaluator`** — Containers-policy.json evaluation (accept, reject, signedBy, sigstoreSigned).
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
        Layers: sbomLayers,
    },
)

// Push to registry with multiple tags.
repo, _ := remote.NewRepository("registry.example.com/builds/sbom")
_, _ = oras.Copy(ctx, memStore, desc.Digest.String(), repo, "v1.2.3", oras.DefaultCopyOptions)
oras.TagN(ctx, repo, desc.Digest.String(), []string{"v1.2", "v1", "latest"}, oras.DefaultTagNOptions)
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

The objects package sits on top of the core ORAS APIs. Use the core APIs (`PackManifest` + `Copy`) when you need fine-grained control over the copy graph, hooks, or cross-repository blob mounting. Use the objects package when you want a simpler, more declarative interface for building and navigating artifacts.

---

## 6. Registry Mirroring and Replication

Registries can be mirrored for air-gapped environments, caching, or compliance.

### Capabilities Used

- **`oras.Copy` / `oras.CopyGraph`** — Deep copy of artifacts including all referenced blobs and manifests.
- **`CopyGraphOptions.PreCopy` / `PostCopy`** — Hook into copy operations for progress reporting, logging, or custom validation.
- **`CopyGraphOptions.MountFrom`** — Cross-mount blobs instead of re-uploading when source and destination are on the same registry.
- **`remote.Registry.Repositories`** — Enumerate all repositories in a source registry.
- **Referrers support** — Copy OCI referrers (signatures, attestations, SBOMs) alongside their subjects.

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

- Air-gapped deployments: export on connected machine, transfer media, import on isolated machine.
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
    CredentialFunc: remote.GetCredentialFunc(fallback),
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
allowed, _ := evaluator.Evaluate(ctx, policy.ImageReference{
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

- **`remote.RepositoryMiddleware`** — Wrap repositories with additional behavior (logging, metrics, policy, warning handling).
- **`remote.Compose`** — Chain multiple middlewares together.
- **`remote.WithPolicyEnforcement`** — Built-in middleware for applying container policy checks.
- **`remote.WithWarningHandler`** — Built-in middleware for processing registry warnings.
- **`CopyOptions.PolicyCheck`** — Callback hook for policy enforcement in the copy path.
- **`CopyGraphOptions.PreCopy` / `PostCopy` / `OnCopySkipped`** — Hooks for custom logic during graph traversal.

### Typical Flow

```go
// Compose middlewares for a production repository client.
middleware := remote.Compose(
    remote.WithPolicyEnforcement(evaluator, "docker", scope),
    remote.WithWarningHandler(func(w remote.Warning) {
        log.Printf("Registry warning: %s", w.Text)
    }),
    myCustomLoggingMiddleware(),
)

baseRepo, _ := remote.NewRepository("registry.example.com/app")
repo := middleware(baseRepo)
```

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
