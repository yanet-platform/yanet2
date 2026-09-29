# Versioned releases

A release line is `release/X.Y`. Keep `main` moving and merge fixes there first.
Backport only selected commits to the release line through pull requests. Do not
use `release/alpha` for a versioned release. The release branch remains in place
for later patches. Merge each backport only after its existing PR CI passes.

## Repository rulesets

An administrator must configure these GitHub settings before the first release:

- Protect `release/*` branches, excluding `release/alpha`: require pull requests,
  require Commit Lint and Agents Lint, which run on every PR, and block force
  pushes and deletion. Check other CI results before merging backports.
  The release flow adds no separate branch gate or CI-run lookup.
- Protect `v*` tags: restrict creation to release maintainers and block updates
  and deletion. The tag is the human promotion action. It must point to a commit
  on the first-parent history of its matching release line.
- Give Actions permission to publish packages to GHCR and create GitHub
  Releases. The workflows request `packages: write` and `contents: write` only
  where needed. Confirm repository policy allows those permissions.

These rulesets are repository settings; the workflow files cannot install them.
No staging signoff is required by this flow.

## Open a release line and backport

For an optional candidate, create `release/X.Y` from a tested commit on `main`
before running it. Otherwise, the first stable tag creates the missing branch
at the tagged commit, which must be on `main`'s first-parent history. A later
patch tag can recreate a missing branch only when its commit descends from the
previous published patch on its first-parent history. A historical tag does
not recreate the branch after a newer tag exists. Keep the branch for
future patches. Open a pull request for each selected backport. Cherry-pick
from `main` with `git cherry-pick -x <commit>` on a branch based on
`release/X.Y`, then target that release branch with the pull request. Merge it
only after the existing CI checks pass.

## Build a candidate (optional)

To preview a release, run **Release Candidate** in GitHub Actions on the
`release/X.Y` branch and enter the target stable version `X.Y.Z`. The first
release on a line is `X.Y.0`; later versions require the immediately preceding
patch tag on the same ancestry. Optionally enable the Ubuntu 22.04 amd64
validation build.

Preflight rejects an invalid branch or version, a commit outside the branch's
first-parent history, and a missing predecessor or stale patch tag. The run
builds Ubuntu 24.04 amd64 and arm64 packages at `X.Y.Z~rcN`, verifies both
sets and their exact version, then builds and publishes all seven GHCR images
with the unique `X.Y.Z-rcN` tag.
`N` incorporates the GitHub run ID and attempt, so a rerun gets a new tag.
The optional Ubuntu 22.04 set is validated but is not included in release
assets. Download the candidate DEBs from the run artifacts if deployment
validation is desired. Candidate builds do not gate publication.

## Publish a version

Create `vX.Y.Z` at the intended commit and push the tag. The first tag may
point to a tested commit on `main` if the release branch does not exist yet;
later tags normally point to commits on the release branch.
Automatic branch creation requires a tagged commit that contains this workflow.
For an older commit, create the release branch manually before pushing the tag.
If you built a candidate, use its exact commit. For example:

```bash
git fetch origin --tags
git tag vX.Y.Z <release-commit-sha>
git push origin refs/tags/vX.Y.Z
```

The **Publish Release** workflow checks that the tag matches the branch and
commit and that the patch order is increasing. It builds the DEBs at final
version `X.Y.Z`. If a candidate was built, its DEBs have a different version;
the SHA links the two builds.
After both 24.04 architecture sets pass artifact and exact-version checks, it
builds each of the seven images once under a unique staging tag. Once all
seven pushes succeed, it copies those exact image manifests to the `X.Y.Z`
tags and creates the GitHub Release with the verified DEBs. The workflow
refuses to replace an existing final tag with a different digest. There are
no moving `X.Y`, `X`, or `latest` aliases. A failed preflight publishes nothing.
The GitHub Release body names the source SHA; it does not generate notes from
the previous global release, which may belong to another release line.

Review [upgrade notes](upgrade.md) before rolling out packages. Versioned
releases are not created from `release/alpha`, and candidate builds do not
create Git tags or GitHub prereleases.

## Patches and retries

For a patch, merge the fix to `main`, backport it to the retained release line,
wait for the backport PR's CI, optionally build a candidate for the next patch
number, and tag the release branch commit after the previous publication
succeeds. The previous `vX.Y.(Z-1)` tag must be an ancestor of `vX.Y.Z`; a
missing or unpublished predecessor blocks stable publication. A newer tag
blocks an older candidate.

Rerun a failed candidate after fixing its cause. Every run attempt uses a new
candidate version and image tag. A failed candidate can leave partial images
under its own tag; use only tags from successful runs.

If a stable run fails, rerun the workflow for the same protected tag. A failed
build can leave partial staging images, but no final image tags. A failed
promotion can leave some final `X.Y.Z` tags without a GitHub Release. A retry
uses new staging tags and accepts an existing final tag only when its digest
matches the new staged image. If the digest differs, promotion stops rather
than moving the final tag; a maintainer must investigate the differing build
and complete the release without changing published tags. Existing GitHub
Release assets must also match the rebuilt files by SHA-256; missing assets
are added on retry. Promotion is not atomic, so consumers should wait for
the GitHub Release before using a new version. Keep the Git tag fixed at the
release SHA.
