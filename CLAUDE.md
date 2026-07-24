# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
make lint-go                  # loglint + enclint + golangci-lint modernize (lint/logger/, lint/encapsulation/, .golangci.yml)
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

Modules in `modules/` follow one of two layouts. New modules use the
**canonical** form; legacy modules are gradually migrated.

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

`acl`, `blackhole`, `mirror`, `nat64`, and `route-mpls` have migrated to
this layout too; of those, `acl` and `blackhole` have no `fuzzing/` yet,
and `route-mpls` has neither `tests/` nor `fuzzing/`.

**Legacy** (pdump): no `bindings/`, CGO calls live directly in
`controlplane/ffi.go`, no `backend.go`. `fwstate` is partially
migrated: it has `bindings/go/` but still routes CGO through
`controlplane/ffi.go` and has no `backend.go`.

**Early-stage** (balancer2): has `api/`, `bindings/go/`, `dataplane/`,
and `cli/`, but its `controlplane/` holds only `balancerpb/` — no
`mod.go`/`cfg.go`/`service.go`/`backend.go` — and it has no `tests/`
or `fuzzing/`.

Module dataplane symbols are exported via meson linker defsym: `new_module_<name>`.

Active modules: `route, acl, balancer2, blackhole, forward, decap, nat64,
fwstate, dscp, pdump, route-mpls, mirror`.

### Devices

`devices/` mirrors `modules/` layout (`api/`, `controlplane/`, `dataplane/`,
`cli/`) but for device adapters rather than packet-processing modules.
Active devices: `plain`, `vlan`, `trafgen`.

### Operators

`operators/` holds long-running Go control-plane processes that orchestrate
the dataplane through the gateway, distinct from per-module gRPC services.

- `operators/pipeline` — declarative reconciliation operator (`cmd/`,
  `internal/`, `operatorpb/`, Rust `cli/`). Its structural template has been
  replicated by per-module operators `operators/{decap,forward,route}`, each
  with `cmd/` + `internal/` + `operatorpb/` + a Rust `cli/` (route ships two
  CLI crates: `cli/route` and `cli/neighbour`).
- `operators/bird-adapter` — BIRD routing-daemon adapter (canonical agent
  layout: `adapterpb/`, `internal/`, `service.go`). Note:
  `modules/route/bird-adapter/` is a separate proto-contract subtree
  (`adapterpb/`, `proto/`) consumed by the agent — not a duplicate binary.

### Shared Libraries

- `common/go/` — Go support packages: `xcfg`, `xcmd`, `xerror`, `xiter`,
  `xnetip`, `xpacket`, `logging`, `metrics`, `dataplane`, `bitset`,
  `maptrie`, `rcucache`, `testutils`.
- `common/rust/` — Rust shared crates: `commonpb`, `filterpb`, `ynpb`
  (compiled ynpb protos, exposes `pub mod pb`), `bitmap`. Module CLIs
  depend on these via `extern_path` instead of recompiling protos.
- `common/commonpb/` — Go protos: `metric`, `target` (used by the
  metrics package).
- `common/filterpb/` — Go filter proto plus helpers (`convert.go`,
  `filter.go`).
- `common/btree/` — header-only C BTree (`u16.h`, `u32.h`, `u64.h`).
- `common/ttlmap/` — header-only C TTL map (`ttlmap.h` + `detail/`).
- `common/*.h` — C headers: `lpm.h`, `radix.h`, `crc32.h`, `hash.h`,
  `rcu.h`, `memory*.h`, etc.

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
- **Local Makefile**: `cli/Makefile` runs `cargo build/clippy/fmt`
  scoped to the CLI workspace without leaving the directory.

### Build System

Meson orchestrates C/DPDK builds and Go binary compilation (via `custom_target` with `go build`). Rust is built separately via Cargo. DPDK is a Meson subproject in `subprojects/dpdk/`. Sanitizer flags are propagated to CGO automatically when using `-Db_sanitize`.

## Coding Conventions

### General (every language — C, Go, Rust, TypeScript, shell, Makefiles, proto, YAML)

- **No banner / section-separator comments. Anywhere. In any language.**
  Lines like `// --- foo ---`, `# === foo ===`, `# --- validation ---`,
  `// ----------`, `/* ===== */`, ASCII box-art, or any horizontal divider
  used to label a block are forbidden — in source, scripts, configs, and
  tests alike. This explicitly includes shell scripts, Makefiles, and proto
  files, not just C/Go/Rust/TS. If a file feels long enough to want visual
  dividers, split it into smaller files or functions instead. This is a hard
  rule the user has repeated many times across C, Rust, Go, and shell; treat
  any occurrence as a blocking review finding, never a non-blocking nit.

- **Doc-comment shape (fields, structs, functions) — every language.** A short
  one-line brief (what/why), then a blank comment line, then the detail
  paragraph (concise, no fanaticism). Never glue brief and detail into one
  run-on block, and never cram multiple ideas onto consecutive comment lines
  without the blank separator. Applies to `//`, `///`, `#`, and `/* */`.
  Single-sentence comments need no blank line; inline implementation comments
  inside function bodies are exempt. This is a hard rule the user has repeated
  many times (C and Go alike); treat a crammed multi-idea doc comment as a
  blocking review finding, not a nit.

### Go

- **Receiver names**: always `m`. No type-letter mnemonics.
- **No abbreviated identifiers** — spell them out (`labels` not `lbls`,
  `metrics` not `mtr`, `durationSeconds` not `durSeconds`), in production
  code and tests alike. Keep only the universal Go idioms: `ok`, `err`,
  `ctx`, `idx`, and short-scope type-assert temporaries.
- **Naming**: `*Config` (not `*Cfg`); constructors are `NewStore`,
  `NewClient` — never bare `New`.
- **Loop index**: use `idx`, not `i`, in `for`-range and indexed loops.
- **Maps**: `map[K]V{}` not `make(map[K]V)`.
- **gRPC**: `grpc.NewClient` not `grpc.Dial`.
- **Concurrency**: prefer `errgroup.Group` over `sync.WaitGroup`,
  including in tests.
- **Logging (zap)**: structured, lowercase messages, snake_case keys,
  typed fields (`zap.String`, etc.). Use `*zap.Logger` (not Sugared).
  `log *zap.Logger` is the **last** field of the struct, after all
  other fields. Per-instance context via `zap.With` on the struct
  logger; avoid count/elapsed noise. `Info` = a just-completed state
  change in past tense.
- **Constructors and methods accepting `*zap.Logger` MUST use options
  pattern**: `NewFoo(cfg, WithLog(log))`. Inside the constructor:
  `opts := newOptions(); for _, o := range options { o(opts) }`.
  Parameter is `options ...Option`, never renamed to `opt`/`optsList`.
  `WithLog()` is defined per constructor. Enforced mechanically by
  `lint/logger/` (`make lint-go`); known violations are ledgered in
  `lint/logger/allowlist.txt` — do not add new rows.
- **Encapsulation**: mutex and the fields it guards stay private. A
  private field or method may only be reached through a base spelled
  `m` — the receiver, or the value under construction in a
  constructor. Any other base is a violation: another object, or a
  chain such as `m.opts.log` (write `m.opts.Log`). The fix is to give
  the type an exported method or field, even when the type itself is
  unexported: only an exported surface makes decomposition
  reviewable. The check keys on the identifier `m` rather than on
  true receiver identity, so it is a convention aid, not a proof.
  Enforced mechanically by `lint/encapsulation/` (pre-commit hook via
  `make hooks`, `make lint-go`, and CI via the `enclint` job in
  `go-lint.yml`); known violations are ledgered in
  `lint/encapsulation/allowlist-private.txt` — do not add new rows.
  Files that `import "C"` are exempt, because C struct fields cannot
  be distinguished from Go private fields without type information.
  A method may also reach into the private fields or methods of a
  parameter declared with the same type as its receiver, because an
  operation on another value of one's own type crosses no
  encapsulation boundary.
- **gRPC handlers**: never use `_` for `ctx` / `req` — name them.
- **No log-only RPC stubs**: when a brief names an RPC, actually invoke
  the client. `m.log.Debug("would call …")` is a bug, not a stub.
- **Comments**: English, end with period, fit within ~80 chars
  (reflow rather than preserving narrower fill). List only production
  callers, not "tests".
- **Doc comments**: first line is a single-sentence brief ending with
  period. If detail follows, separate with a blank `//` line, then the
  body paragraph. Never glue brief and detail on consecutive `//` lines.
- **Tests**: table-driven, use `require.NoError(t, err)`. Do not
  reference tests inside production-code comments. Every `_test.go`
  file declares `package <pkg>_test`, so the compiler — not a
  linter — forbids reaching into private fields and functions; known
  violations are ledgered in
  `lint/encapsulation/allowlist-testpkg.txt` — do not add new rows.
  A `package main` command is exempt, because main packages are
  unimportable, so an external test could never reach anything.

### Rust

- `.rustfmt.toml` uses nightly-only options (`wrap_comments`,
  `format_code_in_doc_comments`, `imports_granularity`, `group_imports`).
  Always use `cargo +nightly fmt`.
- Run `cargo +nightly fmt -- --check` and `cargo clippy` before committing.
- Proto compilation needs `protobuf-compiler` in CI.
- **Proto crates**: tonic-include crates expose `pub mod pb`, never
  `pub mod <crate>`. Consumers depend on shared crates in `common/rust/`
  via `extern_path` rather than recompiling protos.
- **Orphan rule**: `impl ForeignTrait for ForeignType` is forbidden
  (e.g., `ValueEnum for ynpb::pb::LogLevel`). Define a local enum/wrapper
  in the CLI with the foreign trait, then `impl From<Local> for Foreign`.
  Free functions are not a substitute.
- **Wire vs domain types**: parsing and invariant-checking live on the
  domain type. The wire type (proto-generated) gets multiple
  `From<Domain>` impls; `TryFrom` is only used when fallible. Validation
  semantics differ per module — confirm before generalizing
  (e.g., acl accepts non-contiguous masks; forward/decap do not).
- **`Display` and `Serialize`**: own-crate types implement `Display`;
  `Serialize` delegates via `serializer.collect_str(self)`. Never blanket
  `#[derive(Serialize)]` on a proto module if any type has a manual impl.
- **`fmt` imports**: `use std::fmt::{self, Display, Formatter};` with
  explicit `Result<(), fmt::Error>` (not `fmt::Result` alias).
- **No doc comments** on `Display`/`Serialize`/`TryFrom`/`From`/`Debug`/
  `Default`/`FromStr` impls — the trait name is the doc.
- **Doc-comment structure**: `///` / `//!` blocks lead with a
  single-sentence brief ending with period. If detail follows, separate
  with a blank `///` line, then the body paragraph. Never glue brief
  and detail on consecutive `///` lines.
- **No infallible `TryFrom`**: replace with `From`, or remove the impl
  if the call site is trivially inlinable.
- **`assert_eq!` order**: expected first, actual second:
  `assert_eq!(expected, actual)`.
- **Style**: prefer shadowing over `_str`-suffixed intermediates.
  Use `match self { Self(v) => … }` or `let Self(v) = *self;` over
  direct `self.0`. Trait bounds in `where` clauses, not inline.
  Import type names directly (`use serde::Serialize;` then `T: Serialize`),
  not module-qualified (`serde::Serialize`).
- **Struct-literal field order**: list fields in the same order they are
  declared in the struct definition. Applies to proto-generated structs
  too — e.g. if the message declares `nexthop_addrs` (tag 3) before
  `do_flush` (tag 4), the literal must place `nexthop_addrs` before
  `do_flush`, not append it last. `rustfmt`/`clippy` do not catch this.

### C

- Always use braces for `if`/`else`/`for`/`while`, even single-line bodies.
- Format with `clang-format`.
- **Functions with > 6 parameters are a code smell.** Split into smaller
  composable primitives, or introduce a config struct (`struct foo_config`
  + designated initializer) to bring the call site to 3–4 args. "Omnibus
  init" functions (16–17 args) are the wrong shape for testable C.
- **Multi-segment mbufs**: `rte_pktmbuf_data_len()` covers only the head
  segment. Whole-packet operations (snapshot/restore, checksum, copy) must
  walk the `mbuf->next` chain / use `rte_pktmbuf_pkt_len()`, or explicitly
  reject chained packets.
- **Zero-valued config limits mean "unset / no clamp"** across the dataplane.
  Never feed the sentinel into min/subtraction arithmetic, and clamp (do not
  bypass) accepted-but-degenerate values below internal header deltas.
- **Write wire-format fields at their full declared width** when recycling a
  header in place: a 16-bit store into a reused 32-bit field leaves stale
  upper bytes and a malformed packet.

### Tests & Benchmarks

- **A benchmark must demonstrably exercise the path it claims to measure**:
  synthesized traffic matches the ruleset (protocols, device scoping,
  address families), sampling covers the whole dataset (no integer-division
  `stride == 1` prefix bias), and every documented input mode works
  end-to-end.
- **dataplane_ut `Bench`/`run_rounds` recycles a fixed packet set**: module
  actions that allocate or emit packets (e.g., ACL `CREATE_STATE` sync)
  must be rejected or neutralized in benchmarks, or they leak mbufs and
  shift what is measured.
- **Exported Go APIs whose arguments cross CGO into C-side array indexing**
  (device IDs, queue/worker indices) validate the range on the Go side
  before the call.
- **Validating a C-only change needs `go test -count=1` AND a verified-clean
  `meson compile`.** Go's test cache keys on Go sources and arguments, not on
  the C archive it links, so a cached PASS can replay against a stale object.
  A failed compile is the same trap from the other side: the previous archive
  stays on disk, so even `-count=1` links stale objects and goes green. Grep
  the compile output for failures before believing any test verdict — a stale
  PASS reads exactly like "this assertion cannot fail", which is the opposite
  of the truth. Mutation checks must therefore corrupt behaviour (flip a
  condition, self-assign) rather than delete calls, which trips `-Werror` on
  the now-unused function and silently leaves the good archive in place. A
  shared-memory struct layout change is a different problem and needs a full
  `go clean -cache`.
- **Human-readable CLI output is NOT part of the contract — never pin its
  format in a test.** No `test_*_output` test that builds a response and
  asserts the formatted block, and no assert on a literal format string
  inside a helper test either (`assert_eq!("2h 46m", format_age(..))`,
  `assert_eq!("3/5 ready · 1 degraded", summary_line(..))`). The look
  keeps changing, so such tests are pure churn. Test the LOGIC instead —
  a state diff, a staleness predicate, rounding bands, a width clamp,
  word wrapping. Where a real invariant hides behind a string, assert
  the invariant: "the age renders as at most two whitespace-separated
  components" survives a format change; `assert_eq!("1year 1month", ..)`
  does not. Two things ARE fair game, because they are contracts rather
  than looks: a behavioural guarantee such as "the ASCII path emits zero
  non-ASCII bytes" (for pipes and non-UTF-8 locales), and value
  contracts such as a canonical MAC rendering or a hex round-trip.

### TypeScript/React

Web UI lives in `web/` (`package.json`, `index.html`, `dist/`).

- Prefer arrow function expressions.

### Commits & PRs

- Commit format:
  `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): brief description`
  with high-level description (no code-level details, no
  backtick-quoted symbol names).
- **Do not** add `Co-Authored-By: Claude …` / `Generated with Claude Code`
  footers.
- PR title:
  `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>): <short description>`
  — MUST include the scope, exactly like commit subjects. A scopeless
  `feat: …` title is a convention violation.
- Scope is mandatory (lowercase, comma-separated for multiple scopes); an
  optional `!` before the colon marks a breaking change (e.g.
  `feat(route)!: ...`); the brief must not start with an uppercase letter
  and carries no trailing period. This is enforced mechanically by the
  `commit-msg` hook (`make hooks`) and the Commit Lint CI job
  (`lint/commit/`).
- PR body: bullets start with capital, end with period. Add
  `Closes #<number>.` when applicable. **Do not** include a
  `## Summary` header — content goes directly. **Do not** include a
  `Test plan` section. PR descriptions have no 80-char line limit.

## Agent Memory & Feedback

**`<REPO_ROOT>/.claude/agent-memory/<agent>/`** — one memory directory per agent, **always at the repository root**, never under a subdirectory like `web/.claude/…` or `controlplane/.claude/…`. The path is `<repo>/.claude/agent-memory/<agent>/` regardless of the agent's current working directory. If you would write to a `.claude/` path that is not directly under the repo root, you are wrong — walk up to the repo root first.

### Structure

- **One lesson per file**, named `kebab-case-slug.md`. The **first line is a one-line summary** of the lesson (≤ 150 chars, imperative for rules). After a blank line, a short body: the full rule or fact, a `Why:` line (what happened and why it mattered — a correction, an incident, a confirmed approach), and a `How to apply:` line when the trigger isn't obvious from the rule. No YAML frontmatter. Keep a lesson file under ~20 lines.
- **`MEMORY.md` is a pure index, not a memory.** It is the only auto-loaded file, so keep it tight: `# <agent> memory` heading, then one line per lesson: `- [<one-line summary>](<slug>.md)`. Group under optional `## Rules` / `## Project context` / `## References` headings; optional `###` topic sub-headings are allowed within them but count toward the cap. Hard cap: 200 lines (auto-load truncates beyond that). Never write lesson bodies into `MEMORY.md`.
- User-profile facts are lesson files too, summary prefixed `User: …`, indexed under `## Rules`.

### What to record

- **Corrections and confirmed approaches alike.** Corrections ("don't do X", "stop doing Y") are easy to notice; confirmations ("yes, exactly", a non-obvious choice accepted without pushback) are quieter — record both, or you will avoid past mistakes while drifting away from approaches already validated.
- **Always include why it mattered.** The `Why:` line is what lets a future reader judge edge cases instead of blindly pattern-matching the rule.

### What NOT to save

- Anything already in this `CLAUDE.md` (Coding Conventions, Architecture, etc.) — duplicates waste tokens.
- Anything the repo or chat history already records: code patterns, file paths, architecture (read the code); git history (`git log` / `git blame`); debugging fix recipes (the fix is in the code, the context in the commit message).
- Ephemeral task state, TODOs, design logs — those go in plans, `.arch/`, or `TODO.md`.

### Hygiene

- **Update an existing note rather than create a duplicate.** Before writing, scan the index for a note on the same lesson; if found, update that file in place and append `(seen: N)` to its summary (in both the file's first line and the index line), starting at `(seen: 2)`.
- At `(seen: 3)` the lesson graduates into this `CLAUDE.md`: add it to the appropriate section, then delete the lesson file and its index line.
- **Delete notes that turn out to be wrong or stale.** Before acting on a note, verify the referenced file/symbol still exists — trust the code over the note, and remove or fix the note when they disagree.
- When updating a note, keep its first line and its index line identical.

### Delegation & verification (architect)

Graduated from architect memory after recurring three or more times. These bind the agent that delegates work, not the agent that writes it.

- **Verify every specialist claim yourself.** After each round, run the real build/test rather than trusting the report. A mechanism explanation in a comment, a "pre-existing failure", and a "flake" are all unverified until you reproduce them. This extends to injected diagnostics: "new" errors surfaced against a worktree or a stale LSP index are frequently phantom — confirm against a real build before acting.
- **Never put an unverified detail in a brief.** A rationale, a magic token, or the semantics of an existing flag will be implemented verbatim, so anything you cannot cite from the code is a defect you are authoring. Read it first or leave it out.
- **Demand a whole-class audit at brief time.** When a change introduces a hazard class, require the specialist to enumerate and fix the whole class in one pass. A reviewer's (or Codex's) `file:line` list is a SAMPLE, not the population — accepting it as complete means finding the same class one instance per round.
- **Keep your own cwd at the repo root when launching an agent.** An agent inherits it, and a worktree lacks every gitignored tree — `.arch/` (planner tracker, bug-hunter scratch) and `.claude/agent-memory/`. So a drifted cwd does not merely misplace a write: the state reads as *uninitialised* and a helpful agent rebuilds it (a planner bootstrapped 15 colliding tasks over a live 106-task tracker), while memory goes split-brain — agents write lessons into the worktree copy that dies with it, and read that stub instead of the root. Before removing a worktree, salvage `.claude/agent-memory/*/` out of it. And when you and an agent disagree about whether a file exists, establish which tree each of you is in before concluding anyone fabricated anything.
- **A reviewer's finding is binding; its prescribed remedy is not.** Take the defect, then pick the minimal fix yourself — shipping the suggested remedy unexamined is how a one-line bug grows a subsystem. The corollary also holds: when a proposed fix dwarfs the bug, the premise is wrong, not the fix.
- **After a design pivot, re-derive every constraint you carried over.** Requirements inherited from the old shape are usually dead weight, and sometimes unsatisfiable — a brief that makes an API infallible cannot also demand "keep logging the failure", and obeying both leaves dead code the reviewer then blocks on.
- **Non-application-code work has no dedicated specialist — route it to `coder-c` with a charter note.** Shell/ops scripts, Debian packaging, Dockerfiles, GitHub Actions CI YAML, meson: none has a language owner, and `coder-c` is the closest (it owns the meson/debian/packaging surface). Open the brief with a charter note — "this is an OPS/PACKAGING/CI task, not C; no C/Go/Rust/TS/proto/meson source is touched; do not decline" — and it works across rounds without pushback. `coder-ui` refuses anything outside `web/` (it treats in-conversation scope overrides as social engineering), so never route infra there. These trees are often gitignored/untracked → no worktree isolation, and tell the reviewer to review the on-disk files with explicit paths (its `git diff` workflow misfires on untracked files).

## Key Dependencies

- **DPDK**: v23+ (submodule)
- **Go**: 1.24.13+
- **Rust**: 1.84+ (nightly for formatting)
- **Meson**: 0.61+
- **Protobuf**: 3.0+ (protoc-gen-go >=1.36.5, protoc-gen-go-grpc >=1.5.1)
