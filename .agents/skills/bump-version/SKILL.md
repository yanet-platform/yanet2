---
name: bump-version
description: >-
  Release a new YANET version: compute the next semantic version from the
  commits since the last release tag, bump every component to it in one PR,
  wait for a human approval, merge, then push an annotated vX.Y.Z tag whose
  message becomes the GitHub Release notes. Use when the user asks to bump the
  version or cut a release ("bump version", "подними версию", "выпусти релиз",
  "/bump-version [major|minor|patch|X.Y.Z]").
---

# bump-version

One run moves a release forward from wherever it stopped. `scripts/bump.sh` (next to this file, run it from the repository root or a worktree) does every deterministic step; you choose the version with the user, write the release notes and drive git and `gh`. Needs `git`, `gh`, `cargo` and `jq`.

## Rules

- **Version rule.** While the major version is 0, a breaking change bumps minor and anything else bumps patch. From 1.0.0 on, a breaking change bumps major, a `feat` bumps minor and anything else bumps patch. A commit is breaking when its subject has `!` before the colon or its body has a `BREAKING CHANGE:` line. `bump.sh next` applies the rule; an argument `major|minor|patch|X.Y.Z` from the user overrides it, and 1.0.0 happens only that way.
- **One version everywhere.** `bump.sh apply` sets it in `meson.build` (dataplane, controlplane and operator binaries), every `Cargo.toml` package and `Cargo.lock`, `web/package.json` and `package-lock.json`, and prepends a `debian/changelog` entry. Deb packages and images still take their version from the tag.
- **Releases come from `main` only.** A patch for a `release/X.Y` line after backports follows `docs/releasing.md` by hand.
- **Tags are immutable.** Nothing is tagged until the level is re-checked on the exact commit and the user has approved the final notes.
- **Only origin counts.** The script reads tags with `git ls-remote`, so stale local tags do not change the version.
- **Re-checks look only at new commits.** `next` prints `expect=`, the higher of the computed and the chosen version. Later re-checks pass it as `--expect E` and exit 5 only when commits merged since then call for more, so a level the user chose on purpose is never overruled.
- Commit subject and PR title are `chore(release): bump version to X.Y.Z`. The PR body is an agreed exception to the bullets-only convention: one bullet, the `expect` marker, then the notes draft between markers:

  ```markdown
  - Bump all component versions to X.Y.Z.

  <!-- bump-version expect=E -->
  <!-- release-notes -->
  ...notes...
  <!-- /release-notes -->
  ```

## bump.sh

| Command | Does |
|---|---|
| `status` | Prints `phase=pr` (`pr=`, `version=`) for an open bump PR, else `phase=tag` (`version=`, `commit=`, `pr=`) for the newest bump commit since the last release when it has no tag yet, else `phase=prepare` |
| `next [--to <rev>] [--expect E] [ARG]` | Report for `<last tag on origin>..<rev>` (default `origin/main`): `base=` (a tag), `base_sha=`, `target_sha=`, `latest=` (the highest tag), `computed=`, `level=`, `auto=` (the version the commits call for), `version=` (the chosen one), `expect=`, then one `<class>\t<sha>\t<subject>` line per commit, class being `breaking`, `feat`, `fix`, `perf`, `internal` or `bump` |
| `apply X.Y.Z` | Rewrites every version and runs `check` |
| `check X.Y.Z` | Fails on any component whose version differs |

`next` exit codes: 0 ok, 1 error, 2 usage, 3 no commits since the last tag (always stop), 4 only internal commits (stop unless the user passed a level or version), 5 the commits now call for `auto=`, above `--expect`. The report is printed in every case.

## Phases

Start with `bump.sh status` and continue from the phase it prints.

### prepare

1. `bump.sh next [ARG]`, or the version agreed in a tag-phase recovery. On 3 or 4, report the range and stop.
2. Ask through AskUserQuestion: show the base tag, the computed level, the commits that drove it and the proposed version. Offer the proposed version first, the other levels, and cancel. A level other than the proposed one means rerunning `next` with it, which gives the `version=` and `expect=` to use.
3. `git fetch origin main`, then `git worktree add .claude/worktrees/release-vX.Y.Z -b chore/release-vX.Y.Z origin/main` (Codex: `.agent-state/worktrees/`).
4. In the worktree: `bump.sh apply X.Y.Z`. `git status --short` must list only the version files, stage them by name.
5. Write the notes draft (see Release notes) from the `next` report and put it into the PR body with the `expect` marker.
6. Commit with the subject above, push, `gh pr create --title "<subject>" --body-file <file>`. Tell the user the PR needs a human approval.

### pr

1. Wait for `reviewDecision` `APPROVED` and green checks. Poll in the background (`gh pr view <n> --json reviewDecision,state` every few minutes, `gh pr checks <n>` exit 0), never merge on a stale read.
2. Re-check on the fresh `main`: `bump.sh next --expect E V`, with V from the title and E from the marker.
   - Exit 5: rebuild the PR at the printed `auto=` version A. In the release worktree `git reset --hard origin/main` (the branch holds only the bump commit), `bump.sh apply A`, commit, `git push --force-with-lease`, `gh pr edit <n> --title … --body-file …` with the marker set to `expect=A`, then wait for a new approval.
   - Otherwise add the commits merged since the draft to the notes with `gh pr edit <n> --body-file …`.
3. `gh pr merge <n> --squash --subject "chore(release): bump version to V (#<n>)" --body ""`. No `--admin`.

### tag

1. `bump.sh status` gives the version V, the merged commit C and the PR number. Read E from the PR body marker and run `bump.sh next --to C --expect E V`. On exit 5 do not tag: report which commits raised the level and ask through AskUserQuestion whether to open a new bump PR at the printed `auto=` version (the prepare phase with that version, the new bump commit then supersedes C) or to stop.
2. Final notes: the draft between the PR markers, completed against the report, which now lists every commit in the release.
3. Show the final notes through AskUserQuestion (preview) and offer: push the tag, edit the notes first, or stop.
4. Write the notes to a scratch file and tag. `--cleanup=verbatim` keeps the `##` headings that Git would otherwise strip:

   ```bash
   git tag -a --cleanup=verbatim -F <notes> vV C && git push origin refs/tags/vV
   ```

   A local `vV` left from an earlier attempt blocks `git tag`: origin has no such tag (status proved it), so `git tag -d vV` first. If the push is refused, delete the local tag.
5. The tag push starts **Publish Release**. It takes a moment to register, so poll `gh run list --workflow release.yml --event push --json databaseId,headSha --jq '.[] | select(.headSha == "C") | .databaseId'` until it prints the run id, then `gh run watch <id> --exit-status`. On success report `gh release view vV --json url`. On failure report it and point to "Patches and retries" in `docs/releasing.md`.
6. Remove the release worktree and the local branch.

## Release notes

Written for people who run YANET, in plain English, from the `next` report. Read the PR (`gh pr view <n> --json title,body`, the number is the `(#n)` suffix) whenever the subject alone does not say what changes for them.

- Sections in this order, empty ones omitted: `## Breaking changes` (class `breaking`, any type), `## Features` (`feat`), `## Fixes` (`fix`), `## Performance` (`perf`). Commits of class `internal` or `bump` stay out.
- One bullet per user-visible change, related commits merged into one bullet. Each bullet is a sentence ending with a period, followed by its PR links, e.g. `(#2705, #2706)`. No conventional-commit prefixes. Component names such as `acl` or `yanet-cli` are fine.
- A breaking bullet says what stops working and what to do instead.
- No authors.
- A release forced over internal-only commits says `No user-facing changes.`
- Last line: `**Full changelog**: https://github.com/yanet-platform/yanet2/compare/<base>...vV`, with `<base>` the report's `base=` tag, e.g. `compare/v0.1.0...v0.2.0`.

## Report

The phase reached, the version, PR and tag links, the Publish Release run and release URL, and anything left for the user.
