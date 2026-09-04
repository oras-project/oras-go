# Migration Guide

ORAS Go v3 changes the module path and reorganizes remote-registry
configuration so that a registry owns settings shared by its repositories. It
also moves credentials out of the `auth` package and makes several interface
and authentication changes.

This guide covers migration from v2 to v3. For the older v1 to v2 migration,
see the [v2 branch migration guide](https://github.com/oras-project/oras-go/blob/v2/MIGRATION_GUIDE.md).

## Upgrade the module

Replace the v2 module requirement and imports:

```diff
-oras.land/oras-go/v2
+github.com/oras-project/oras-go/v3
```

Then update the module graph:

```sh
go get github.com/oras-project/oras-go/v3
go mod tidy
```

The v2 and v3 module paths are distinct, so they can coexist temporarily while
an application is migrated package by package.

## Breaking changes

### Registry and repository configuration

In v2, `remote.Repository` duplicated configuration such as the HTTP client,
plain-HTTP setting, warning handler, and metadata limit. In v3, a
repository points to its parent `remote.Registry`, which owns those shared
settings. Per-repository overrides such as media types and tag/referrer page
sizes remain on `remote.Repository`.

| v2 | v3 |
| --- | --- |
| `repo.Client` | `repo.Registry.Client` |
| `repo.PlainHTTP` | `repo.Registry.PlainHTTP` |
| `repo.MaxMetadataBytes` | `repo.Registry.MaxMetadataBytes` |
| `repo.HandleWarning` | `repo.Registry.HandleWarning` |
| `repo.Reference` | `repo.Reference()` |
| `repo.Reference.Repository` | `repo.RepositoryName` |
| `repo.Reference.Registry` | `repo.Registry.Reference.Registry` |
| `remote.Registry.RepositoryOptions` | fields directly on `remote.Registry` |

Note that `Reference()` is a read-only getter; the reference can no longer be
set through it. The v2 struct-literal pattern
`&remote.Repository{Reference: ref, Client: c}` does not compile in v3;
construct repositories with `remote.NewRepository` or
`remote.NewRepositoryWithProperties` instead.

For example:

```go
// v2
repo, err := remote.NewRepository("localhost:5000/example")
if err != nil {
	return err
}
repo.Client = client
repo.PlainHTTP = true
ref := repo.Reference
```

becomes:

```go
// v3
repo, err := remote.NewRepository("localhost:5000/example")
if err != nil {
	return err
}
repo.Registry.Client = client
repo.Registry.PlainHTTP = true
ref := repo.Reference()
```

`repo.Reference` → `repo.Reference()` is not a pure rename. In v2 the field
held the full parsed reference, including any tag or digest passed to
`remote.NewRepository`; in v3 a `remote.Repository` identifies a repository
only, so `Reference()` returns just the registry and repository name, and
`remote.NewRepository` rejects a reference carrying a tag or digest with an
error wrapping `errdef.ErrInvalidReference`. Parse such input with
`properties.NewReference` and keep the tag or digest for the operation that
needs it.

`remote.NewRegistryWithProperties` and
`remote.NewRepositoryWithProperties` are the preferred constructors when
configuration comes from `registries.conf`, Docker configuration, certificates,
or policy files. They use `remote.ClientBuilder` to apply the same client and
transport rules to the primary registry and its mirrors.

### Authentication and credentials

Credential values and functions moved from `registry/remote/auth` to
`registry/remote/credentials`. The auth client field was renamed to make its
function type explicit.

| v2 | v3 |
| --- | --- |
| `auth.Credential` | `credentials.Credential` |
| `auth.EmptyCredential` | `credentials.EmptyCredential` |
| `auth.CredentialFunc` | `credentials.CredentialFunc` |
| `auth.StaticCredential` | `credentials.StaticCredentialFunc` |
| `auth.Client.Credential` | `auth.Client.CredentialFunc` |
| `credentials.Credential(store)` | `remote.NewCredentialFunc(store)` |
| `credentials.Login` / `credentials.Logout` | `remote.Login` / `remote.Logout` |
| `credentials.ServerAddressFromHostname` | `remote.ServerAddressFromHostname` |
| `credentials.ServerAddressFromRegistry` | `remote.ServerAddressFromRegistry` |
| `credentials.ErrClientTypeUnsupported` | `remote.ErrClientTypeUnsupported` |
| `auth.WithScopes(ctx, scopes...)` | `auth.WithScopesForHost(ctx, host, scopes...)` |
| `auth.AppendScopes(ctx, scopes...)` | `auth.AppendScopesForHost(ctx, host, scopes...)` |
| `auth.GetScopes(ctx)` | `auth.GetScopesForHost(ctx, host)` |
| `auth.GetAllScopesForHost(ctx, host)` | `auth.GetScopesForHost(ctx, host)` |

Scope hints are host-specific in v3. Pass the host of the registry request,
normally from `properties.Reference.Host()`, to the replacement functions. The
host must match the request host; for example, a `docker.io` reference resolves
to `registry-1.docker.io`.

For example:

```go
// v2
client := &auth.Client{
	Credential: auth.StaticCredential("registry.example.com", auth.Credential{
		Username: "user",
		Password: "password",
	}),
}
```

becomes:

```go
// v3
client := &auth.Client{
	CredentialFunc: credentials.StaticCredentialFunc(
		"registry.example.com",
		credentials.Credential{
			Username: "user",
			Password: "password",
		},
	),
}
```

The `credentials.Store` methods now accept and return
`credentials.Credential`. Custom stores must update their method signatures.

#### Native credential-store detection

`credentials.StoreOptions.DetectDefaultNativeStore` was replaced by the
inverse `IgnoreDefaultNativeStore` option. Detection is enabled by default in
v3:

```go
// Preserve the v2 zero-value behavior, which did not detect a native store.
store, err := credentials.NewStore(path, credentials.StoreOptions{
	IgnoreDefaultNativeStore: true,
})
```

When a native store is detected, v3 uses it for the current `DynamicStore` but
does not write the detected `credsStore` value back to the Docker configuration
file.

### Bearer token flow

`auth.Client.ForceAttemptOAuth2` was replaced by `auth.Client.TokenFetcher`.
With username/password credentials, the v3 default uses the OAuth2 password
grant. To preserve the v2 default and use the distribution-spec token flow,
configure a composite fetcher in legacy mode:

```go
client.TokenFetcher = auth.NewCompositeTokenFetcher(
	client.Client,
	client.Header,
	client.ClientID,
	true,
)
```

When using `remote.ClientBuilder`, the same choice can be made through
`properties.TokenFlowDistribution`. A caller-supplied
`ClientBuilder.TokenFetcher` takes precedence over the registry properties.

### Registry references

`registry.Reference` now records a tag and digest separately. This preserves
both parts of a reference such as `repository:tag@digest` instead of dropping
the tag. Its `Digest()` method was renamed to `GetDigest()` because `Digest` is
now a field. For references produced by the parsers, `GetReference()` returns
the digest when present or the tag otherwise.

The legacy `registry.Reference` type and `registry.ParseReference` function are
deprecated in v3. New code should use
`registry/remote/properties.Reference` and `properties.NewReference`:

```go
ref, err := properties.NewReference("registry.example.com/team/app:v1")
if err != nil {
	return err
}
fmt.Println(ref.Registry, ref.Repository, ref.Tag)
```

The parsers also accept `oci://`, `http://`, and `https://` prefixes.

### Repository interfaces, predecessors, and untagging

`registry.Repository` now embeds `content.PredecessorFinder`. Applications
that only consume a `registry.Repository` are unaffected, but custom
implementations must add:

```go
Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error)
```

This method returns the descriptors that directly point to `node`.

`registry.ManifestStore` now embeds `content.Untagger`. Implementations must
add:

```go
Untag(ctx context.Context, reference string) error
```

The concrete `remote.Repository` implements this method using the OCI
Distribution Specification tag-deletion API. Untagging a digest is rejected;
the reference must be a tag.

### Referrers capability

`remote.Repository.SetReferrersCapability` no longer returns an error. The
first value wins and later conflicting calls are ignored. Code that inspected
`remote.ErrReferrersCapabilityAlreadySet` should set the value and then read
back the effective state with the new `ReferrersCapability()` getter, which
returns a `properties.ReferrersAPI` (`ReferrersAPISupported`,
`ReferrersAPIUnsupported`, or `ReferrersAPIUnknown`) rather than a `bool`:

```go
repo.SetReferrersCapability(true)
effective := repo.ReferrersCapability()
```

`remote.ErrReferrersCapabilityAlreadySet` was removed.

### Removed v3 development APIs

Applications that tested against pre-release v3 snapshots may also need these
changes:

- Remove calls to `credentials.SetDefaultConfigLoader`. The credentials
  package now loads configuration directly.
- Remove uses of `credentials.ConfigFileLoader` and
  `credentials.ErrNoConfigLoader`; neither has a replacement.
- Remove the `SetCredentialsStore` and `Save` methods from custom
  implementations of `credentials.ConfigFile` (an interface introduced during
  v3 development); both were dropped from the interface.
- Remove the `force-basic-auth` key from `registries.conf` and uses of
  `properties.Attributes.ForceBasicAuth`. The option was never consumed. The
  registry's `WWW-Authenticate` challenge selects the authentication scheme;
  `token-flow` selects how a bearer token is acquired.

## Observable behavior changes

- `remote.NewRepository` returns an error wrapping `errdef.ErrInvalidReference`
  when the reference contains a tag or digest (e.g.
  `localhost:5000/example:v1`). In v2 such a reference was accepted and the
  tag or digest was kept on `repo.Reference`.
- `oras.CopyError` includes a `Descriptor` when a copy operation had already
  selected content. Its error string includes the digest in those cases. Use
  `errors.As` and the structured fields rather than comparing error strings.
- Registry repository listing can be bounded with `RepositoryListMaxPages`.
  Its zero value is unlimited; exceeding a configured limit wraps
  `errdef.ErrTooManyPages`. The existing tag and referrer page limits now also
  serve as defaults on `remote.Registry`, with repository-level overrides.
- A detected native credential store is no longer persisted to a shared Docker
  configuration file as a side effect of `DynamicStore.Put`.

## Major additions in v3

- **Unified configuration:** `registry/remote/config.LoadConfigs` loads Docker
  credentials, containers `auth.json`, `registries.conf`, `policy.json`,
  `registries.d`, and certificate directories. The default path strategy
  follows `containers/image`; the experimental UAPI strategy is also
  available.
- **Configured clients:** `remote.ClientBuilder` constructs clients,
  registries, and repositories with TLS, retries, credentials, token flow,
  warnings, logging, policy, and mirror settings.
- **Registry mirrors:** repositories created from registry properties try
  applicable mirrors for read operations and fall back to the primary.
  Writes always go to the primary.
- **Policy and signatures:** the `registry/remote/policy` package enforces
  allow/deny policy, and `registry/remote/signature` verifies OpenPGP simple
  signatures with `registries.d` lookaside storage.
- **Content caching:** `content/cache.CacheReadOnlyTarget` wraps a read-only
  target with a content store. `content/cache.NewFromEnv` uses `ORAS_CACHE`.
- **Hierarchical credential matching:** `credentials.StoreOptions.Hierarchical`
  enables longest-prefix namespace matching when reading credentials from the
  plaintext config file, as used by containers `auth.json` (Podman/Buildah).
- **HTTP diagnostics:** `remote.NewLoggingTransport` adds `slog`-based debug
  logging with sensitive-header redaction and bounded response bodies.
- **Tag deletion:** `remote.Repository.Untag` deletes a tag without deleting
  the referenced manifest.
- **Copy diagnostics and policy:** `oras.CopyError.Descriptor` identifies the
  failing content, and `oras.CopyOptions.PolicyCheck` can reject a copy before
  content transfer begins.

For current API documentation and examples, see
[pkg.go.dev](https://pkg.go.dev/github.com/oras-project/oras-go/v3).

## FAQs

### Can v2 and v3 be imported by the same application?

Yes. They have different module paths. This can make an incremental migration
easier, but values from the two majors have distinct Go types and must not be
mixed without explicit conversion.

### Where is the v1 to v2 guide?

It remains on the
[`v2` branch](https://github.com/oras-project/oras-go/blob/v2/MIGRATION_GUIDE.md),
next to the version it documents.

## Community Support

If you encounter challenges during migration, seek assistance from the
community by [submitting a GitHub issue](https://github.com/oras-project/oras-go/issues/new)
or asking in the [#oras](https://cloud-native.slack.com/archives/CJ1KHJM5Z)
Slack channel.
