# Versioned releases

A release line is `release/X.Y`. Keep `main` moving and merge fixes there first.
Tag a commit on `main` to include every intervening commit in the release line,
or backport only selected commits through pull requests. Do not use
`release/alpha` for a versioned release. Merge each backport only after its
existing PR CI passes.

## Repository rulesets

An administrator must configure these GitHub settings before the first release:

- Protect `release/*` branches, excluding `release/alpha`: require pull requests,
  require Commit Lint and Agents Lint, which run on every PR, and block force
  pushes and deletion. Add a write-enabled deploy key for this repository and
  allow deploy keys to bypass the ruleset. Store its private key as
  `RELEASE_BRANCH_DEPLOY_KEY` in the `release-branch-updater` environment, with
  access limited to branch `main` and tags `v*`. Release preflight uses the key
  to advance the branch to a validated tag. Check other CI results before
  merging backports. The release flow adds no separate branch gate or CI-run lookup.
- Protect `v*` tags: restrict creation to release maintainers and block updates
  and deletion. The tag is the human promotion action. It must point to a commit
  on the release line's first-parent history or advance that line from `main`.
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
previous published patch on its first-parent history. A historical tag can
recreate the branch only if every newer release tag descends from it. If the
branch exists but lags a tag on `main`, preflight fast-forwards it only when the
tip and previous release tag are on the tag's first-parent history. A divergent
branch blocks publication. Open a pull request for each selected backport.
Cherry-pick from `main` with `git cherry-pick -x <commit>` on a branch based on
`release/X.Y`, then target that release branch with the pull request.

## Build a candidate (optional)

To preview a release, run **Release Candidate** in GitHub Actions on the
`release/X.Y` branch and enter the target stable version `X.Y.Z`. The first
release on a line is `X.Y.0`; later versions require the immediately preceding
patch tag on the same ancestry. Optionally enable the Ubuntu 22.04 amd64
validation build.

Preflight rejects an invalid branch or version, a commit outside the branch's
first-parent history, and a missing predecessor or stale patch tag. After
preflight, the source-built neighbour-sidecar image builds and publishes with
the unique `X.Y.Z-rcN` tag while Ubuntu 24.04 amd64 and arm64 packages build at
`X.Y.Z~rcN`. After package verification and the sidecar build succeed, the six
package-backed GHCR images build and publish with the same tag. A successful
run builds each of the seven images once from the validated source commit.
`N` incorporates the GitHub run ID and attempt, so a rerun gets a new tag.
The optional Ubuntu 22.04 set is validated but is not included in release
assets. Download the candidate DEBs from the run artifacts if deployment
validation is desired. Candidate builds do not gate publication.

## Publish a version

Create `vX.Y.Z` at the intended commit and push the tag. The first tag may
point to a tested commit on `main` if the release branch does not exist yet;
later tags may point to the release branch or to a first-parent descendant of
its tip on `main`. A tag from `main` brings all commits between the branch tip
and the tag into the release line.
Automatic branch creation requires a tagged commit that contains this workflow.
For an older commit, create the release branch manually before pushing the tag.
If you built a candidate, use its exact commit. For example:

```bash
git fetch origin --tags
git tag vX.Y.Z <release-commit-sha>
git push origin refs/tags/vX.Y.Z
```

The **Publish Release** workflow checks that the tag matches the branch and
commit and that the patch order is increasing. It advances the branch before
building when a validated tag comes from `main`. It builds the DEBs at final
version `X.Y.Z`. If a candidate was built, its DEBs have a different version;
the SHA links the two builds.
After preflight, the source-built neighbour-sidecar image builds and pushes
under a unique staging tag while the DEBs build. After both 24.04 architecture
sets pass artifact and exact-version checks and the sidecar build succeeds,
the six package-backed images build and push under the same staging tag.
Each of the seven images builds once. Once all seven pushes succeed,
it copies those exact image manifests to the `X.Y.Z`
tags and creates the GitHub Release with the verified DEBs. The workflow
refuses to replace an existing final tag with a different digest. There are
no moving `X.Y`, `X`, or `latest` aliases. A failed preflight publishes nothing.
Releases made with this workflow list each final image, digest, manifest size
in bytes, and a `docker pull` command in the GitHub Release description. The
Release body names the source SHA; it does not generate notes from
the previous global release, which may belong to another release line.
The message of an annotated tag opens the description as the release notes.
Keep Markdown headings in it with `--cleanup=verbatim`, otherwise Git strips
lines starting with `#`:

```bash
git tag -a --cleanup=verbatim -F release-notes.md vX.Y.Z <release-commit-sha>
```

Review [upgrade notes](upgrade.md) before rolling out packages. Versioned
releases are not created from `release/alpha`, and candidate builds do not
create Git tags or GitHub prereleases.

## Patches and retries

For a patch, merge the fix to `main`. Tag a commit on `main` to bring the branch
forward automatically, or backport selected commits to the release branch,
wait for the backport PR's CI, optionally build a candidate for the next patch
number, and tag the branch commit after the previous publication succeeds.
The previous `vX.Y.(Z-1)` tag must be an ancestor of `vX.Y.Z`; a
missing or unpublished predecessor blocks stable publication. Every newer tag
must descend from the tag being published on its first-parent history.

Rerun a failed candidate after fixing its cause. Every run attempt uses a new
candidate version and image tag. A later package failure can leave the early
sidecar image under that candidate's tag. Other failures can also leave
partial images; use only tags from successful runs.

If a stable run fails, rerun the workflow for the same protected tag. A failed
tag run whose commit predates a workflow fix can instead use the updated
workflow on `main`: `gh workflow run release.yml --ref main -f tag=vX.Y.Z`.
The manual run uses the selected `main` revision's release helpers while
building from the unchanged tag commit. A later package failure can leave the
early sidecar staging image. A failed build can leave partial staging images,
but no final image tags. A failed promotion can leave some
final `X.Y.Z` tags without a GitHub Release. A retry
uses new staging tags and accepts an existing final tag only when its digest
matches the new staged image. If the digest differs, promotion stops rather
than moving the final tag; a maintainer must investigate the differing build
and complete the release without changing published tags. Existing GitHub
Release assets must also match the rebuilt files by SHA-256; missing assets
are added on retry. Promotion is not atomic, so consumers should wait for
the GitHub Release before using a new version. Keep the Git tag fixed at the
release SHA.
