# Migration Guide

ORAS Go library has been completely rewritten and has become:

- simpler to use
- easier to maintain
- well tested
- well documented

As there are breaking changes of APIs, the version `v2` is introduced.
Documentation and examples of the `v2` APIs can be found at [pkg.go.dev](https://pkg.go.dev/oras.land/oras-go/v2).

## Migrating to `v2`

The import path of `v2` is:
```
"oras.land/oras-go/v2"
```

Basically, you would run:

```
go get oras.land/oras-go/v2
go mod tidy
```

## Major Changes in `v2`

- `content.FileStore` now becomes [file.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/file#Store)
- `content.OCIStore` now becomes [oci.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/oci#Store)
- `content.MemoryStore` now becomes [memory.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/memory#Store)
- Supports [remote registry](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote) APIs
- Supports [Copy](https://pkg.go.dev/oras.land/oras-go/v2#Copy) with more flexible options
- Supports [Extended Copy](https://pkg.go.dev/oras.land/oras-go/v2#ExtendedCopy) with options (experimental)
