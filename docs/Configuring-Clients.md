# Configuring registry clients

The [`registry/remote/config`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config) package loads the configuration used by container tools and turns it into the properties needed to build an ORAS registry client. Together with [`remote.ClientBuilder`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#ClientBuilder), it provides one path for applying credentials, TLS settings, mirrors, retry behavior, and image policy.

## Load configuration

[`config.LoadConfigs`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#LoadConfigs) loads Docker `config.json`, containers `auth.json`, `registries.conf`, `policy.json`, `registries.d`, and `containers-certs.d` from their default locations. A missing file is skipped. A file that exists but cannot be parsed returns an error.

The default [`StrategyContainersImage`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#StrategyContainersImage) strategy uses system and user configuration tiers and merges all applicable configuration. The experimental [`StrategyUAPI`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#StrategyUAPI) strategy uses vendor, system, and user tiers, with first-found-wins behavior for the main configuration files.

Use [`config.LoadConfigsWithOptions`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#LoadConfigsWithOptions) to select a strategy or override individual paths. Unspecified paths still use the selected strategy's defaults.

## Build a repository

Building a configured repository has four steps:

1. Call [`Configs.RegistryProperties`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#Configs.RegistryProperties) with the image reference. It applies `registries.conf` rewriting and mirror settings, then adds TLS settings from `containers-certs.d`.
2. Call [`Configs.CredentialStore`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#Configs.CredentialStore) and assign the result to [`ClientBuilder.CredentialStore`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#ClientBuilder). It returns an error when neither Docker config nor containers `auth.json` was loaded. Docker config is checked first. Containers `auth.json` is the fallback and uses longest-prefix namespace matching.
3. If image policy is enabled, call [`Configs.PolicyEvaluator`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/config#Configs.PolicyEvaluator) and assign the result to [`ClientBuilder.PolicyEvaluator`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#ClientBuilder).
4. Call [`ClientBuilder.NewRepository`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote#ClientBuilder.NewRepository) with the properties.

The builder also accepts a custom retry policy, user agent, debug logger, warning logger, token fetcher, and authentication-cache factory. Defaults are used for fields that are not set. A builder may be reused; by default, its cache factory creates a new authentication cache for every client it builds, so repositories do not share a cache.

See the [complete configuration example](./configuring-clients/main.go), which CI compiles against the current API. It loads the host's available configuration, wires credentials and policy when present, and constructs a repository without making a network request.

## Mirrors

Mirrors come from `registries.conf` and are applied by `ClientBuilder.NewRepository`. For reads, the repository tries configured mirrors in order and then falls back to the primary registry. The `pull-from-mirror` value controls which references a mirror accepts:

- `all` accepts tags and digests.
- `digest-only` accepts only digest references.
- `tag-only` accepts only tag references.

A context cancellation or deadline error stops the operation immediately instead of trying another mirror. Writes always go to the primary registry. Each mirror gets its own client. Credentials are resolved for the mirror's host through the credential store rather than copied from the primary. The mirror's transport takes only `insecure` from `registries.conf`; `containers-certs.d` TLS settings are applied to the primary host only.

## Policy and signatures

When a policy evaluator is configured, `reject` and `insecureAcceptAnything` requirements are checked before network I/O. The `signedBy` and `sigstoreSigned` requirements need a manifest digest: for digest references they also run before network I/O, and for tag references they run after the tag is resolved. Blob operations are checked against reference-level requirements only. A policy rejection returns an error wrapping [`policy.ErrDenied`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/policy#ErrDenied); an evaluator or verifier failure is returned as an operational error instead.

To enable `signedBy`, create one verifier with [`signature.NewSignedByVerifierFromConfig`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/signature#NewSignedByVerifierFromConfig) and pass it to `Configs.PolicyEvaluator` with [`policy.WithSignedByVerifier`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/policy#WithSignedByVerifier). The verifier is safe to share across repositories. It resolves `registries.d` lookaside storage for each repository scope and fails closed when a scope has no configured store. To enable `sigstoreSigned`, also pass a verifier with [`policy.WithSigstoreVerifier`](https://pkg.go.dev/github.com/oras-project/oras-go/v3/registry/remote/policy#WithSigstoreVerifier); without one, those requirements fail with `ErrUnsupported`.
