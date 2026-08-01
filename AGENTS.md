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

# Build everything
make all                      # builds dataplane + CLI

# Build individual components
make dataplane                # meson compile -C build
make cli                      # cargo build --release --workspace
cd controlplane && go build ./...
npm ci && npm run build -w web   # web is an npm workspace; install at repo root

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
make lint/comments            # pure-run comment separator lint
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

Modules in `modules/` follow one of two layouts. New modules use the **canonical** form; legacy modules are gradually migrated.

**Canonical** (decap, dscp, forward, route — use as reference):

```
modules/<name>/
  api/               # C library for control plane FFI (controlplane.c/h)
  bindings/go/       # CGO wrapper crate consumed by controlplane
  controlplane/      # Go control plane
    <name>pb/        # Protobuf definitions + generated code
    mod.go           # Module initialization
    backend.go       # Shared-memory write path (uses bindings)
    service.go       # gRPC service implementation
    service_test.go  # Service-level tests
    cfg.go           # Module config struct
  dataplane/         # C packet processing (header-only hot paths as static inline)
    config.h         # Shared memory config structure
    dataplane.c/h    # Module entry point
  cli/               # Rust CLI crate (build.rs runs tonic-build)
  tests/             # C unit tests
  fuzzing/           # LibFuzzer targets
  internal/          # Optional: module-private Go packages (route only — discovery, rib).
```

`acl`, `blackhole`, `mirror`, `nat64`, and `route-mpls` have migrated to this layout too; of those, `acl` and `blackhole` have no `fuzzing/` yet, and `route-mpls` has neither `tests/` nor `fuzzing/`.

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
- `common/btree/` — header-only C BTree (`u16.h`, `u32.h`, `u64.h`).
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

## Coding Conventions

### General (every language — C, Go, Rust, TypeScript, shell, Makefiles, proto, YAML)

- **No unlabeled pure-run comment separators.** A comment whose payload, after delimiters and surrounding whitespace, is a run of at least three identical separator glyphs is forbidden, for example `/////////////////` or `// ================`. Labeled comments such as `// --- foo ---`, `### something`, Markdown/hashed headings, ASCII or bit diagrams, and table underlines are allowed. A pure `=` or `-` run may form a Setext underline only when it immediately follows a nonempty comment-text line. Multiline block comments are assessed as one payload. Enforce this rule with `make lint/comments`.
- **Doc comments (fields, structs, functions).** Use a short one-line brief, then a blank comment line and concise detail. This applies to `//`, `///`, `#`, and `/* */`; single-sentence comments and inline implementation comments are exempt. A crammed multi-idea doc comment is blocking.
- **Rationale comments must cite the operative guarantee and prescribe a working remedy.** For panic, safety, impossibility, reachability, or remedies, name what must fail for the claim to fail; use the deletion test. Correct every sibling occurrence across file types, including every clause and execution-order claim (`before`, `after`, `first`, `already`, `then`), after grepping the claim. Open sibling files before claiming no other caller/test exists.
- **Comment prose.** Use one space between sentences, minimal semicolons, human prose, and present-tense code description; omit provenance, review references, and diff narration.
- **`set -e` does not cross `$(...)`.** In shell and Makefile recipes, guard every fallible command in a captured function or start that function with `set -e`.

### Go

- **Receiver names**: always `m`. No type-letter mnemonics.
- **No abbreviated identifiers**: spell out names in production and tests (`labels`, `metrics`, `durationSeconds`); only `ok`, `err`, `ctx`, `idx`, and short-scope type-assert temporaries are exceptions.
- **Naming**: `*Config`, never `*Cfg`; constructors are `NewStore`/`NewClient`, never bare `New`.
- **Loop index**: use `idx`, not `i`; prefer `for idx := range n` to C-style loops (Go 1.22+, enforced by `modernize`).
- **Maps**: `map[K]V{}` not `make(map[K]V)`.
- **gRPC**: `grpc.NewClient` not `grpc.Dial`.
- **Concurrency**: prefer `errgroup.Group` over `sync.WaitGroup`, including in tests.
- **Mutex discipline**: write `defer m.mu.Unlock()` immediately after `m.mu.Lock()`; split helpers when observers/RPCs must run unlocked. Holding it across a self-locking non-reentrant collaborator is correct when snapshot+`Set` must be atomic.
- **Logging (zap)**: structured lowercase messages, snake_case keys, typed fields, and `*zap.Logger`, never Sugared. `log *zap.Logger` is the last struct field; use `zap.With` for per-instance context, avoid count/elapsed noise, and use past-tense `Info` for completed changes.
- **Logger options**: constructors/methods accepting `*zap.Logger` use `NewFoo(cfg, WithLog(log))`, `options ...Option`, `opts := newOptions(); for _, o := range options { o(opts) }`, and a per-constructor `WithLog()`. The `logger` stylelint check enforces this.
- **Encapsulation**: mutexes and guarded fields stay private. Reach private fields/methods only through `m` (receiver or constructor value), never another object or chains such as `m.opts.log` (write `m.opts.Log`); expose a method/field instead. Same-type parameters are allowed. `private` is an identifier-based convention, enforced by `lint/style/` via hooks, `make lint-go`, and CI; `import "C"` files are exempt.
- **`stylelint`** gates `logger`, `private`, `testpkg`, `receiver`, `loopindex`, `maplit`, `grpcdial`, `sugar`, `zapmsg`, `zapkey`, `testctx`, `handlerblank`, `barenew`, and `loggerlast`. Each live violation has one reasoned `<check>:<path>:<name>` row in `lint/style/allowlist.txt`; do not add rows. Check scopes are declared with the checks.
- **The allowlist is self-cleaning**: stale rows fail, so delete them with the code fix. Fix by shape: read-only owner data → exported field, repeated behaviour → method, duplicate carrier → delete. A wrong whole-class rule needs an exemption; a justified instance needs a reasoned row. Positive-control with `-allowlist <(git show HEAD:lint/style/allowlist.txt)`.
- **gRPC handlers**: never use `_` for `ctx` / `req` — name them.
- **No log-only RPC stubs**: when a brief names an RPC, actually invoke the client. `m.log.Debug("would call …")` is a bug, not a stub.
- **Comments**: English, period-terminated, about 80 columns, and list production callers only. Doc comments begin with a one-sentence period-terminated brief and separate detail with a blank `//` line.
- **Tests**: table-driven with `require.NoError(t, err)`; production comments never mention tests. `_test.go` uses `package <pkg>_test` and `testpkg` gates it; `package main` is exempt because it is unimportable.

### Rust

- `.rustfmt.toml` uses nightly-only options (`wrap_comments`, `format_code_in_doc_comments`, `imports_granularity`, `group_imports`). Always use `cargo +nightly fmt`.
- Run `cargo +nightly fmt -- --check` and `cargo clippy` before committing.
- Proto compilation needs `protobuf-compiler` in CI.
- **Proto crates**: tonic-include crates expose `pub mod pb`, never `pub mod <crate>`; consume shared `common/rust/` crates through `extern_path`.
- **Orphan rule**: never implement a foreign trait for a foreign type. Give the CLI a local enum/wrapper, implement the foreign trait there, then `From<Local> for Foreign`; free functions are not a substitute.
- **Visibility**: avoid `pub(crate)`; items are `pub` or private. Conceptual type API methods are `pub`, even in binaries.
- **Wire vs domain types**: parse and check invariants in the domain type; wire types get `From<Domain>` and use `TryFrom` only when fallible. Confirm module-specific validation (ACL permits non-contiguous masks, forward/decap do not) before generalising.
- **`Display`/`Serialize`**: own types implement `Display`; `Serialize` uses `serializer.collect_str(self)`. Never blanket-derive `Serialize` for a proto module with a manual implementation.
- **`fmt` imports**: `use std::fmt::{self, Display, Formatter};` with explicit `Result<(), fmt::Error>` (not `fmt::Result` alias).
- **No doc comments** on `Display`/`Serialize`/`TryFrom`/`From`/`Debug`/ `Default`/`FromStr` impls — the trait name is the doc.
- **Doc comments**: `///`/`//!` begin with a one-sentence period-terminated brief and separate detail with a blank `///` line.
- **No infallible `TryFrom`**: replace with `From`, or remove the impl if the call site is trivially inlinable.
- **`assert_eq!` order**: expected first, actual second: `assert_eq!(expected, actual)`.
- **Style**: prefer shadowing to `_str`, destructure `self` rather than `self.0`, put bounds in `where`, and import types directly.
- **Struct literals**: follow declaration order, including generated protos; rustfmt/clippy do not check this.
- **Empty CLI results**: use `output::empty`/`empty_with_hint`, never bare printing or call-site format guards. The primitive owns the stderr marker, `No <subject> found.` register, and non-TTY/serializing suppression.

### C

- Always use braces for `if`/`else`/`for`/`while`, even single-line bodies.
- Format with `clang-format`.
- **Functions with more than six parameters are a code smell.** Split them or use a designated-initializer config struct; omnibus initialisers are untestable.
- **Multi-segment mbufs**: `rte_pktmbuf_data_len()` is head-only. Whole-packet work walks `mbuf->next`/uses `rte_pktmbuf_pkt_len()`, or rejects chained packets.
- **Zero config limits mean unset/no clamp.** Never use the sentinel in min/subtraction arithmetic; clamp accepted degenerate values below internal header deltas.
- **Write recycled wire fields at their declared width.** A partial write retains stale bytes.
- **`memory_balloc` does not zero.** `memset` allocations whenever a sentinel, CGO-visible field, or index depends on zero/NULL.
- **cgo-boundary headers stay DPDK-free.** `<rte_*.h>` in `packet.h`, `packet_front.h`, `lib/utils/packet.h`, or another cgo-path header is blocking.

### Tests & Benchmarks

- **Benchmarks must exercise their claimed path**: traffic matches rules (protocol, device, address family), sampling covers the dataset (no `stride == 1` prefix bias), and every documented input mode works end-to-end.
- **dataplane_ut `Bench`/`run_rounds` recycles fixed packets**: reject or neutralise allocating/emitting actions (for example ACL `CREATE_STATE` sync) so mbufs do not leak or distort results.
- **Exported Go APIs whose arguments cross CGO into C-side array indexing** (device IDs, queue/worker indices) validate the range on the Go side before the call.
- **C-only changes require `go test -count=1` and a clean `meson compile`.** Go can link a stale C archive after cached tests or a failed compile; inspect compile output first. Mutation tests must corrupt behaviour, not delete calls. Shared-memory layout or `YANET_MODULE_ABI_VERSION` changes require `go clean -cache`, `rm -f <binary>` for standalone CGO binaries, and removal of `/dev/hugepages/yanet*` plus cached VM baselines.
- **Race tests need targeted `-run` repetition (about 10–20 runs).** One `go test -race -count=1` can miss or misattribute a race through `t.Parallel()` siblings.
- **Do not pin human-readable CLI formatting in tests.** Test logic/invariants (state, staleness, rounding, widths, wrapping), not literal output. ASCII-only paths and canonical MAC/hex rendering are contracts and may be tested.

### TypeScript/React

Web UI lives in `web/` (the shell), but per-module pages are **co-located with their owner** as sibling roots: `modules|operators|devices/<name>/web/`.

- Prefer arrow function expressions.
- **A co-located spec only runs because `web/vite.config.ts` lists those sibling roots** — vitest's default `include` is web-relative, so a `*.test.ts` added or moved there silently stops running in CI. Diff the collected test-file count on any such move.
- **Browser-visible changes need a real Playwright run on the real path plus an inspected screenshot**; `--list` is not verification. A pixel-diff of two empty states reports a false 0 — assert row counts first.

### Commits & PRs

- **Never run `git commit`/`--amend` unless the current request asks for it.** Default: stage nothing, return the diff plus a verification summary.
- **Never `git stash`/`checkout`/`reset`/`restore` to A/B a pre-existing state.** `stash push -- <paths>` aborts on untracked files, a later bare `pop` pops an unrelated stash, and in a shared dirty worktree this destroys another agent's work. Read HEAD instead (`git show HEAD:<path>`, `git diff HEAD -- <path>`) or materialise a baseline out of tree (`git archive <ref>`, symlinking `subprojects/{dpdk,libpcap}` back in). If you suspect a failure is pre-existing, say so and stop.
- Commit format: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): brief description` with high-level description (no code-level details, no backtick-quoted symbol names).
- **Do not** add `Co-Authored-By` or `Generated with` footers for Codex or Claude Code.
- PR title: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): <short description>` — MUST include the scope, exactly like commit subjects. A scopeless `feat: …` title is a convention violation.
- Scope is mandatory (lowercase, comma-separated for multiple scopes); an optional `!` before the colon marks a breaking change (e.g. `feat(route)!: ...`); the brief must not start with an uppercase letter and carries no trailing period. This is enforced mechanically by the `commit-msg` hook (`make hooks`) and the Commit Lint CI job (`lint/commit/`).
- PR body: bullets start with capital, end with period. Add `Closes #<number>.` when applicable. **Do not** include a `## Summary` header — content goes directly. **Do not** include a `Test plan` section. PR descriptions have no 80-char line limit.
- **Reach GitHub through the `github` MCP server rather than `gh`**, where a GitHub MCP server is granted. Agents without a GitHub MCP server in their tool allowlist keep using read-only `gh`. Reads (PR/issue/run state, diffs, review threads, job logs) and, on a write-capable server, writes (create/update/merge PRs, comments, review-thread resolution, workflow reruns) all have tools, and they return structured data instead of shell-parsed text. `gh` stays correct only for the enumerated gaps — continuous check watching, `--admin` merge bypass, REST/GraphQL endpoints with no tool, and `gh auth refresh`. Record the reason on every fallback.
  - The two-server split is Claude-Code-specific: `github` (full read+write) is granted only to the architect, and `github_ro` (read-only) is granted to the reviewer, planner, and bug-hunter; the remaining agents hold neither and use read-only `gh`.
  - Codex defines a single `mcp_servers.github` server, shared by every Codex agent including the reviewer, with no per-agent scoping and no read-only mode — it is write-capable end to end, so on Codex the reviewer's read-only discipline is a charter obligation the agent enforces on itself, not a mechanical guarantee from the server.
- **MCP file-write tools (`create_or_update_file`, `push_files`, `delete_file`, `create_branch`) never substitute for local git**: they commit server-side, bypassing staging discipline, the `commit-msg` hook, and local verification gates.

## Agent Memory & Feedback

Client memory is root-local and client-specific: Codex uses `<repo>/.agent-state/agent-memory/<agent>/`; Claude Code uses `<repo>/.claude/agent-memory/<agent>/`. Codex worktrees are `<repo>/.agent-state/worktrees/`. Never put either tree beneath a subdirectory or redirect one client into the other's tree.

### Structure

- **One lesson per `kebab-case-slug.md` file.** Its first line is a summary of at most 150 characters (imperative for rules, excluding index wrapper), byte-identical to its `MEMORY.md` index line. After a blank line, include the rule/fact, `Why:`, and when needed `How to apply:`. No YAML frontmatter; keep lessons under about 20 lines.
- **`MEMORY.md` is only an index.** It is auto-loaded and has `# <agent> memory`, then one `- [<summary>](<slug>.md)` line per lesson, grouped under optional `## Rules`, `## Project context`, and `## References` headings with optional `###` subheadings. Keep it at most 200 lines; never put lesson bodies there.
- User-profile facts are lesson files too, summary prefixed `User: …`, indexed under `## Rules`.

### What to record

- **Record corrections and confirmed approaches.** Include why each matters so future readers can judge edge cases.

### What NOT to save

- Anything already in `AGENTS.md`, the repo, or chat history: code patterns, paths, architecture, git history, and debugging recipes.
- Ephemeral task state, TODOs, and design logs; use plans, `.arch/`, or `TODO.md`.

### Hygiene

- **Update, do not duplicate.** Scan first; update a matching lesson and append `(seen: N)` to both identical summaries, beginning at `(seen: 2)`. At `(seen: 3)`, add the rule to the relevant AGENTS section and delete the lesson and index row.
- **Verify notes against code.** Delete or correct stale/wrong notes.
- **Compact by merging, never dropping facts.** Fold related lessons into one digest while keeping live bug classes and recipes separately discoverable; merged digests may be longer. Keep index summaries as retrieval hooks, not findings. Promote only terse, broadly applicable rules; agent-specific rules stay in that agent's section.

### Delegation & verification (architect)

These rules bind the delegating agent, not the implementer.

- **Verify specialist claims yourself.** Run real builds/tests; reproduce mechanism claims, pre-existing failures, flakes, and new diagnostics before acting.
- **Brief only verified details.** Read and cite code, and falsify exclusionary premises with a probe. Trace transitive callers/pointers before accepting reachability claims; preconditions describe state, not reachability.
- **Require whole-class audits.** A `file:line` sample is not the population: pair it with the defining grep. Map all sites and callers before modifying shared primitives, return values, or a named data-flow site.
- **Review the complete candidate with `review-change`.** Give the reviewer the task brief, exact base, candidate worktree or PR, and full intended manifest, including explicit untracked or ignored paths. Classify every entry as publish or local-only and stage only the publish manifest. Keep the first pass independent of existing review comments. Record the approval fingerprint (base, manifests, modes/types, content) and, for a PR, its head SHA; recheck immediately before the verdict. Any fingerprint change requires a whole-candidate restart. Stage/commit/amend/push may carry approval only after the publish fingerprint is identical on the packaged side and local-only entries remain unchanged; a new commit SHA alone is not a content change.
- **Launch agents from the repo root.** They inherit cwd, and worktrees lack gitignored state such as `.arch/`, `.agent-state/agent-memory/`, and `.claude/agent-memory/`. Before removing a worktree, salvage both memory trees and establish each participant's tree before disputing file existence.
- **Check open PRs before briefing and inspect their diffs.** If one owns the zone, drive or review it rather than competing.
- **Reconcile the safety queue every roughly ten merges** and after consecutive `refactor`/`docs` work; re-verify audit ledgers against HEAD before ranking.
- **Production is the oracle.** A mechanism replay can false-positive and an unrealistic timing model can false-green; instrument production when a repro and live system disagree.
- **A review finding binds, its suggested remedy does not.** Diagnose the defect and choose the minimal fix; reconsider the premise when the remedy is disproportionate.
- **After a design pivot, re-derive inherited constraints.** Do not retain dead or mutually unsatisfiable requirements.
- **Route non-application work to `coder-c` with a charter note.** State that ops/packaging/CI work is not C and no C/Go/Rust/TS/proto/meson source changes. Do not route infrastructure to `coder-ui`. For untracked/gitignored trees, have reviewers inspect explicit on-disk paths rather than relying on `git diff`.

## Key Dependencies

- **DPDK**: v23+ (submodule)
- **Go**: 1.24.13+
- **Rust**: 1.84+ (nightly for formatting)
- **Meson**: 0.61+
- **Protobuf**: 3.0+ (protoc-gen-go >=1.36.5, protoc-gen-go-grpc >=1.5.1)
