# ORAS Go Library — Usage Scenarios

This document describes the primary scenarios where oras-go is used and how the library's features map to each use case. It is intended for contributors, integrators, and anyone evaluating oras-go for their project.

---

## 1. CLI Tools (oras CLI, Helm, Notation, Flux)

Command-line tools are the most common consumers of oras-go. These tools push, pull, tag, and manage OCI artifacts on remote registries.

### Capabilities Used

- **`config.LoadConfigs`** — Unified loader for Docker config.json, containers auth.json, registries.conf, registries.d, and certs.d. CLI tools benefit from loading the full configuration stack to respect the user's existing container ecosystem settings.
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
configs, _ := config.LoadConfigs(config.LoadConfigsOptions{})

// 2. Set up repository with credentials and TLS from loaded configs.
repo, _ := remote.NewRepository("registry.example.com/myapp")
repo.Client = &auth.Client{
    Credential: configs.CredentialFunc(),
}

// 3. Pack local files into an OCI manifest.
fs, _ := file.New("/tmp/workspace")
defer fs.Close()
desc, _ := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, "application/vnd.myapp.config.v1", oras.PackManifestOptions{
    Layers: layerDescriptors,
})

// 4. Push to registry.
_, _ = oras.Copy(ctx, fs, desc.Digest.String(), repo, "latest", oras.DefaultCopyOptions)
```

### Why Load Full Configs

Even though CLI tools may not need policy or signature verification, loading the full configuration stack provides significant benefits:

- **Broader credential coverage** — Reads both Docker `config.json` and containers `auth.json`, so credentials stored by either Docker or Podman are found automatically.
- **Per-registry TLS** — Picks up custom CA certificates and client certs from `certs.d` without requiring CLI flags.
- **Mirror support** — Respects registry mirrors configured in `registries.conf`, which is essential for enterprise and air-gapped environments.
- **Ecosystem consistency** — Users configure these files once and expect all registry-interacting tools to respect them.

### Why oras-go

CLI tools need a library that abstracts the OCI Distribution Spec without requiring a container runtime. oras-go provides a pure-Go client with no daemon dependency, making it suitable for lightweight CLI binaries.

---

## 2. Container Runtimes and Image Managers (Podman, Buildah, Skopeo-like tools)

These tools interact with registries at a lower level and require full containers-ecosystem configuration support.

### Capabilities Used

- **`config.LoadConfigs`** — Unified loader for Docker config.json, containers auth.json, registries.conf, policy.json, registries.d, and certs.d.
- **`config.RegistriesConfig`** — Registry mirrors, blocked registries, unqualified search registries, and prefix-based rewriting.
- **`config.Policy` / `policy.Evaluator`** — Containers-policy.json evaluation (accept, reject, signedBy, sigstoreSigned).
- **`signature.NewSignedByVerifier`** — OpenPGP signature verification via lookaside storage.
- **`signature.LookasideStore`** — Fetch/store simple signing signatures from file:// or https:// lookaside locations configured in registries.d.
- **TLS configuration via certs.d** — Per-registry TLS certificates.

### Typical Flow

```go
// 1. Load all container ecosystem configs at once.
configs, _ := config.LoadConfigs(config.LoadConfigsOptions{})

// 2. Resolve mirrors and rewrites for a pull.
ref := "docker.io/library/nginx:latest"
locations := configs.RegistriesConfig.ResolveReference(ref)

// 3. Check policy before pulling.
evaluator, _ := policy.NewEvaluator(configs.PolicyConfig,
    policy.WithSignedByVerifier(
        signature.NewSignedByVerifier(
            signature.NewLookasideStoreFromConfig(configs.RegistriesDConfig, scope),
        ),
    ),
)
allowed, _ := evaluator.Evaluate(ctx, policy.ImageReference{
    Transport: "docker",
    Scope:     "docker.io/library/nginx",
    Reference: ref,
})

// 4. Pull from the first available mirror.
for _, loc := range locations {
    repo, _ := remote.NewRepository(loc)
    // configure TLS from certs.d, auth from configs...
    _, _ = oras.Copy(ctx, repo, ref, localStore, "", oras.DefaultCopyOptions)
}
```

### Key Difference from CLI Tools

Both CLI tools and container runtimes should load the full configuration stack via `config.LoadConfigs`. The key difference is that container runtimes additionally require policy evaluation and signature verification before pulling or running images, whereas CLI tools typically do not enforce these checks.

---

## 3. CI/CD Pipelines and Artifact Distribution

Build systems use oras-go to publish build outputs (binaries, SBOMs, Helm charts, WASM modules) as OCI artifacts.

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

## 4. Registry Mirroring and Replication

Enterprises mirror registries for air-gapped environments, caching, or compliance.

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

## 5. OCI Layout and Local Storage

Applications that work with OCI artifacts offline use local storage backends.

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

## 6. Credential Management Libraries

Applications that manage registry credentials across Docker, Podman, and native platform keystores.

### Capabilities Used

- **`credentials.NewStoreFromDocker`** — Detects and uses Docker's credential helpers (docker-credential-osxkeychain, docker-credential-secretservice, etc.).
- **`credentials.NewFileStore`** — Direct file-based credential storage.
- **`credentials.Store` interface** — Pluggable credential backends with `Get`, `Put`, `Delete`.
- **`config.LoadConfigs`** — Load Docker config.json and containers auth.json simultaneously, with hierarchical namespace matching for Podman-style auth.
- **`auth.Client`** — HTTP client with automatic credential resolution, OAuth2 token exchange, and scope-based auth.

### Typical Flow

```go
// Create a credential function that checks multiple sources.
dockerStore, _ := credentials.NewStoreFromDocker(credentials.StoreOptions{})
fileStore, _ := credentials.NewFileStore("/custom/auth.json")
fallback := credentials.NewStoreWithFallbacks(dockerStore, fileStore)

client := &auth.Client{
    Credential: credentials.Credential(fallback),
}

repo, _ := remote.NewRepository("ghcr.io/org/repo")
repo.Client = client
```

---

## 7. Image Signature Verification

Applications that enforce image provenance and integrity before pulling or running images.

### Capabilities Used

- **`config.Policy`** — Load and evaluate containers-policy.json with requirement types: `insecureAcceptAnything`, `reject`, `signedBy`, `sigstoreSigned`.
- **`policy.Evaluator`** — Apply policy rules to image references.
- **`signature.SimpleSigningPayload`** — Parse and validate "atomic container signature" payloads.
- **`signature.VerifyOpenPGPSignature`** — Verify OpenPGP (GPG) detached signatures.
- **`signature.MatchSignedIdentity`** — Apply identity matching rules (exact, repository, remap, etc.).
- **`signature.LookasideStore`** — Fetch signatures from lookaside servers or local directories.

### Typical Flow

```go
// Load policy and registries.d config.
configs, _ := config.LoadConfigs(config.LoadConfigsOptions{})

// Build verifier from lookaside config.
sigStore := signature.NewLookasideStoreFromConfig(configs.RegistriesDConfig, scope)
verifier := signature.NewSignedByVerifier(sigStore)

// Create evaluator with verifier.
evaluator, _ := policy.NewEvaluator(configs.PolicyConfig,
    policy.WithSignedByVerifier(verifier),
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

## 8. Library Integration and Middleware

Libraries and frameworks that wrap oras-go to add cross-cutting concerns.

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
| CLI tools | `oras`, `remote`, `config`, `credentials` | Full stack | Optional | No |
| Container runtimes | `oras`, `remote`, `config`, `policy`, `signature` | Full stack | Yes | Yes |
| CI/CD pipelines | `oras`, `remote`, `memory` | Docker config.json | No | No |
| Registry mirroring | `oras`, `remote` | Optional | No | No |
| OCI local storage | `oras`, `oci`, `file`, `memory` | None | No | No |
| Credential management | `credentials`, `auth`, `config` | Docker + containers auth | No | No |
| Signature verification | `config`, `policy`, `signature` | Policy + registries.d | Yes | Yes |
| Middleware/library | `remote`, `policy` | Varies | Optional | Optional |
