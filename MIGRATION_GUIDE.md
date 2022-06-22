# Migration Guide

In version `v2`, ORAS Go library has been completely rewritten with:

- More unified interfaces
- Fewer dependencies
- Higher test coverage
- Better documentation

Documentation and examples can be found at [pkg.go.dev](https://pkg.go.dev/oras.land/oras-go/v2).

## Migrating from `v1` to `v2`

The import path of `v2` is:
```
"oras.land/oras-go/v2"
```

Basically, you would run:

```
go get oras.land/oras-go/v2
go mod tidy
```

Since breaking changes are introduced in `v2`, code refactoring is required for migrating from `v1` to `v2`.  
However, you can still keep `v1` while using `v2`.

## Major Changes in `v2`

- Moves `content.FileStore` to [file.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/file#Store)
- Moves `content.OCIStore` to [oci.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/oci#Store)
- Moves `content.MemoryStore` to [memory.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/memory#Store)
- Supports [Copy](https://pkg.go.dev/oras.land/oras-go/v2#Copy) with more flexible options
- Supports [Extended Copy](https://pkg.go.dev/oras.land/oras-go/v2#ExtendedCopy) with options (experimental)
- Supports [remote registry](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote) APIs
- No longer supports `docker.Login` and `docker.Logout` (removes the dependency on `docker`); instead, provides authentication through [auth.Client](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote/auth#Client)
