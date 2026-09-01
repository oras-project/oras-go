# Design: `signedBy` policy evaluation for tag references

Status: **proposal** — needs a decision before v3 GA.

## Problem

A `signedBy` policy requirement can never be satisfied when the image is
referenced by tag.

`DefaultSignedByVerifier.Verify` needs the manifest digest, because that is what
a simple-signing payload binds to (`critical.image.docker-manifest-digest`). It
obtains one via `parseImageDigest`, which scans the reference for `@` and errors
out if there is none:

```go
func parseImageDigest(ref string) (digest.Digest, error) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '@' { /* ... */ }
	}
	return "", fmt.Errorf("reference %s does not contain a digest", ref)
}
```

But policy is evaluated *before* resolution. `Repository.Resolve` and
`Repository.FetchReference` call `checkPolicy(ctx, reference)` with whatever the
caller passed in — usually a tag:

```go
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := r.checkPolicy(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	// ...
}
```

So for `registry.example.com/app:v1` under a `signedBy` policy:
`checkPolicy` → `IsImageAllowed` → `evaluateSignedBy` → `Verify` →
`parseImageDigest` fails → `Verify` returns an error → the evaluator returns
`(false, err)` → the pull is denied. Every time, regardless of whether a
perfectly valid signature exists.

This fails closed, so it is not a security hole. It is a functional gap: the
feature is unusable for the most common way people name images.

## Why it is not a one-line fix

The obvious move — resolve the tag first, then evaluate policy — inverts the
current ordering, and the ordering exists for a reason: policy should be able to
reject a request *before* the client talks to the registry about it. Scope- and
transport-level requirements (`reject`, `insecureAcceptAnything`) are meaningful
pre-flight; signature requirements inherently are not.

There is also a re-entrancy hazard. Resolving a tag inside the verifier means
calling back into `Repository.Resolve`, which calls `checkPolicy` again. The
`policyCheckedKey` context marker prevents infinite recursion but only if it is
threaded correctly through the verifier.

## How containers/image handles it

`containers/image` splits the decision in two. `PolicyContext.IsRunningImageAllowed`
operates on an `UnparsedImage` that has already been fetched far enough to know
its manifest digest, while reference-level rules are applied earlier against the
parsed reference. Signature requirements only ever see a resolved image.

## Options

### 1. Two-phase policy evaluation (recommended target)

Split requirements by what they need:

- **Pre-resolve**: `reject`, `insecureAcceptAnything`, and any future
  reference-shaped rule. Evaluated in `checkPolicy` as today.
- **Post-resolve**: `signedBy`, `sigstoreSigned`. Evaluated after the descriptor
  is known, against `ImageReference` carrying the resolved digest.

Sketch:

```go
func (r *Repository) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if err := r.checkPolicyPreResolve(ctx, reference); err != nil {
		return ocispec.Descriptor{}, err
	}
	desc, err := r.Manifests().Resolve(withPolicyChecked(ctx), reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := r.checkPolicyPostResolve(ctx, reference, desc); err != nil {
		return ocispec.Descriptor{}, err
	}
	return desc, nil
}
```

Cost: the `Evaluator` needs to partition requirements and expose two entry
points, and every mutating/reading path in `repository.go` needs the second
call sited correctly. `Fetch` already has a digest, so it feeds the post-resolve
phase directly.

Risk to watch: a requirement set containing *only* post-resolve requirements
must not let a pre-resolve pass be mistaken for an allow. The partition must
track that at least one phase actually evaluated the requirement.

### 2. Verifier resolves the tag itself

Give `DefaultSignedByVerifier` a resolver and have it turn a tag into a digest
on demand.

Cheaper — no change to the evaluator's shape — but it puts a network call inside
a policy verifier, needs `withPolicyChecked` threaded through to avoid
re-entering policy, and means the digest the signature is checked against is
fetched by a different code path than the one that will actually pull the
content. That last point is a TOCTOU seam: the tag could move between the
verifier's resolve and the caller's.

### 3. Document `signedBy` as digest-only

Make `parseImageDigest`'s failure an explicit, documented limitation, and have
`Verify` return a clearly-worded error naming the constraint.

Cheapest and honest, but it substantially reduces the feature's value — most
users pull by tag.

## Recommendation

Target **option 1**. It matches containers/image semantics, avoids the TOCTOU
seam in option 2, and is the only option under which `signedBy` is actually
usable as specified.

Ship **option 3** as the interim state in the meantime: an explicit error and a
documented limitation are much better than the current behaviour, where a
correctly-signed image pulled by tag is denied with a message about digest
parsing.

Shipping v3 GA with `signedBy` silently unusable for tag references is the
outcome to avoid.
