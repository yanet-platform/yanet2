# clang-format action

This action runs `clang-format` inside a prebuilt container image, pinned in `action.yml` by an immutable `@sha256:...` digest rather than the mutable `:19` tag.

## Why the pin

`ghcr.io/yanet-platform/clang-format:19` is a mutable tag: the `Build and push clang-format image` workflow can rebuild and re-push it at any time, silently swapping the formatter binary underneath every consumer. A new `clang-format` patch release changes brace, wrapping, and alignment heuristics enough to either mass-reformat the tree on whichever unrelated PR happens to touch a changed file, or fail every open PR at once. Pinning to a digest makes a version bump a deliberate, reviewed step instead of something that happens silently on the next image rebuild.

## The gotcha this creates

Because CI consumes a digest, editing `Dockerfile` or `entrypoint.sh` does not change what the formatting gate runs, and editing `action.yml` in the same PR does not fix that, because no rebuild happens until the change reaches `main` or the workflow is dispatched manually. The concrete trap: a PR that adds a new action input together with the `entrypoint.sh` code that reads it gets a **green** `C formatting` check produced by the old image, which ignores that input entirely. So treat any change to the image as step 1 of the two-step bump below.

## Bumping the pinned version

1. Change `Dockerfile` or `entrypoint.sh`, or run the `Build and push clang-format image` workflow manually via `workflow_dispatch` to pick up a newer `release/19.x` patch release.
2. After the rebuild completes, read the pushed reference and `clang-format --version` output from its job summary.
3. Open a follow-up PR that replaces the `image:` digest and the version comment in `action.yml`.
4. `check-format.yml` also triggers on changes under this directory, so that PR runs the formatting gate over the whole tree. Any reformatting the new version wants shows up as a red check on the bump PR itself — fold that reformatting into the same PR.

## Reading the currently published digest without merging anything

```
docker buildx imagetools inspect ghcr.io/yanet-platform/clang-format:19 --format '{{.Manifest.Digest}}'
```

This is a public package and needs no authentication.

## Notes

The image is built for linux/amd64 only. The digest currently pinned in `action.yml` is clang-format 19.1.7, built from `llvmorg-19.1.7`. `Dockerfile` clones the `release/19.x` branch, so a rebuild resolves whatever that branch points to at build time — which is why the workflow's job summary reports the version it actually produced.

Every rebuild publishes both the moving `:19` tag and an immutable `:19-<commit-sha>` tag, so the manifest a pin names always keeps a tag and cannot be swept by an untagged-version cleanup.
