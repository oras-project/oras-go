## Module Path Change

**PR:** [#1051](https://github.com/oras-project/oras-go/pull/1051)

The module path has changed from `oras.land/oras-go/v2` to `github.com/oras-project/oras-go/v3`.

### Migration

Update all import statements in your code:

```go
// Before (v2)
import "oras.land/oras-go/v2"
import "oras.land/oras-go/v2/registry/remote"

// After (v3)
import "github.com/oras-project/oras-go/v3"
import "github.com/oras-project/oras-go/v3/registry/remote"
```

Update your `go.mod`:

```bash
go get github.com/oras-project/oras-go/v3
```

