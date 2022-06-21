## Migration Guide
ORAS Go v2 completely re-implements the whole library, in order to make it:

- Simpler to use
- Easier to maintain
- Well tested
- Well documented

As a result, most APIs are re-designed in v2. The detailed documentation and examples can be found in the [GoDoc](https://pkg.go.dev/oras.land/oras-go/v2).

### Migrating from v1 to v2
Starting from v2, the import path will be:
```
"oras.land/oras-go/v2"
```

Basically, you would need to run:

```
go get oras.land/oras-go/v2
go mod tidy
```

### Major Changes in v2

- `content.FileStore` now becomes [file.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/file#Store)
- `content.OCIStore` now becomes [oci.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/oci#Store)
- `content.MemoryStore` now becomes [memory.Store](https://pkg.go.dev/oras.land/oras-go/v2/content/memory#Store)
- Supports [remote registry](https://pkg.go.dev/oras.land/oras-go/v2/registry/remote) APIs
- Supports [Copy](https://pkg.go.dev/oras.land/oras-go/v2#Copy) with more flexible options
- Supports [Extended Copy](https://pkg.go.dev/oras.land/oras-go/v2#ExtendedCopy) with options