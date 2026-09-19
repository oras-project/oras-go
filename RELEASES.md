# Releasing oras-go

Releases are created via a GitOps workflow. Merging a `release/vX.Y.Z` branch
into `main` automatically tags the commit and publishes the GitHub Release.

## Steps

### 1. Create a release branch

The release branch needs at least one commit so GitHub will allow a PR to be
opened. Use an empty commit as a lightweight marker:

```bash
git fetch upstream
git checkout -b release/v3.0.0 upstream/main
git commit --allow-empty -s -m "chore: prepare release v3.0.0"
git push origin release/v3.0.0
```

The release does not need to contain the changes being released — those are
already on `main`. The PR is a trigger: when it merges, the workflow tags the
PR's `merge_commit_sha` (the exact commit that landed on `main`), which includes
all prior work on the branch.

### 2. Open a pull request

Open a PR from `release/v3.0.0` targeting the `main` branch. Write the release
notes directly in the PR description using the format from prior releases:

```markdown
## New Features
...

## Bug Fixes
...

## Documentation
...

## Other Changes
...
```

The PR description becomes the GitHub Release body verbatim, so write it in
its final form.

### 3. Get approvals

The number of approvals required from the owners listed in
[OWNERS.md](OWNERS.md) depends on the kind of release. The author counts
toward the total, so an owner-authored release PR needs one fewer review than
the number below, and no release can be approved by its author alone:

| Release type | Owner approvals (author included) |
| --- | --- |
| Patch (`vX.Y.Z` → `vX.Y.Z+1`) | 2 owners |
| Minor (`vX.Y.Z` → `vX.Y+1.0`) | 2 owners |
| Major (`vX.Y.Z` → `vX+1.0.0`) | super majority of owners, more than two thirds (3 of the 4 current owners) |

These tiers are project policy, not an enforced gate: GitHub branch protection
supports only a single fixed approval count, so it cannot express
per-release-type requirements.

Reviewers should verify:

- The target commit is correct
- The release notes are accurate and complete
- All CI checks pass

### 4. Merge

Merge the PR. The [release workflow](.github/workflows/release.yml)
automatically:

1. Extracts the version from the branch name (`release/v3.0.0` → `v3.0.0`)
2. Creates and pushes the git tag
3. Publishes the GitHub Release with the PR body as release notes

## Pre-releases

Tags containing `-alpha`, `-beta`, or `-rc` (e.g., `v3.0.0-rc.1`) are
automatically marked as pre-release on GitHub. Use the same branch naming
convention: `release/v3.0.0-rc.1`.

## Testing the workflow locally

Three levels of local validation are available without triggering a real release:

**1. Validate the GoReleaser config:**
```bash
goreleaser check
```

**2. Validate workflow structure and job matching (dry run):**
```bash
act pull_request \
  -e .github/act/release-event.json \
  -W .github/workflows/release.yml \
  -n
```

**3. Run the workflow end-to-end with a fake token (Colima + cached actions required):**
```bash
act pull_request \
  -e .github/act/release-event.json \
  -W .github/workflows/release.yml \
  -s GITHUB_TOKEN=fake \
  --pull=false \
  --action-offline-mode \
  --container-daemon-socket -
```

This runs all steps up to and including version extraction (`version=vX.Y.Z` will
appear in the output). The `git push` step then fails with a permission error —
that is expected and confirms no tag was pushed. The mock event payload is at
`.github/act/release-event.json`.

## Updating the documentation site

After a release, update [oras-www](https://github.com/oras-project/oras-www)
to reflect the new version. See the `CLAUDE.md` in that repository for the
exact steps.
