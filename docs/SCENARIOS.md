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

// 3. Build a configured client with credentials from Docker/Podman config.
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

---

## 2. Policy Enforcement

Policy evaluation and signature verification can be added to the configuration-driven workflow to enforce allow/deny decisions before pulling images.

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
configs, _ := config.LoadConfigs()

// 2. Check policy before pulling.
ref := "docker.io/library/nginx:latest"
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
if !allowed {
    log.Fatal("image rejected by policy")
}

// 3. Set up repository with credentials, TLS, and mirror resolution from configs.
props, _ := configs.RegistryProperties(ref)
builder := remote.NewClientBuilder()
builder.CredentialStore, _ = configs.CredentialStore(credentials.StoreOptions{})
repo, _ := remote.NewRepositoryWithProperties(props, builder)

// 4. Pull the image.
_, _ = oras.Copy(ctx, repo, ref, localStore, "", oras.DefaultCopyOptions)
```

---

## 3. Artifact Packing and Distribution

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

## 4. Registry Mirroring and Replication

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

## 5. OCI Layout and Local Storage

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

## 6. Credential Management

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

## 7. Image Signature Verification

Image provenance and integrity can be enforced before pulling or running images.

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
configs, _ := config.LoadConfigs()

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
| Policy enforcement | `oras`, `remote`, `config`, `policy`, `signature` | Full stack | Yes | Yes |
| Artifact distribution | `oras`, `remote`, `memory` | Optional | No | No |
| Registry mirroring | `oras`, `remote` | Optional | No | No |
| OCI local storage | `oras`, `oci`, `file`, `memory` | None | No | No |
| Credential management | `credentials`, `auth`, `config` | Docker + containers auth | No | No |
| Signature verification | `config`, `policy`, `signature` | Policy + registries.d | Yes | Yes |
| Middleware | `remote`, `policy` | Varies | Optional | Optional |
