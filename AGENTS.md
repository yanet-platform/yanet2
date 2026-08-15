# AGENTS.md

This file provides guidance to AI coding agents working in this repository.

## Project Overview

YANET is a high-performance software router built on DPDK. It uses a multi-language approach:

- **C + DPDK**: Dataplane (fast-path packet processing)
- **Go**: Control plane (modules, gateway API)
- **Rust**: CLI tools
- **TypeScript/React**: Web UI

## Build & Test Commands

```bash
# Initial setup
git submodule update --init   # DPDK submodule
meson setup build             # configure C/DPDK build

# AI agent tooling (not needed to build YANET; requires Node/npm)
make ai/agents                # generate .claude/agents/, .codex/agents/, and .opencode/agents/ (regenerate after any pull touching .rulesync/)

# Build everything
make all                      # builds dataplane + CLI

# Build individual components
make dataplane                # meson compile -C build
make cli                      # cargo build --release --workspace
cd controlplane && go build ./...
npm ci && npm run build -w web   # web is an npm workspace. Install at repo root

# Debug/sanitizer builds
make setup-debug              # debug build without sanitizers
make setup-asan               # debug + address/undefined sanitizers

# Run tests
make test                     # Go tests + meson tests (cleans go cache first)
make test-asan                # tests with address sanitizer
make test-tsan                # thread sanitizer (separate build-tsan dir)
make test-functional          # functional tests (requires QEMU/VM)
meson test -C build <name>    # run a single C test by name
go test ./modules/route/...   # run Go tests for a specific module

# Formatting & linting
gofmt -w .                    # Go
clang-format -i <file>        # C
cargo +nightly fmt            # Rust (uses nightly-only options in .rustfmt.toml)
cargo clippy                  # Rust lints
make proto-lint               # protobuf formatting check
make lint-go                  # stylelint + golangci-lint modernize (lint/style/, .golangci.yml)
make lint/comments            # source-comment convention lint
make lint/clang-syntax        # clang -fsyntax-only sweep; needs a configured build/ (meson setup)
make lint-commit              # commit-subject convention check (lint/commit/)
make proto-go                 # generate *.pb.go via protoc (needed before go lint locally)
make hooks                    # install the git hooks (run once per clone)

# Fuzzing
make fuzz                     # build fuzz targets
make fuzz MODULE=<name>       # run specific fuzzer
```

## Architecture

### Repository Layout

Top-level directories and their roles:

- `dataplane/`     — main C/DPDK binary (`main.c`, `config.c`, `dpdk.c`, `worker.c`, `drivers/`, `unittest/`).
- `controlplane/`  — Go gateway, CGO bindings (`ffi/`), root protos (`ynpb/`), control-plane package (`yncp/`), entrypoints (`cmd/`).
- `modules/`       — packet-processing modules (see Module Structure).
- `devices/`       — device adapters (`plain`, `vlan`); same layout as modules.
- `operators/`     — long-running orchestration daemons (see Operators).
- `lib/`           — C support libraries: `controlplane`, `counters`, `dataplane`, `dataplane_ut`, `errors`, `filter`, `fwstate`, `logging`, `utils`, plus `tests/` and `fuzzing/`.
- `api/`           — public C API headers exposed to control plane (`agent.h`, `config.h`, `counter.h`, `info.h`).
- `bindings/go/`   — root-level Go CGO bindings for the agent/shared-memory agent surface.
- `cli/`           — Rust CLI workspace: `core/` (yanet-cli library), `modules/` (shared CLI subcommands), `Makefile`.
- `common/`        — shared libraries across languages (see Shared Libraries).
- `web/`           — TypeScript/React Web UI.
- `subprojects/dpdk/` — DPDK as a Meson subproject.
- `docs/`, `deploy/`, `debian/`, `etc/` — documentation and packaging.

### Data Flow

```
CLI (Rust) --gRPC--> Gateway (Go) --gRPC--> Module Control Plane (Go) --shared memory--> Dataplane (C/DPDK)
```

The dataplane reads configuration from shared memory and continues working with the last valid config if upper layers fail. Configuration updates are applied atomically.

### Gateway (controlplane/)

A single Go gRPC server that proxies requests to module backends. Modules register themselves with the gateway on startup. The gateway routes by gRPC service name to the correct module backend. Also provides an HTTP-to-gRPC translation layer.

Key packages:

- `cmd/` — binary entrypoints: `yncp-director` (gateway daemon), `bird-adapter` (legacy build of the BIRD adapter).
- `gateway/` — flat package: API gateway server (`gateway.go`, `runner.go`, `registry.go`, `service.go`, `auth_service.go`, `cfg.go`) plus client-side helpers used by built-in services and operators (`registrar.go`, `registration_loop.go`, `tls.go`, `credentials.go`).
- `builtin/` — in-process built-in services: `pipeline`, `inspect`, `function`, `counters`, `logging`. Each implements the structural `gateway.Service` interface and is constructed by the director, then passed to `NewGateway` via `gateway.WithService(...)`.
- `internal/auth/`, `internal/version/`, `internal/xgrpc/` — supporting packages.
- `ffi/` — CGO bindings for shared memory (`shm.go`, `agent.go`, `pipeline.go`).
- `ynpb/` — root protobuf definitions: pipeline, device, counters, inspect, logging, auth, function, gateway, module.
- `yncp/` — control-plane package (`cfg.go`, `director.go` — module registration hub, `version.go`).

### Module Structure

Modules in `modules/` follow one of two layouts. New modules use the **canonical** form. Legacy modules are gradually migrated.

**Canonical** (decap, dscp, forward, route — use as reference):

```
modules/<name>/
  api/               # C library for control plane FFI (controlplane.c/h)
  bindings/go/       # CGO wrapper crate consumed by controlplane
  controlplane/      # Go control plane
    <name>pb/        # Protobuf definitions + generated code
    mod.go           # Module struct implementing BuiltInModule; New() constructor takes options
    backend.go       # Shared-memory write path (uses bindings)
    service.go       # gRPC service implementation
    service_test.go  # Service-level tests
    cfg.go           # Module config struct + DefaultConfig()
  dataplane/         # C packet processing (header-only hot paths as static inline)
    config.h         # Shared memory config structure
    dataplane.c/h    # Module entry point
  cli/               # Rust CLI crate (build.rs runs tonic-build)
  tests/             # C unit tests
  fuzzing/           # LibFuzzer targets
  internal/          # Optional: module-private Go packages (route only — discovery, rib).
```

`acl`, `blackhole`, `mirror`, `nat64`, and `route-mpls` have migrated to this layout too. Of those, `acl` and `blackhole` have no `fuzzing/` yet, and `route-mpls` has neither `tests/` nor `fuzzing/`.

**Legacy** (pdump): no `bindings/`, CGO calls live directly in `controlplane/ffi.go`, no `backend.go`. `fwstate` is partially migrated: it has `bindings/go/` but still routes CGO through `controlplane/ffi.go` and has no `backend.go`.

**Early-stage** (balancer2): has `api/`, `bindings/go/`, `dataplane/`, and `cli/`, but its `controlplane/` holds only `balancerpb/` — no `mod.go`/`cfg.go`/`service.go`/`backend.go` — and it has no `tests/` or `fuzzing/`.

Module dataplane symbols are exported via meson linker defsym: `new_module_<name>`.

Active modules: `route, acl, balancer2, blackhole, forward, decap, nat64, fwstate, dscp, pdump, route-mpls, mirror`.

### Devices

`devices/` mirrors `modules/` layout (`api/`, `controlplane/`, `dataplane/`, `cli/`) but for device adapters rather than packet-processing modules. Active devices: `plain`, `vlan`, `trafgen`.

### Operators

`operators/` holds long-running Go control-plane processes that orchestrate the dataplane through the gateway, distinct from per-module gRPC services.

- `operators/pipeline` — declarative reconciliation operator (`cmd/`, `internal/`, `operatorpb/`, Rust `cli/`). Its structural template has been replicated by per-module operators `operators/{decap,forward,route}`, each with `cmd/` + `internal/` + `operatorpb/` + a Rust `cli/` (route ships two CLI crates: `cli/route` and `cli/neighbour`).
- `operators/bird-adapter` — BIRD routing-daemon adapter (canonical agent layout: `adapterpb/`, `internal/`, `service.go`). Note: `modules/route/bird-adapter/` is a separate proto-contract subtree (`adapterpb/`, `proto/`) consumed by the agent — not a duplicate binary.
- `operators/neighbours` — **web-only here**: the public tree carries just `web/`, because the operator process lives in the private repo. The missing `cmd/`/`internal/` is not an incomplete operator.

### Shared Libraries

- `common/go/` — Go support packages: `xcfg`, `xcmd`, `xerror`, `xiter`, `xnetip`, `xpacket`, `logging`, `metrics`, `dataplane`, `bitset`, `maptrie`, `rcucache`, `testutils`.
- `common/rust/` — Rust shared crates: `commonpb`, `filterpb`, `ynpb` (compiled ynpb protos, exposes `pub mod pb`), `bitmap`. Module CLIs depend on these via `extern_path` instead of recompiling protos.
- `common/commonpb/` — Go protos: `metric`, `target` (used by the metrics package).
- `common/filterpb/` — Go filter proto plus helpers (`convert.go`, `filter.go`).
- `common/ttlmap/` — header-only C TTL map (`ttlmap.h` + `detail/`).
- `common/*.h` — C headers: `lpm.h`, `radix.h`, `crc32.h`, `hash.h`, `rcu.h`, `memory*.h`, etc.

### Shared Memory Pattern

1. Module control plane attaches via `ffi.SharedMemory` (Go CGO)
2. Creates agent via `shm.AgentAttach(name, instanceIdx, size)`
3. Writes C-level config through FFI functions (e.g., `acl_module_config_update()`)
4. Uses `runtime.Pinner` to pin Go memory during C calls
5. Dataplane reads updated config atomically

### Rust CLI Workspace

- **Core library**: `cli/core/` (crate name `yanet-cli`, aliased as `ync` in dependents)
- **Module CLIs**: `modules/<name>/cli/` — each is a separate crate
- **Shared CLI modules**: `cli/modules/{inspect,pipeline,function,counters,common}`
- **Proto compilation**: Each CLI's `build.rs` uses `tonic-build` (client-only)
- **Binary naming**: `yanet-cli`, `yanet-cli-route`, `yanet-cli-acl`, etc.
- **Common dependency**: `ync = { path = "../../../cli/core", version = "0.1", package = "yanet-cli" }`
- **Local Makefile**: `cli/Makefile` runs `cargo build/clippy/fmt` scoped to the CLI workspace without leaving the directory.
- **Three registration surfaces move together**, on add AND remove: root `Cargo.toml` members, the root `Makefile` CLI list (`CLI_CORE_MODULES` / `CLI_MODULES`, suffix only), and `debian/yanet2-cli.install` (`usr/bin/yanet-cli-<name>`, else `dh_missing --fail-missing` aborts build-deb). Only the first affects `cargo build --workspace`, so a miss is SILENT — it builds, CI is green, and it is never installed. Private (gitignored) module CLIs invert the first: standalone workspaces, NOT in root `members`, since cargo hard-fails on a missing member path.

### Build System

Meson orchestrates C/DPDK builds and Go binary compilation (via `custom_target` with `go build`). Rust is built separately via Cargo. DPDK is a Meson subproject in `subprojects/dpdk/`. Sanitizer flags are propagated to CGO automatically when using `-Db_sanitize`.

Agent charters are authored once at `.rulesync/subagents/*.md` — `make ai/agents` generates the `.claude/agents/`, `.codex/agents/`, and `.opencode/agents/` trees all three clients read. Public skills are authored once under `.agents/skills/<name>/`, which Codex reads directly, and the Claude Code tree mirrors them by symlink (`.claude/skills/<name>` → `../../.agents/skills/<name>`).

## Coding Conventions

### General (every language — C, Go, Rust, TypeScript, shell, Makefiles, proto, YAML)

- **No unlabeled pure-run comment separators.** A comment whose payload, after delimiters and surrounding whitespace, is a run of at least three identical separator glyphs is forbidden, for example `/////////////////` or `// ================`. Labeled comments such as `// --- foo ---`, `### something`, Markdown/hashed headings, ASCII or bit diagrams, and table underlines are allowed. A pure `=` or `-` run may form a Setext underline only when it immediately follows a nonempty comment-text line. Multiline block comments are assessed as one payload. Enforce this rule with `make lint/comments`.
- **Comments: structure and scope.** A doc comment opens with a one-line brief, then a blank comment line and detail. Single-sentence and inline comments are exempt from that shape. This applies to `//`, `///`, `#`, and `/* */`. Write only what a caller needs at the call site, skipping what the code, a test's own name, or a neighbouring doc already shows, and leave the change's narrative in the pull request body.
- **A comment is budgeted at 8 prose lines, and 12 is a hard stop.** A rationale guarding a single statement gets 3-4. Blank separator lines, lines whose payload begins with `@param`, `@return`, `@see` or `@file`, diagrams, tables, and license headers do not count toward the budget. Over budget, cut ideas rather than re-paragraph: delete restated mechanism, the code's former shape, enumerated cases, hypotheticals named but not taken, and anything a nearby doc states. Cutting never reaches the load-bearing reason, and a rationale the code requires is never dropped as redundant with a nearby doc. Cramming several ideas into one comment is blocking, doc or inline. A claim still needing more than 12 prose lines to defend means the code or API needs fixing.
- **A comment never grows across review rounds.** Re-cut it whole instead of appending a qualifier, and prefer weakening a claim to adding a counterexample. A round that pushes a comment further over budget is a defect in the round.
- **Comment prose.** Use one space between sentences, human prose, and present-tense code description. Omit provenance, review references, and diff narration. Never join two independent clauses with a semicolon — end the sentence or use a dash instead.
- **A rationale names what must fail for the claim to fail, prescribes a working remedy, and is deletion-tested.** Full rationale-content and claim-falsification rules: `.claude/conventions/comments.md`, loaded on demand by the agent writing or reviewing a comment.
- **`set -e` does not cross `$(...)`.** In shell and Makefile recipes, guard every fallible command in a captured function or start that function with `set -e`.

### Go

See `.claude/conventions/go.md` — loaded on demand by the agent writing or reviewing Go.

### Rust

See `.claude/conventions/rust.md` — loaded on demand by the agent writing or reviewing Rust.

### C

See `.claude/conventions/c.md` — loaded on demand by the agent writing or reviewing C.

### Tests & Benchmarks

- **Benchmarks must exercise their claimed path**: traffic matches rules (protocol, device, address family), every device named in a dump is registered, sampling covers the dataset (no `stride == 1` prefix bias), and every documented input mode works end-to-end.
- **`for b.Loop()` suppresses inlining and devirtualization inside its body.** A closure, variadic option, or interface-boxed argument can therefore heap-allocate in the benchmark although the real call site stacks it. The distortion needs an optimization the real call site gets and the loop body does not: an inlined helper that produces the argument, such as an option constructor returning a closure, or a devirtualized interface call whose arguments then stop escaping. A closure written inline at the call site and passed by a direct call to a concrete type is unaffected. Cross-check `-gcflags=-m` against a real caller before trusting the number. `b.Loop()` stays the default, since its keep-alive is what stops the measured work being eliminated, but leave a benchmark that has deliberately switched to `for range b.N`: the `bloop` analyzer flags that form in editors and the pinned `golangci-lint` does not, so a quick-fix reverting it will not be caught by CI.
- **dataplane_ut `Bench`/`run_rounds` recycles fixed packets**: reject or neutralise allocating/emitting actions (for example ACL `CREATE_STATE` sync) so mbufs do not leak or distort results.
- **Exported Go APIs whose arguments cross CGO into C-side array indexing** (device IDs, queue/worker indices) validate the range on the Go side before the call.
- **C-only changes require `go test -count=1` and a clean `meson compile`.** Go can link a stale C archive after cached tests or a failed compile. Inspect compile output first. Mutation tests must corrupt behaviour, not delete calls. Shared-memory layout or `YANET_MODULE_ABI_VERSION` changes require `go clean -cache`, `rm -f <binary>` for standalone CGO binaries, and removal of `/dev/hugepages/yanet*` plus cached VM baselines.
- **Race tests need targeted `-run` repetition (about 10–20 runs).** One `go test -race -count=1` can miss or misattribute a race through `t.Parallel()` siblings.
- **Do not pin human-readable CLI formatting in tests.** Test logic/invariants (state, staleness, rounding, widths, wrapping), not literal output. ASCII-only paths and canonical MAC/hex rendering are contracts and may be tested.
- **`modules/*/tests/vm` suites require manual binary staging.** An exit code of 127 or 3 from them is a staging gap, not a product defect — stage the binaries and re-run before filing anything.

### TypeScript/React

Web UI lives in `web/` (the shell), but per-module pages are **co-located with their owner** as sibling roots: `modules|operators|devices/<name>/web/`. That ownership decides routing, so it stays here. The style and test rules are in `.claude/conventions/ts.md`, loaded on demand by the agent writing or reviewing TypeScript/React.

### Worktree isolation

- **Do every writing task in a dedicated linked worktree.** Create or reuse it on a task branch forked from confirmed `origin/main` before the first command that writes a tracked file, then keep editing, generating, building, testing, formatting, staging, and committing inside it. Purely read-only inspection may run anywhere.
- **The primary checkout stays on `main` and takes no tracked-file writes.** It is the synchronization anchor: fetch and pull, inspect state, create or remove worktrees, and launch agents so gitignored trees resolve. Its gitignored trees are the real ones, so writes to `.arch/`, the client memory trees, the shared `build/`, and any gitignored tree that cannot be worktree-isolated still belong there.
- **Only an explicit instruction for the current task waives the isolation.** A generic request to fix, change, build, test, commit, pull, or continue is not that waiver. Before the first writing command, `cd` into the worktree or address it as `git -C <worktree-root>`, and confirm that `git rev-parse --show-toplevel` and `git branch --show-current` name it and a non-`main` branch: an agent inherits the launching cwd, typically the primary checkout on `main`.
- **Default location is the client's root-local gitignored worktree directory** — `.claude/worktrees/<name>` for Claude Code, `.agent-state/worktrees/<name>` for Codex — never a sibling of the repository. A bare tree is about 16 MB, but a task that compiles C needs its own multi-gigabyte `build/`: put that one on a volume with room for it rather than on a tight repository volume.
- **Seed a fresh worktree before launching anyone into it**, because it holds only tracked files. Symlink the client memory tree and local settings. The build directory is always called `build`, so every existing `meson compile -C build` recipe stays correct, but which of two things it is depends on what the task's gates do with it. A gate that only links against the archives already in it, or ignores it entirely, may borrow the primary `build` by symlink, seeding the generated `*.pb.go` beside it — `go build`, `go test`, `cargo`, `npm` and a lint-only target such as `make lint/comments` all qualify. A gate that PRODUCES or CONSUMES `build` needs the worktree's own real one instead: `make test`, `make dataplane`, `make fuzz` and `make test-asan` drive meson at it, and `make test-functional` mounts it into the VM without meson at all. Through the symlink each of those exercises the primary checkout's artifacts rather than yours, so the gate goes green on stale ones while ninja rewrites the archives every other worktree links against, and `meson setup --reconfigure`/`--wipe` retargets the developer's shared directory at your worktree. Building its own costs more than the tree: `meson setup build` initialises the empty `subprojects/dpdk` and `subprojects/libpcap` itself, but a linked worktree does not share the superproject's submodule objects, so git clones them from the remote (network required) into `.git/worktrees/<name>/modules/` — budget roughly 255 MB on top of the build, and put such a worktree on a volume with room. A web gate needs `npm ci` from the worktree root. A gitignored tree, such as a private module, cannot be worktree-isolated at all — work it at the primary checkout with absolute paths.
- **Before a command that produces or consumes `build`, check whether it is a real directory and report a seeding gap if it is a symlink** — a borrowed link makes the gate exercise the primary checkout's artifacts instead of yours.
- **If your memory tree is missing from the worktree, write through the primary checkout's absolute path** rather than creating a second copy that dies with the worktree.
- **Never symlink `.claude/agents/`, `.codex/agents/`, or `.opencode/agents/` into a worktree.** `make ai/agents` writes every generated file through the symlink before it notices and refuses, and none of these trees are git-tracked — the damage to the primary's real tree is neither prevented, detected, nor recoverable. `.worktreeinclude` gives a tool-created worktree its own copy instead, but nothing ever refreshes it: `.githooks/post-merge` no-ops outside the primary checkout, and the old tracked scheme's propagation through merge or rebase is gone. Charter generation (`make ai/agents`) runs only at the primary checkout, after merge.

### Commits & PRs

- **Never run `git commit`/`--amend` unless the current request asks for it.** Default: stage nothing, return the diff plus a verification summary.
- **Never `git stash`/`checkout`/`reset`/`restore` to A/B a pre-existing state.** `stash push -- <paths>` aborts on untracked files, a later bare `pop` pops an unrelated stash, and in a shared dirty worktree this destroys another agent's work. Read HEAD instead (`git show HEAD:<path>`, `git diff HEAD -- <path>`) or materialise a baseline out of tree (`git archive <ref>`, symlinking `subprojects/{dpdk,libpcap}` back in). If you suspect a failure is pre-existing, say so and stop.
- Commit format: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): brief description` with high-level description (no code-level details, no backtick-quoted symbol names).
- **Do not** add `Co-Authored-By` or `Generated with` footers for Codex or Claude Code.
- PR title: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): <short description>` — MUST include the scope, exactly like commit subjects. A scopeless `feat: …` title is a convention violation.
- Scope is mandatory (lowercase, comma-separated for multiple scopes). An optional `!` before the colon marks a breaking change (e.g. `feat(route)!: ...`). The brief must not start with an uppercase letter and carries no trailing period. This is enforced mechanically by the `commit-msg` hook (`make hooks`) and the Commit Lint CI job (`lint/commit/`).
- PR body: bullets start with capital, end with period. Add `Closes #<number>.` when applicable. **Do not** include a `## Summary` header — content goes directly. **Do not** include a `Test plan` section. PR descriptions have no 80-char line limit.
- **Reach GitHub through the `github` MCP server rather than `gh`**, where a GitHub MCP server is granted. Agents without a GitHub MCP server in their tool allowlist keep using read-only `gh`. Reads (PR/issue/run state, diffs, review threads, job logs) and, on a write-capable server, writes (create/update/merge PRs, comments, review-thread resolution, workflow reruns) all have tools, and they return structured data instead of shell-parsed text. `gh` stays correct only for the enumerated gaps — continuous check watching, `--admin` merge bypass, REST/GraphQL endpoints with no tool, and `gh auth refresh`. Record the reason on every fallback.
  - The two-server split is Claude-Code-specific: `github` (full read+write) is granted to the architect and the planner, and `github_ro` (read-only) is granted to the reviewer and the bug-hunter. The remaining agents hold neither and use read-only `gh`.
  - Codex defines a single `mcp_servers.github` server, shared by every Codex agent including the reviewer, with no per-agent scoping and no read-only mode — it is write-capable end to end, so on Codex the reviewer's read-only discipline is a charter obligation the agent enforces on itself, not a mechanical guarantee from the server.
- **MCP file-write tools (`create_or_update_file`, `push_files`, `delete_file`, `create_branch`) never substitute for local git**: they commit server-side, bypassing staging discipline, the `commit-msg` hook, and local verification gates.

## Agent Memory & Feedback

Client memory is root-local and client-specific: Codex uses `<repo>/.agent-state/agent-memory/<agent>/`. Claude Code uses `<repo>/.claude/agent-memory/<agent>/`. Codex worktrees are `<repo>/.agent-state/worktrees/`. Claude Code worktrees are `<repo>/.claude/worktrees/`. Never put either tree beneath a subdirectory or redirect one client into the other's tree.

### Structure

- **One lesson per `kebab-case-slug.md` file.** Its first line is a summary of at most 150 characters (imperative for rules, excluding index wrapper), byte-identical to its `MEMORY.md` index line. After a blank line, include the rule/fact, `Why:`, and when needed `How to apply:`. No YAML frontmatter — the client harness's generic memory instructions describe wrapping each file in a `name:`/`description:`/`metadata:` frontmatter block, but this repository's bare-summary format overrides that guidance and wins. Keep the lesson body, including its summary line, to at most 12 lines. Lessons also carry `Last applied: YYYY-MM`, bumped when the lesson is actually applied. A lesson older than six months with no `(seen: N)` marker is deleted at the next sweep.
- **`MEMORY.md` is only an index.** It is auto-loaded and has `# <agent> memory`, then one `- [<summary>](<slug>.md)` line per lesson, grouped under optional `## Rules`, `## Project context`, and `## References` headings with optional `###` subheadings. Keep it to at most 60 lesson entries — count the `- [` rows, not headings or blanks. Never put lesson bodies there. At this cap you may not add a new entry until you have merged or deleted one — treat it as a failing lint, not advice. 200 lines is where the auto-load hard-truncates. 60 entries is the far tighter budget for what every run pays to read, so being under 200 is not the test.
- User-profile facts are lesson files too, summary prefixed `User: …`, indexed under `## Rules`.

### What to record

- **Record corrections and confirmed approaches.** Include why each matters so future readers can judge edge cases.

### What NOT to save

- Anything already in `AGENTS.md`, the repo, or chat history: code patterns, paths, architecture, git history, and debugging recipes.
- Ephemeral task state, TODOs, and design logs; use plans, `.arch/`, or `TODO.md`.

### Hygiene

- **Update, do not duplicate.** Scan first, grepping 2-3 key phrases of the new lesson across every agent tree (all of `<client-tree>/agent-memory/*/`, indexes and lesson files, not just the target agent's) — a duplicate often landed under a different agent. Update the matching lesson and append `(seen: N)` to both identical summaries, beginning at `(seen: 2)` — inline on the rule that recurred when the file holds several. At `(seen: 5)`, consider promotion — a language-specific rule goes to `.claude/conventions/<lang>.md`, and only a rule every agent needs goes to `AGENTS.md`. Promotion deletes the lesson and index row; a decline is recorded inline as `(kept local)` and is not re-litigated at the next sweep. A merged file's counter belongs to no single rule: promote only the sub-rules that independently reached five, and re-count from the survivors. Promoting to `AGENTS.md` grows what every agent reads on every run, so that bar is deliberately high.
- **Verify notes against code.** Delete or correct stale/wrong notes.
- **Compact by merging, never dropping facts.** A merge rewrites two lessons into one shorter rule. Concatenating them under provenance headers is not a merge. A merge keeps each counter attached to the rule that earned it, and the summary carries the highest. Keep live bug classes and recipes separately discoverable. Keep index summaries as retrieval hooks, not findings. Promote only terse, broadly applicable rules. Agent-specific rules stay in that agent's section.

### Delegation & verification (architect)

These rules bind the delegating agent, not the implementer.

- **Verify specialist claims yourself.** Run real builds/tests. Reproduce mechanism claims, pre-existing failures, flakes, and new diagnostics before acting.
- **Brief only verified details.** Read and cite code, and falsify exclusionary premises with a probe. Trace transitive callers/pointers before accepting reachability claims. Preconditions describe state, not reachability.
- **Require whole-class audits.** A `file:line` sample is not the population: pair it with the defining grep. Map all sites and callers before modifying shared primitives, return values, or a named data-flow site. The repo-search tooling is gitignore-aware, so a plain repo-wide grep silently returns nothing for generated files and for gitignored private trees. Any "no callers left", "nothing else uses this", or whole-population conclusion is therefore false until re-run with the private and generated paths named explicitly.
- **Review the complete candidate with `review-change`.** Give the reviewer the task brief, exact base, candidate worktree or PR, and full intended manifest, including explicit untracked or ignored paths. Classify every entry as publish or local-only and stage only the publish manifest. Keep the first pass independent of existing review comments. Record the approval fingerprint (base, manifests, modes/types, content) and, for a PR, its head SHA. Recheck the fingerprint immediately before the verdict. Any fingerprint change requires a whole-candidate restart. Stage/commit/amend/push may carry approval only after the publish fingerprint is identical on the packaged side and local-only entries remain unchanged. A new commit SHA alone is not a content change.
- **Name the task worktree's absolute root in every brief, and tell the agent to `cd` there first.** Agents inherit cwd, and a worktree lacks gitignored state such as `.arch/`, `.agent-state/agent-memory/`, and `.claude/agent-memory/`, so seed or symlink whatever the agent's gate needs before launching it. Before removing a worktree, salvage both memory trees and establish each participant's tree before disputing file existence.
- **Check open PRs before briefing and inspect their diffs.** If one owns the zone, drive or review it rather than competing.
- **Reconcile the safety queue every roughly ten merges** and after consecutive `refactor`/`docs` work. Re-verify audit ledgers against HEAD before ranking.
- **Production is the oracle.** A mechanism replay can false-positive and an unrealistic timing model can false-green. Instrument production when a repro and live system disagree.
- **A review finding binds, its suggested remedy does not.** Diagnose the defect and choose the minimal fix. Reconsider the premise when the remedy is disproportionate.
- **After a design pivot, re-derive inherited constraints.** Do not retain dead or mutually unsatisfiable requirements.
- **Route non-application work to `coder-c` with a charter note.** State that ops/packaging/CI work is not C and no C/Go/Rust/TS/proto/meson source changes. Do not route infrastructure to `coder-ui`. For untracked/gitignored trees, have reviewers inspect explicit on-disk paths rather than relying on `git diff`.

## Key Dependencies

- **DPDK**: v23+ (submodule)
- **Go**: 1.24.13+
- **Rust**: 1.88+ (nightly for formatting)
- **Meson**: 0.61+
- **Protobuf**: 3.0+ (protoc-gen-go >=1.36.5, protoc-gen-go-grpc >=1.5.1)
