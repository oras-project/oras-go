# ORAS Go library

[![GitHub Actions status](https://github.com/oras-project/oras-go/workflows/build/badge.svg)](https://github.com/oras-project/oras-go/actions/workflows/build.yml?query=workflow%3Abuild)
[![Go Report Card](https://goreportcard.com/badge/oras.land/oras-go)](https://goreportcard.com/report/oras.land/oras-go)
[![GoDoc](https://godoc.org/github.com/oras.land?status.svg)](https://godoc.org/oras.land/oras-go)

![ORAS](https://github.com/oras-project/oras-www/raw/main/docs/assets/images/oras.png)

## Project status
### Versioning

The ORAS Go library follows [Semantic Versioning](https://semver.org/), where breaking changes are reserved for MAJOR releases, and MINOR and PATCH releases must be 100% backwards compatible.

### v1: stable

[![GoDoc](https://godoc.org/github.com/oras.land?status.svg)](https://godoc.org/oras.land/oras-go)

As there are various stable projects depending on the ORAS Go library, the
[`v1`](https://github.com/oras-project/oras-go/tree/v1) branch
is maintained for API stability, dependency updates, and security patches.
All `v1.*` releases are based upon this branch.

If you are seeking stability over new features, you are highly encouraged
to use releases with major version `1`.

### v2: experimental

[![GoDoc](https://godoc.org/github.com/oras.land?status.svg)](https://godoc.org/oras.land/oras-go/v2)

In contrast to the `v1` branch, the
[`main`](https://github.com/oras-project/oras-go/tree/main) branch
is where all new feature development will occur. Since ORAS is a
primary staging ground for the
[ORAS Artifacts Specification](https://github.com/oras-project/artifacts-spec),
changes are expected to occur regularly to meet new requirements.
Any backward-incompatible changes will follow our [versioning policy](#versioning) and be reserved for the next major version of the library (`2`), which users may opt into.

If you are seeking new features over stability, you should use the
`main` branch (or a specific commit hash) when including the ORAS
Go library in your project's `go.mod`.

## Docs

Documentation for the ORAS Go library is located on
the project website: [oras.land/client_libraries/go](https://oras.land/client_libraries/go/)

## Code of Conduct

This project has adopted the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md). See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for further details.
