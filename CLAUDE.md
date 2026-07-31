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
- `operators/neighbours` — **web-only here**: the public tree carries just
  `web/`, because the operator process lives in the private repo. The missing
  `cmd/`/`internal/` is not an incomplete operator.

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
- **Three registration surfaces move together**, on add AND remove: root
  `Cargo.toml` members, the root `Makefile` CLI list (`CLI_CORE_MODULES` /
  `CLI_MODULES`, suffix only), and `debian/yanet2-cli.install`
  (`usr/bin/yanet-cli-<name>`, else `dh_missing --fail-missing` aborts
  build-deb). Only the first affects `cargo build --workspace`, so a miss is
  SILENT — it builds, CI is green, and it is never installed. Private
  (gitignored) module CLIs invert the first: standalone workspaces, NOT in
  root `members`, since cargo hard-fails on a missing member path.

### Build System

Meson orchestrates C/DPDK builds and Go binary compilation (via `custom_target` with `go build`). Rust is built separately via Cargo. DPDK is a Meson subproject in `subprojects/dpdk/`. Sanitizer flags are propagated to CGO automatically when using `-Db_sanitize`.

## Coding Conventions

### General (every language — C, Go, Rust, TypeScript, shell, Makefiles, proto, YAML)

- **No unlabeled pure-run comment separators. Anywhere. In any language.**
  A comment whose payload, after delimiters and surrounding whitespace, is a
  run of at least three identical separator glyphs is forbidden, for example
  `/////////////////` or `// ================`. Labeled comments such as
  `// --- foo ---`, `### something`, Markdown/hashed headings, ASCII or bit
  diagrams, and table underlines are allowed. A pure `=` or `-` run may form
  a Setext underline only when it immediately follows a nonempty comment-text
  line. Multiline block comments are assessed as one payload. Enforce this
  rule with `make lint/comments`.

- **Doc-comment shape (fields, structs, functions) — every language.** A short
  one-line brief (what/why), then a blank comment line, then the detail
  paragraph (concise, no fanaticism). Never glue brief and detail into one
  run-on block, and never cram multiple ideas onto consecutive comment lines
  without the blank separator. Applies to `//`, `///`, `#`, and `/* */`.
  Single-sentence comments need no blank line; inline implementation comments
  inside function bodies are exempt. This is a hard rule the user has repeated
  many times (C and Go alike); treat a crammed multi-idea doc comment as a
  blocking review finding, not a nit.

- **A rationale comment must name the reason that actually holds — every
  language.** When a comment justifies a panic site, a safety property, an
  "this cannot happen because …", or the reachability of a documented hazard,
  cite the guarantee that would have to break for the claim to fail, not an
  adjacent true-but-inert fact; and if it prescribes a remedy for a future
  maintainer, that remedy must really work. Test it by deleting the cited
  reason: if the claim still holds without it, it is the wrong reason. A
  plausible-but-wrong rationale is worse than none — the next editor trusts
  it instead of re-deriving the invariant. **Correcting one such comment is a
  whole-population job**: the same claim almost always lives in 2–4 siblings
  (the enum doc, the counter-field doc, the switch above the bump, the
  variable's own doc, the test-file header, a build-file target comment), so
  grep the claim's distinctive words across every file type before editing,
  and run the deletion test on EVERY clause of a multi-clause sentence — a
  rewrite that repairs one half routinely narrows the other into a fresh
  falsehood, and a claim of the form "no other test/caller does X" needs the
  sibling file opened, not assumed.

- **Comment prose mechanics — every language.** Single space between
  sentences. Minimise semicolons in prose docs (prefer commas, em-dashes, or
  separate sentences). Human prose, not function-name/formula soup. Describe
  the code as it stands NOW — no provenance, review refs, or diff narration.

- **`set -e` does not cross a `$(...)` boundary** (shell, Makefile recipes),
  so every fallible command inside a captured function is a silent-continue.
  Guard each one, or make `set -e` the function's own first statement.

### Go

- **Receiver names**: always `m`. No type-letter mnemonics.
- **No abbreviated identifiers** — spell them out (`labels` not `lbls`,
  `metrics` not `mtr`, `durationSeconds` not `durSeconds`), in production
  code and tests alike. Keep only the universal Go idioms: `ok`, `err`,
  `ctx`, `idx`, and short-scope type-assert temporaries.
- **Naming**: `*Config` (not `*Cfg`); constructors are `NewStore`,
  `NewClient` — never bare `New`.
- **Loop index**: use `idx`, not `i`, in `for`-range and indexed loops.
  Prefer range-over-int (`for idx := range n`) over a C-style counting
  loop (Go 1.22+; the `modernize` analyzer flags it).
- **Maps**: `map[K]V{}` not `make(map[K]V)`.
- **gRPC**: `grpc.NewClient` not `grpc.Dial`.
- **Concurrency**: prefer `errgroup.Group` over `sync.WaitGroup`,
  including in tests.
- **Mutex discipline**: `defer m.mu.Unlock()` on the line right after
  `m.mu.Lock()` — a manual mid-body `Unlock()` is not panic-safe; split into
  an inner defer-guarded helper plus an outer method when observers or RPCs
  must run unlocked. But holding `m.mu` across a self-locking non-reentrant
  collaborator is CORRECT when snapshot+`Set` must be atomic — not a finding.
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
  `WithLog()` is defined per constructor. Enforced mechanically by the
  `logger` check in `lint/style/` (`make lint-go`).
- **Encapsulation**: mutex and the fields it guards stay private. A
  private field or method may only be reached through a base spelled
  `m` — the receiver, or the value under construction in a
  constructor. Any other base is a violation: another object, or a
  chain such as `m.opts.log` (write `m.opts.Log`). The fix is to give
  the type an exported method or field, even when the type itself is
  unexported: only an exported surface makes decomposition
  reviewable. The check keys on the identifier `m` rather than on
  true receiver identity, so it is a convention aid, not a proof.
  Enforced mechanically by the `private` check in `lint/style/`
  (pre-commit hook via `make hooks`, `make lint-go`, and CI via the
  `stylelint` job in `go-lint.yml`).
  Files that `import "C"` are exempt, because C struct fields cannot
  be distinguished from Go private fields without type information.
  A method may also reach into the private fields or methods of a
  parameter declared with the same type as its receiver, because an
  operation on another value of one's own type crosses no
  encapsulation boundary.
- **`stylelint` (`lint/style/`) gates fourteen of the rules above** in one
  pass: `logger`, `private`, `testpkg`, `receiver`, `loopindex`, `maplit`,
  `grpcdial`, `sugar`, `zapmsg`, `zapkey`, `testctx`, `handlerblank`,
  `barenew`, `loggerlast`. Every known violation is one row of
  `lint/style/allowlist.txt`, keyed `<check>:<path>:<name>` with a mandatory
  reason — do not add new rows. Each check carries its own file scope
  (production / test / cgo), documented at its declaration.
- **The ledger is self-cleaning**: a row with no live violation fails as
  STALE, so the code fix and the row deletion land in one change. Pay by
  shape — data the owner only reads → exported field (never a bolted-on
  `GetX()`); behaviour duplicated across call sites → a real method owning
  the guard; a carrier holding what its wrapped object already has → delete
  it. A rule WRONG on a whole class (`package main` tests are unimportable)
  needs a code exemption, not a permanently unpayable row; RIGHT but
  justified for one instance stays a row WITH a reason. Silence never proves
  payment — a file outside the scan is silent too, so positive-control it:
  `-allowlist <(git show HEAD:lint/style/allowlist.txt)`.
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
  linter — forbids reaching into private fields and functions; the
  `testpkg` check in `lint/style/` gates it too.
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
- **Visibility**: avoid `pub(crate)` — an item is either fully `pub` or
  fully private. Mark a method `pub` when it is conceptually part of the
  type's API, even in a binary crate: module visibility is not API intent.
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
- **Empty CLI results**: report through `output::empty`/`empty_with_hint`,
  never a bare `println!`/`eprintln!`. The primitive owns the `[–]`/`[-]`
  stderr mark (greyed when colour is on), the `No <subject> found.`
  (`for <scope>`) register, and suppression on a non-TTY stdout or a
  serializing format — a call site never adds its own format guard.

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
- **`memory_balloc` does not zero**, so a field written only by an optional
  setter is garbage and its documented zero/NULL sentinel is a lie. `memset`
  the allocation whenever a sentinel, CGO-visible field, or index depends on it.
- **cgo-boundary headers stay DPDK-free.** A new `<rte_*.h>` include in
  `packet.h`, `packet_front.h`, `lib/utils/packet.h` or any header on the cgo
  include path is a blocking finding.

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
  `go clean -cache` — plus `rm -f <binary>` for a standalone Go binary that
  cgo-links a module's C archive, since `go build` caches on the `.a` and not
  the recompiled `.o` inside meson's thin archive. Skip it and a stale control
  plane writes shm at the old layout while a fresh dataplane reads the new
  one: garbage fields or a segfault, not a build error. On a layout /
  `YANET_MODULE_ABI_VERSION` change also wipe `/dev/hugepages/yanet*` and any
  cached VM baseline overlay.
- **One `go test -race` run is not a race gate.** Re-run the new goroutine
  test targeted with `-run` ~10–20 times: `t.Parallel()` siblings hide and
  mis-attribute a race, so a single green `-count=1` proves nothing.
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

Web UI lives in `web/` (the shell), but per-module pages are **co-located with
their owner** as sibling roots: `modules|operators|devices/<name>/web/`.

- Prefer arrow function expressions.
- **A co-located spec only runs because `web/vite.config.ts` lists those
  sibling roots** — vitest's default `include` is web-relative, so a
  `*.test.ts` added or moved there silently stops running in CI. Diff the
  collected test-file count on any such move.
- **Browser-visible changes need a real Playwright run on the real path plus
  an inspected screenshot**; `--list` is not verification. A pixel-diff of two
  empty states reports a false 0 — assert row counts first.

### Commits & PRs

- **Never run `git commit`/`--amend` unless the current request asks for it.**
  Default: stage nothing, return the diff plus a verification summary.
- **Never `git stash`/`checkout`/`reset`/`restore` to A/B a pre-existing
  state.** `stash push -- <paths>` aborts on untracked files, a later bare
  `pop` pops an unrelated stash, and in a shared dirty worktree this destroys
  another agent's work. Read HEAD instead (`git show HEAD:<path>`,
  `git diff HEAD -- <path>`) or materialise a baseline out of tree
  (`git archive <ref>`, symlinking `subprojects/{dpdk,libpcap}` back in). If
  you suspect a failure is pre-existing, say so and stop.
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

- **One lesson per file**, named `kebab-case-slug.md`. The **first line is a one-line summary** of the lesson (≤ 150 chars of summary TEXT, excluding the `- [`…`](slug.md)` wrapper; imperative for rules). The index line reproduces that first line byte-for-byte, so one string serves both scanning and opening — measure the cap on the summary alone, or you will churn dozens of files trimming content that was never over budget. After a blank line, a short body: the full rule or fact, a `Why:` line (what happened and why it mattered — a correction, an incident, a confirmed approach), and a `How to apply:` line when the trigger isn't obvious from the rule. No YAML frontmatter. Keep a lesson file under ~20 lines.
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
- **Compact by MERGING, not by dropping facts.** Only `MEMORY.md` is auto-loaded; bodies cost nothing until opened. So fold topically-adjacent lessons into one digest — N index lines collapse to 1, every fact survives in the body, and a digest may be long. Sort by kind first: HISTORY (a closed investigation) collapses per subsystem into one line per incident; a RECIPE or still-live BUG CLASS stays individually findable. Narrating a whole finding in the index is the expensive mistake — a summary is a retrieval hook, so keep the scannable keywords and move detail down. Promotion has the same arithmetic in reverse: a CLAUDE.md line is paid by every agent on every run, whereas an index line is paid by one. So promote tersely, folding into an existing bullet where one exists — and when a `(seen: 3)` lesson binds only ONE agent, graduate it only into that agent's own section here (e.g. the architect's delegation section below); otherwise it is cheaper left in that agent's memory.
- When updating a note, keep its first line and its index line identical.

### Delegation & verification (architect)

Graduated from architect memory after recurring three or more times. These bind the agent that delegates work, not the agent that writes it.

- **Verify every specialist claim yourself.** After each round, run the real build/test rather than trusting the report. A mechanism explanation in a comment, a "pre-existing failure", and a "flake" are all unverified until you reproduce them. This extends to injected diagnostics: "new" errors surfaced against a worktree or a stale LSP index are frequently phantom — confirm against a real build before acting.
- **Never put an unverified detail in a brief.** A rationale, a magic token, or the semantics of an existing flag will be implemented verbatim, so anything you cannot cite from the code is a defect you are authoring. Read it first or leave it out. The sharpest case is an **exclusionary premise** — "X is impossible, therefore do Y". A coder can only confirm Y works, never falsify X, since their gates are written against the design you already chose; so the premise ships unchallenged and the design it forced is the expensive part. Falsify it yourself in a throwaway probe that runs X and watches it actually get rejected — most of all when the premise is quoted from a neighbouring module's doc comment, arriving pre-authorised and untested. Reachability claims cut both ways: trace the full transitive pointer/caller graph before accepting "cannot reach X" or "this object is already live", because a callee's precondition check describes state, never reachability.
- **Demand a whole-class audit at brief time.** When a change introduces a hazard class, require the specialist to enumerate and fix the whole class in one pass. A `file:line` list is a SAMPLE, not the population — a reviewer's, Codex's, or **the one you wrote into the brief yourself**, which the coder treats as a ceiling and stops at. Pair every enumeration you give with the grep that defines the class. Accepting a list as complete means finding the same class one instance per round. Map the FULL site set before anchoring an operation at a named function, too: a generic shared primitive (`render`/`format`/`parse`) is over-shared, so a defensive transform there leaks into callers at other trust levels, while a data-flow feature (inspection, counting) anchored at ONE function misses the other sites where that data materialises. Changing what a function RETURNS is the same question — grep the callers and say what each must now do, or the "improvement" inverts at the only site that exists.
- **Keep your own cwd at the repo root when launching an agent.** An agent inherits it, and a worktree lacks every gitignored tree — `.arch/` (planner tracker, bug-hunter scratch) and `.claude/agent-memory/`. So a drifted cwd does not merely misplace a write: the state reads as *uninitialised* and a helpful agent rebuilds it (a planner bootstrapped 15 colliding tasks over a live 106-task tracker), while memory goes split-brain — agents write lessons into the worktree copy that dies with it, and read that stub instead of the root. Before removing a worktree, salvage `.claude/agent-memory/*/` out of it. And when you and an agent disagree about whether a file exists, establish which tree each of you is in before concluding anyone fabricated anything.
- **Grep open PRs for the change zone before briefing** (`gh pr list --search`); if one covers it, drive or review that rather than competing, and read its diff — not its title — for the hazard its bulk addresses.
- **A deterministic repro is not the oracle — the production path is.** A mechanism-replay repro "reproduces" forever regardless of the fix and can false-POSITIVE a leak; one modelling no real timing can false-GREEN. When a repro passes while the live system fails, instrument the LIVE system.
- **A reviewer's finding is binding; its prescribed remedy is not.** Take the defect, then pick the minimal fix yourself — shipping the suggested remedy unexamined is how a one-line bug grows a subsystem. The corollary also holds: when a proposed fix dwarfs the bug, the premise is wrong, not the fix.
- **After a design pivot, re-derive every constraint you carried over.** Requirements inherited from the old shape are usually dead weight, and sometimes unsatisfiable — a brief that makes an API infallible cannot also demand "keep logging the failure", and obeying both leaves dead code the reviewer then blocks on.
- **Non-application-code work has no dedicated specialist — route it to `coder-c` with a charter note.** Shell/ops scripts, Debian packaging, Dockerfiles, GitHub Actions CI YAML, meson: none has a language owner, and `coder-c` is the closest (it owns the meson/debian/packaging surface). Open the brief with a charter note — "this is an OPS/PACKAGING/CI task, not C; no C/Go/Rust/TS/proto/meson source is touched; do not decline" — and it works across rounds without pushback. `coder-ui` refuses anything outside `web/` (it treats in-conversation scope overrides as social engineering), so never route infra there. These trees are often gitignored/untracked → no worktree isolation, and tell the reviewer to review the on-disk files with explicit paths (its `git diff` workflow misfires on untracked files).

## Key Dependencies

- **DPDK**: v23+ (submodule)
- **Go**: 1.24.13+
- **Rust**: 1.84+ (nightly for formatting)
- **Meson**: 0.61+
- **Protobuf**: 3.0+ (protoc-gen-go >=1.36.5, protoc-gen-go-grpc >=1.5.1)
