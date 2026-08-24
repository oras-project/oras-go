# Design: decoupling `credentials` from `config` without a global

Status: **proposal** — recommend settling before the v3 API freeze.

## Problem

`credentials.NewStore` depends on a package-level mutable variable that a
*different* package populates as an import side effect.

`registry/remote/credentials/store.go`:

```go
// defaultConfigLoader is set by the config package during init.
var defaultConfigLoader ConfigFileLoader

var ErrNoConfigLoader = fmt.Errorf("no config loader registered; import the config package or use NewStoreFromConfig")

func SetDefaultConfigLoader(loader ConfigFileLoader) {
	defaultConfigLoader = loader
}

func NewStore(configPath string, opts StoreOptions) (*DynamicStore, error) {
	if defaultConfigLoader == nil {
		return nil, ErrNoConfigLoader
	}
	// ...
}
```

`registry/remote/config/config.go`:

```go
func init() {
	credentials.SetDefaultConfigLoader(func(configPath string) (credentials.ConfigFile, error) { /* ... */ })
}
```

Three consequences:

1. **Action at a distance.** Whether `credentials.NewStore` works depends on
   whether some *other* package was linked in. A caller who imports only
   `credentials` gets `ErrNoConfigLoader` at runtime, and the fix is to add an
   import they do not otherwise use — the kind of thing that gets deleted by a
   later "remove unused import" cleanup and fails in production.
2. **Unsynchronized global.** `SetDefaultConfigLoader` writes a package variable
   with no mutex. Set from `init()` it is safe, but the function is *exported*,
   so any caller can rewrite it at any time from any goroutine. That is a data
   race by construction, and it is racy across the whole process — one library
   swapping the loader changes behaviour for every other consumer.
3. **A ten-method interface exists only to break an import cycle.**
   `credentials.ConfigFile` mirrors `config.Config` (`GetAuthConfig`,
   `GetAuthConfigHierarchical`, `PutAuthConfig`, `DeleteAuthConfig`,
   `GetCredentialHelper`, `CredentialsStore`, `SetCredentialsStore`,
   `IsAuthConfigured`, `Path`, `Save`). Every change to config file handling now
   has to be made in two places that must stay in sync.

The underlying cause is just an import cycle: `config` needs `credentials` for
`Credential`/`AuthConfig` types, and `credentials` needs `config` to load a
config file.

## Proposal

Move the shared types into an internal package that both can import, so the
cycle disappears and the global is unnecessary.

```
registry/remote/internal/configfile/    (new)
    authconfig.go   — AuthConfig, DecodeAuth
    configfile.go   — the ConfigFile interface, Load
```

- `credentials` imports `configfile` for the interface and loader.
- `config` imports `configfile` for the same types and provides the concrete
  implementation.
- Neither imports the other. `init()`, `SetDefaultConfigLoader`,
  `ConfigFileLoader`, and `ErrNoConfigLoader` are all deleted.

`credentials.NewStore` becomes:

```go
func NewStore(configPath string, opts StoreOptions) (*DynamicStore, error) {
	cfg, err := configfile.Load(configPath)
	if err != nil {
		return nil, err
	}
	return NewStoreFromConfig(cfg, opts), nil
}
```

which works with no phantom import and no ordering dependency.

`credentials.AuthConfig` and `credentials.ConfigFile` stay as thin aliases of the
internal types so the public API does not move:

```go
type AuthConfig = configfile.AuthConfig
type ConfigFile = configfile.ConfigFile
```

## Alternatives considered

**Keep the global, add a mutex.** Fixes the race but not the action-at-a-distance
or the duplicated interface. It also leaves an exported process-wide setter,
which is the more consequential design problem.

**Merge `config` and `credentials` into one package.** Removes the cycle
outright, but the two have genuinely different audiences — plenty of callers want
credential resolution without containers-registries.d parsing — and it would be a
much larger public API change.

**Have `config` own everything and make `credentials` config-free.** Arguably the
cleanest long-term shape, but it inverts the existing dependency direction and
would move `Store`, `DynamicStore`, and the native-helper handling. Too large for
the current window.

## Migration impact

- `SetDefaultConfigLoader`, `ConfigFileLoader`, `ErrNoConfigLoader`: removed.
  These exist only in v3 pre-release, so no released API is affected.
- `credentials.NewStore`: unchanged signature, no longer requires the caller to
  import `config`. This is strictly a relaxation — code that imported `config`
  keeps working.
- `credentials.AuthConfig`, `credentials.ConfigFile`: unchanged as far as callers
  are concerned (type aliases).

## Recommendation

Do this before the v3 API freeze. Afterwards, removing an exported setter and
an exported error becomes a breaking change, and the two-copy interface has to
be maintained for the life of v3.
