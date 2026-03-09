# AI Migration Prompt: ORAS Go v2 → v3

Copy and paste the prompt below into an AI assistant (Claude, Copilot, etc.) along with the source files you want to migrate.

---

## The Prompt

```
You are migrating Go code from the ORAS Go v2 library (module path: oras.land/oras-go/v2)
to the ORAS Go v3 library (module path: github.com/oras-project/oras-go/v3).

Apply ALL of the changes described below. Do not change behavior, only update the API
surface to match v3. After migrating, run `go build ./...` mentally and flag any
remaining compilation errors.

────────────────────────────────────────────────────────────
1. MODULE PATH — update every import path
────────────────────────────────────────────────────────────

Replace ALL occurrences:
  oras.land/oras-go/v2  →  github.com/oras-project/oras-go/v3

Examples:
  "oras.land/oras-go/v2"                       → "github.com/oras-project/oras-go/v3"
  "oras.land/oras-go/v2/registry/remote"       → "github.com/oras-project/oras-go/v3/registry/remote"
  "oras.land/oras-go/v2/registry/remote/auth"  → "github.com/oras-project/oras-go/v3/registry/remote/auth"

────────────────────────────────────────────────────────────
2. CREDENTIAL TYPE — moved from auth to credentials package
────────────────────────────────────────────────────────────

Add import: "github.com/oras-project/oras-go/v3/registry/remote/credentials"

Search and replace (whole-word):
  auth.Credential{         → credentials.Credential{
  auth.EmptyCredential     → credentials.EmptyCredential
  auth.CredentialFunc      → credentials.CredentialFunc
  auth.StaticCredential(   → credentials.StaticCredentialFunc(

If code checks for empty credentials, use the new method:
  cred == auth.EmptyCredential  →  cred.IsEmpty()

────────────────────────────────────────────────────────────
3. AUTH CLIENT FIELD RENAMES
────────────────────────────────────────────────────────────

On auth.Client:
  .Credential =   →  .CredentialFunc =   (field renamed)

OAuth2 flag (SEMANTICS INVERTED):
  ForceAttemptOAuth2 = true   →  remove (OAuth2 is now the default)
  ForceAttemptOAuth2 = false  →  client.SetLegacyMode(true)

────────────────────────────────────────────────────────────
4. REPOSITORY / REGISTRY STRUCT — fields moved to Registry
────────────────────────────────────────────────────────────

These fields no longer exist directly on Repository; access via Repository.Registry:
  repo.Client =         →  repo.Registry.Client =
  repo.PlainHTTP =      →  repo.Registry.PlainHTTP =
  repo.HandleWarning =  →  repo.Registry.HandleWarning =
  repo.Policy =         →  repo.Registry.Policy =

Reference field access:
  repo.Reference.Registry     →  repo.Registry.Reference.Registry
  repo.Reference.Repository   →  repo.RepositoryName

RepositoryOptions type was removed — create Repository directly or via Registry.Repository().

For multiple repos sharing config, prefer:
  reg, _ := remote.NewRegistry("registry.example.com")
  reg.Client = myClient
  repo1, _ := reg.Repository(ctx, "repo1")
  repo2, _ := reg.Repository(ctx, "repo2")

────────────────────────────────────────────────────────────
5. REFERENCE STRUCT — Tag and Digest fields added
────────────────────────────────────────────────────────────

The Reference.Reference field is deprecated. Use:
  ref.Reference   →  ref.GetReference()   (or ref.Tag / ref.Digest directly)
  ref.Reference == ""  →  ref.GetReference() == ""

ParseReference now strips URI schemes automatically (oci://, http://, https://).

ParseReferenceList is new — use it for comma-separated refs:
  registry.ParseReferenceList("ghcr.io/repo:v1,v2,v3")

────────────────────────────────────────────────────────────
6. USING ClientBuilder (new, recommended for new code)
────────────────────────────────────────────────────────────

Instead of manually constructing auth.Client and setting fields on Repository,
the v3-idiomatic way is:

  import (
      "github.com/oras-project/oras-go/v3/registry/remote"
      "github.com/oras-project/oras-go/v3/registry/remote/properties"
  )

  builder := remote.NewClientBuilder()
  builder.CredentialStore = myStore   // credentials.Store
  builder.UserAgent = "my-app/1.0"

  props, _ := properties.NewRegistry("registry.example.com/myrepo:v1")
  repo, _ := remote.NewRepositoryWithProperties(props, builder)

ClientBuilder fields:
  BaseTransport    http.RoundTripper        // default: http.DefaultTransport
  RetryPolicy      func() retry.Policy      // default: retry.DefaultPolicy
  CacheFactory     func(string) auth.Cache  // default: per-registry auth.NewCache()
  CredentialStore  credentials.Store
  UserAgent        string
  TokenFetcher     auth.TokenFetcher        // optional custom token fetcher
  PolicyEvaluator  *policy.Evaluator        // optional; enforces policy on all ops
  Logger           *slog.Logger             // optional debug logging

────────────────────────────────────────────────────────────
7. POLICY ENFORCEMENT (new package: registry/remote/policy)
────────────────────────────────────────────────────────────

If your code needs to enforce containers-policy.json allow/deny decisions:

  import (
      "github.com/oras-project/oras-go/v3/registry/remote/policy"
      "github.com/oras-project/oras-go/v3/registry/remote/signature"
  )

  // Build a policy
  pol := policy.NewPolicy().SetDefault(&policy.PRSignedBy{
      KeyType: "GPGKeys",
      KeyPath: "/path/to/pubkey.gpg",
  })

  // Or load from file via config package (see section 9)

  // Create evaluator (optionally with a signature verifier)
  store := signature.NewLookasideStore(readURL, writeURL)
  verifier := signature.NewSignedByVerifier(store)
  evaluator, err := policy.NewEvaluator(pol, policy.WithSignedByVerifier(verifier))

  // Check access
  allowed, err := evaluator.IsImageAllowed(ctx, policy.ImageReference{
      Transport: policy.TransportNameDocker,
      Scope:     "registry.example.com/myrepo",
      Reference: "registry.example.com/myrepo@sha256:abc...",
  })

  // Apply to repository via middleware
  enforced := remote.WithPolicyEnforcement(evaluator, policy.TransportNameDocker, scope)(repo)

  // Or apply at build time via ClientBuilder
  builder.PolicyEvaluator = evaluator
  repo, _ := remote.NewRepositoryWithProperties(props, builder)

────────────────────────────────────────────────────────────
8. SIGNATURE VERIFICATION (new package: registry/remote/signature)
────────────────────────────────────────────────────────────

The signature package implements the atomic container signatures format
(compatible with Podman/Skopeo lookaside signatures).

  import "github.com/oras-project/oras-go/v3/registry/remote/signature"

  // Lookaside store (file:// for local; https:// for remote)
  store := signature.NewLookasideStore("file:///var/lib/containers/sigstore", "")

  // Create a verifier for a PRSignedBy policy requirement
  verifier := signature.NewSignedByVerifier(store)

  // Sign an image (for testing or publishing)
  payload := signature.NewSimpleSigningPayload(desc.Digest, "registry.example.com/repo:v1")
  payloadBytes, _ := payload.Marshal()
  sigData, _ := signature.CreateOpenPGPSignature(payloadBytes, gpgEntity)
  store.PutSignature(ctx, "registry.example.com/repo", desc.Digest, sigData)

  // From registries.d config:
  verifier := signature.NewSignedByVerifierFromConfig(cfgs.RegistriesDConfig, scope)

────────────────────────────────────────────────────────────
9. CONFIG LOADING (updated package: registry/remote/config)
────────────────────────────────────────────────────────────

The config package loads Docker config.json, registries.conf, policy.json, and
certificates from system paths or custom paths.

  import "github.com/oras-project/oras-go/v3/registry/remote/config"

  // Load from system defaults
  cfgs, err := config.LoadConfigs()

  // Or load from custom paths
  cfgs, err := config.LoadConfigsWithOptions(config.LoadConfigsOptions{
      PolicyConfigPath:  "/path/to/policy.json",
      RegistriesDPath:   "/path/to/registries.d/",
      DockerConfigPath:  "/path/to/config.json",
  })

  // Create evaluator from loaded policy
  evaluator, err := cfgs.PolicyEvaluator()                         // no signature verification
  evaluator, err := cfgs.PolicyEvaluator(policy.WithSignedByVerifier(v)) // with verifier

  // Create verifier from registries.d (longest-prefix match on scope)
  verifier := signature.NewSignedByVerifierFromConfig(cfgs.RegistriesDConfig, scope)

  // Create credential store from loaded Docker config
  store := credentials.NewStoreFromConfig(cfgs.DockerConfig)
  builder.CredentialStore = store

────────────────────────────────────────────────────────────
10. CONTENT CACHE (new package: content/cache)
────────────────────────────────────────────────────────────

If you need a caching layer between a fast store and a slow remote:

  import "github.com/oras-project/oras-go/v3/content/cache"

  // Wrap a remote target with a local cache
  cached := cache.New(remoteTarget, localStore)
  // Read ops check localStore first; misses fetch from remoteTarget and populate cache.
  // Write ops go to both.

────────────────────────────────────────────────────────────
11. OBJECTS PACKAGE (new: objects/)
────────────────────────────────────────────────────────────

The objects package provides an ORM-like API for working with OCI images,
artifacts, and image indexes. It is entirely additive — no v2 code needs to
be rewritten to use it — but it may replace verbose push/pull patterns.

  import (
      "github.com/oras-project/oras-go/v3/objects"
      "github.com/oras-project/oras-go/v3/objects/builders"
  )

  client := objects.NewClient(repo)

  // Fetch an image (lazy — fields not populated until Load() is called)
  img, err := client.FetchByReference(ctx, "registry.example.com/app:v1")
  if err := img.Load(ctx); err != nil { ... }
  fmt.Println(img.MediaType(), img.Digest(), img.Annotations())

  // Build and push an image
  image, err := builders.NewImageBuilder().
      AddLayer(layerDesc, layerData).
      SetConfig(configDesc, configData).
      Build()
  desc, err := client.Push(ctx, image, "v1")

────────────────────────────────────────────────────────────
12. REPOSITORY MIDDLEWARE (new)
────────────────────────────────────────────────────────────

Use middleware to add cross-cutting concerns without subclassing:

  import "github.com/oras-project/oras-go/v3/registry/remote"

  // Single middleware
  enforced := remote.WithPolicyEnforcement(evaluator, policy.TransportNameDocker, scope)(repo)

  // Compose multiple middlewares (first = outermost)
  wrapped := remote.Compose(
      remote.WithPolicyEnforcement(evaluator, policy.TransportNameDocker, scope),
      myLoggingMiddleware,
  )(repo)

────────────────────────────────────────────────────────────
13. MIRROR SUPPORT (new)
────────────────────────────────────────────────────────────

Mirrors are now first-class via properties.Mirror. When configured,
read operations (Resolve, Fetch, FetchReference, Exists) try mirrors in
order before falling back to the primary registry.

  props.Mirrors = []properties.Mirror{
      {
          Location:       "mirror.example.com",
          PullFromMirror: remote.PullFromMirrorAll,      // "all", "digest-only", or "tag-only"
          Transport:      properties.Transport{Insecure: true},
      },
  }

Mirrors are also populated automatically when loading registries.conf via
config.LoadConfigs() / the registries.conf bridge.

────────────────────────────────────────────────────────────
SEARCH-AND-REPLACE CHEAT SHEET
────────────────────────────────────────────────────────────

| Find                                | Replace                                        |
|-------------------------------------|------------------------------------------------|
| oras.land/oras-go/v2                | github.com/oras-project/oras-go/v3             |
| auth.Credential{                    | credentials.Credential{                        |
| auth.EmptyCredential                | credentials.EmptyCredential                    |
| auth.CredentialFunc                 | credentials.CredentialFunc                     |
| auth.StaticCredential(              | credentials.StaticCredentialFunc(              |
| .Credential = (on auth.Client)      | .CredentialFunc =                              |
| ForceAttemptOAuth2 = true           | (remove — OAuth2 is now default)               |
| ForceAttemptOAuth2 = false          | client.SetLegacyMode(true)                     |
| repo.Client =                       | repo.Registry.Client =                         |
| repo.PlainHTTP =                    | repo.Registry.PlainHTTP =                      |
| repo.HandleWarning =                | repo.Registry.HandleWarning =                  |
| repo.Policy =                       | repo.Registry.Policy =                         |
| repo.Reference.Registry             | repo.Registry.Reference.Registry               |
| repo.Reference.Repository           | repo.RepositoryName                            |
| ref.Reference (field read)          | ref.GetReference()                             |
| evaluator.Evaluate(                 | evaluator.IsImageAllowed(                      |

────────────────────────────────────────────────────────────
AFTER MIGRATION
────────────────────────────────────────────────────────────

1. Run: go mod tidy
2. Run: go build ./...
3. Run: go vet ./...
4. Fix any remaining type errors — most will be import paths or field renames
   not covered by the simple search-and-replace above.
```
