# Config-to-Properties Field Mapping

This document describes how fields from container configuration files map to `properties.Registry` fields, and which fields are not yet covered.

## registries.conf → properties.Registry

| registries.conf field | properties field | Notes |
|---|---|---|
| `[[registry]].prefix` | (used for matching) | Not stored directly; drives `FindRegistry` lookup |
| `[[registry]].location` | `Reference.Registry` | Rewrites the registry host via `RewriteReference` |
| `[[registry]].insecure` | `Transport.Insecure` | Applied from the pre-rewrite registry entry |
| `[[registry]].blocked` | (error) | Returns `ErrRegistryBlocked` |
| `[[registry]].mirror-by-digest-only` | `Mirrors[].PullFromMirror` | Defaults empty `PullFromMirror` to `"digest-only"` |
| `[[registry.mirror]].location` | `Mirrors[].Location` | Mirror endpoint host |
| `[[registry.mirror]].insecure` | `Mirrors[].Transport.Insecure` | Per-mirror insecure flag |
| `[[registry.mirror]].pull-from-mirror` | `Mirrors[].PullFromMirror` | `"all"`, `"digest-only"`, or `"tag-only"` |
| `unqualified-search-registries` | (used by `SearchRegistryProperties`) | Returns a `[]properties.Registry` per search registry |
| `aliases` / `short-name-mode` | (used by `ResolveAlias`) | Resolved before property creation |

## Unmapped registries.conf fields

| Field | Reason |
|---|---|
| `short-name-mode` | Controls interactive prompting behavior; not relevant to properties |

## Unmapped properties fields (not populated by the bridge)

| properties field | Source | Notes |
|---|---|---|
| `Transport.CACert` | Not in registries.conf | Single CA cert; see also `CACerts` |
| `Transport.CACerts` | `containers-certs.d` `*.crt` | Populated by `LoadCertsDir` / `ApplyToTransport` |
| `Transport.Cert` | `containers-certs.d` `*.cert` | Populated by `LoadCertsDir` / `ApplyToTransport` |
| `Transport.Key` | `containers-certs.d` `*.key` | Populated by `LoadCertsDir` / `ApplyToTransport` |
| `Transport.PlainHTTP` | Programmatic only | Set by caller or builder logic |
| `Transport.HeaderFlags` | Programmatic only | Custom headers set by caller |
| `Credential` | Docker config.json | Flows through `credentials.Store`, not the properties bridge |
| `Attributes.ReferrersAPI` | Programmatic only | Set by caller based on registry capabilities |

## Docker config.json

Docker `config.json` credentials are **not** mapped through the properties bridge.
They flow through `credentials.Store` / `credentials.NewStoreFromDocker`, which is
set on the `ClientBuilder.CredentialStore` field separately.

Use `config.LoadConfigs()` to load both files in one call, then pass the Docker
config to `credentials.NewStoreFromConfig()` and the registries config to
`RegistryProperties()`.

## containers-certs.d

The `containers-certs.d` directory (`/etc/containers/certs.d/<host:port>/`)
provides per-registry TLS certificates. Use `LoadCertsDir` (or
`LoadCertsDirFromPaths` with custom base directories) to discover certificates,
then call `ApplyToTransport` to populate the transport fields:

| File pattern | Transport field |
|---|---|
| `*.crt` | `Transport.CACerts` |
| `*.cert` | `Transport.Cert` |
| `*.key` (matching `.cert` basename) | `Transport.Key` |

Search paths (in order): `/etc/containers/certs.d/`, `$HOME/.config/containers/certs.d/`.

These paths can be overridden via `LoadConfigsOptions.CertsDirPaths`.
