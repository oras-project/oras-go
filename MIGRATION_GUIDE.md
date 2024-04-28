# Migration Guide

In version `v2`, ORAS Go library has been completely refreshed with:

- More unified interfaces
- Notably fewer dependencies
- Higher test coverage
- Better documentation

**Besides, ORAS Go `v2` is now a registry client.**

## Major Changes in `v2`

- Content store
  - [`content.File`](https://pkg.go.dev/oras.land/oras-go/pkg/content#File) is now [`file.Store`](https://pkg.go.dev/oras.land/oras-go/v2/content/file#Store)
  - [`content.OCI`](https://pkg.go.dev/oras.land/oras-go/pkg/content#OCI) is now [`oci.Store`](https://pkg.go.dev/oras.land/oras-go/v2/content/oci#Store)
  - [`content.Memory`](https://pkg.go.dev/oras.land/oras-go/pkg/content#Memory) is now [`memory.Store`](https://pkg.go.dev/oras.land/oras-go/v2/content/memory#Store)
- Registry interaction
  - Provides an [SDK](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote) to interact with both OCI-compliant and Docker-compliant registries
- Authentication
  - Provides authentication through authentication through [`auth.Client`](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote/auth#Client) and provides credential management through [`credentials`](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote/credentials)
- Copy operations
  - Supports [copying](https://pkg.go.dev/oras.land/oras-go/v2#Copy) artifacts between various [`Target`](https://pkg.go.dev/oras.land/oras-go/v2#Target) with more flexible options
  - Supports [extended-copying](https://pkg.go.dev/oras.land/oras-go/v2#ExtendedCopy) artifacts and their predecessors (e.g. referrers) with options *(experimental)*

## Migrating from `v1` to `v2`

1. Update Go dependencies
   - Get the `v2` package

    ```sh
    go get oras.land/oras-go/v2
    ```

   - Import the `v2` package and use it in your code

    ```go
    import "oras.land/oras-go/v2"
    ```

   - Run

    ```sh
    go mod tidy
    ```

2. Code Refactoring  
   - Since breaking changes are introduced in `v2`, code refactoring is required for migrating from `v1` to `v2`.
   - The migration can be done in an iterative fashion, as `v1` and `v2` can be imported and used at the same time.  
   - Please refer to [pkg.go.dev](https://pkg.go.dev/oras.land/oras-go/v2) for detailed documentation and examples.

## Community Support

If you encounter challenges during migration, seek assistance from the community by [filing GitHub issues](https://github.com/oras-project/oras-go/issues/new) or asking in the [#oras](https://cloud-native.slack.com/archives/CJ1KHJM5Z) Slack channel.
