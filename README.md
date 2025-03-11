# ORAS Go library

<p align="left">
<a href="https://oras.land/"><img src="https://oras.land/img/oras.svg" alt="banner" width="100px"></a>
</p>

ORAS Go is a Golang library that provides the ability to push and pull OCI artifacts to and from OCI registries. It implements the [OCI Distribution Specification](https://github.com/opencontainers/distribution-spec) and extends functionality to support various OCI artifacts.

## Project status

### Versioning

The ORAS Go library follows [Semantic Versioning](https://semver.org/), where breaking changes are reserved for MAJOR releases, and MINOR and PATCH releases must be 100% backwards compatible.

### v2: stable

[![Build Status](https://github.com/oras-project/oras-go/actions/workflows/build.yml/badge.svg?event=push&branch=main)](https://github.com/oras-project/oras-go/actions/workflows/build.yml?query=workflow%3Abuild+event%3Apush+branch%3Amain)
[![codecov](https://codecov.io/gh/oras-project/oras-go/branch/main/graph/badge.svg)](https://codecov.io/gh/oras-project/oras-go)
[![Go Report Card](https://goreportcard.com/badge/oras.land/oras-go/v2)](https://goreportcard.com/report/oras.land/oras-go/v2)
[![Go Reference](https://pkg.go.dev/badge/oras.land/oras-go/v2.svg)](https://pkg.go.dev/oras.land/oras-go/v2)

The version `2` is actively developed in the [`main`](https://github.com/oras-project/oras-go/tree/main) branch with all new features.

> [!Note]
> The `main` branch follows [Go's Security Policy](https://github.com/golang/go/security/policy) and supports the two latest versions of Go (currently `1.23` and `1.24`).

#### Usage Examples

Common operations with ORAS Go:

- [Copy examples](https://pkg.go.dev/oras.land/oras-go/v2#pkg-examples)
- [Registry interaction examples](https://pkg.go.dev/oras.land/oras-go/v2/registry#pkg-examples)
- [Repository interaction examples](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote#pkg-examples)
- [Authentication examples](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote/auth#pkg-examples)

If you are seeking latest changes, you should use the [`main`](https://github.com/oras-project/oras-go/tree/main) branch (or a specific commit hash) over a tagged version when including the ORAS Go library in your project's `go.mod`.
The Go Reference for the `main` branch is available [here](https://pkg.go.dev/oras.land/oras-go/v2@main).

To migrate from `v1` to `v2`, see [MIGRATION_GUIDE.md](./MIGRATION_GUIDE.md).

### v1: mantainance only

[![Build Status](https://github.com/oras-project/oras-go/actions/workflows/build.yml/badge.svg?event=push&branch=v1)](https://github.com/oras-project/oras-go/actions/workflows/build.yml?query=workflow%3Abuild+event%3Apush+branch%3Av1)
[![Go Report Card](https://goreportcard.com/badge/oras.land/oras-go)](https://goreportcard.com/report/oras.land/oras-go)
[![Go Reference](https://pkg.go.dev/badge/oras.land/oras-go.svg)](https://pkg.go.dev/oras.land/oras-go)

As there are various stable projects depending on the ORAS Go library `v1`, the
[`v1`](https://github.com/oras-project/oras-go/tree/v1) branch
is maintained for API stability, dependency updates, and security patches.
All `v1.*` releases are based upon this branch.

Since `v1` is in a maintenance state, you are highly encouraged
to use releases with major version `2` for new features.

## Documentation

- [Project Documentation](./docs/README.md): Technical documentation for `oras-go` v2
- [ORAS Website](https://oras.land/docs/Client_Libraries/go): Official ORAS website

## Community

- Slack channel: [#oras](https://cloud-native.slack.com/archives/CJ1KHJM5Z)
- [Reviewing Guide](https://github.com/oras-project/community/blob/main/REVIEWING.md): Guidelines for project reviews
- [Code of Conduct](CODE_OF_CONDUCT.md): This project follows the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md)
