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
cd web && npm install && npm run build

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
- `agents/`        — long-running orchestration daemons (see Agents).
- `filter/`        — filter compiler, classifiers, and query engine (C).
- `lib/`           — C support libraries: `controlplane`, `counters`, `dataplane`, `errors`, `fwstate`, `logging`, `utils`, plus `tests/` and `fuzzing/`.
- `api/`           — public C API headers exposed to control plane (`agent.h`, `config.h`, `counter.h`, `info.h`).
- `bindings/go/`   — root-level Go CGO bindings for the agent/shared-memory surface.
- `mock/`          — C dataplane test mocks (`mock.c/h`, `worker.c/h`, etc.) used by module unit tests.
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
- `internal/gateway/` — actual proxy implementation: `proxy`, `director`, `registry`.
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

**Legacy** (acl, fwstate, nat64, pdump, route-mpls): no `bindings/`,
CGO calls live directly in `controlplane/ffi.go`, no `backend.go`.

**Special**: `balancer` does not follow the canonical layout.
- `agent/` — CGO surface: `agent.c/h`, `manager.c/h`, `config.c`, `balancerpb/`, `go/`.
- `controlplane/` — `api/`, `balancerpb/`, `handler/`, `state/`.
- Top-level `bench/`, `tests/`, `cli/`, `dataplane/` complete the module.

`balancer2` is an early-stage rewrite — only `api/` and `dataplane/`
exist today.

Module dataplane symbols are exported via meson linker defsym: `new_module_<name>`.

Active modules: `route, acl, balancer, balancer2, forward, decap, nat64,
fwstate, dscp, pdump, route-mpls`.

### Devices

`devices/` mirrors `modules/` layout (`api/`, `controlplane/`, `dataplane/`,
`cli/`) but for device adapters rather than packet-processing modules.
Active devices: `plain`, `vlan`.

### Agents

`agents/` holds long-running Go control-plane processes that orchestrate
the dataplane through the gateway, distinct from per-module gRPC services.

- `agents/yanet-pipeline-operator` — declarative reconciliation operator
  (`cmd/`, `internal/`, `operatorpb/`). Structural template for future
  operators (route, acl, balancer).
- `agents/bird-adapter` — BIRD routing-daemon adapter (canonical agent
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
2. Creates agent via `shm.AgentReattach(name, instanceIdx, size)`
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

### Go

- **Receiver names**: always `m`. No type-letter mnemonics.
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
- **Constructors accepting `*zap.Logger` MUST use options pattern**:
  `NewFoo(cfg, WithLog(log))`. Inside the constructor:
  `opts := newOptions(); for _, o := range options { o(opts) }`.
  Parameter is `options ...Option`, never renamed to `opt`/`optsList`.
  `WithLog()` is defined per constructor.
- **Encapsulation**: mutex and the fields it guards stay private.
- **gRPC handlers**: never use `_` for `ctx` / `req` — name them.
- **No log-only RPC stubs**: when a brief names an RPC, actually invoke
  the client. `m.log.Debug("would call …")` is a bug, not a stub.
- **Comments**: English, end with period, fit within ~80 chars
  (reflow rather than preserving narrower fill). List only production
  callers, not "tests". No section-separator comments.
- **Tests**: table-driven, use `require.NoError(t, err)`. Do not
  reference tests inside production-code comments.

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
- **No infallible `TryFrom`**: replace with `From`, or remove the impl
  if the call site is trivially inlinable.
- **`assert_eq!` order**: expected first, actual second:
  `assert_eq!(expected, actual)`.
- **Style**: prefer shadowing over `_str`-suffixed intermediates.
  Use `match self { Self(v) => … }` or `let Self(v) = *self;` over
  direct `self.0`. Trait bounds in `where` clauses, not inline.
  Import type names directly (`use serde::Serialize;` then `T: Serialize`),
  not module-qualified (`serde::Serialize`).

### C

- Always use braces for `if`/`else`/`for`/`while`, even single-line bodies.
- Format with `clang-format`.

### TypeScript/React

Web UI lives in `web/` (`package.json`, `index.html`, `dist/`).

- Prefer arrow function expressions.
- No section separator comments.

### Commits & PRs

- Commit format: `feat|fix|perf|chore|refactor(<scope>): brief description`
  with high-level description (no code-level details, no
  backtick-quoted symbol names).
- **Do not** add `Co-Authored-By: Claude …` / `Generated with Claude Code`
  footers.
- PR title: `<feat|refactor|chore|perf|docs>: <short description>`.
- PR body: bullets start with capital, end with period. Add
  `Closes #<number>.` when applicable. **Do not** include a
  `## Summary` header — content goes directly. **Do not** include a
  `Test plan` section. PR descriptions have no 80-char line limit.

## Agent Memory & Feedback

**`.claude/agent-memory/<agent>/`** — per-agent persistent memory (corrections, confirmed approaches, project notes).
Each agent's directory already exists — agents write to it directly with the Write tool.
Anything codified in this `CLAUDE.md` should be removed from agent memory to avoid duplication; agent memory holds the "Why" and lessons that are still settling.

### Memory file format

Each memory is a standalone `.md` file with frontmatter:

```markdown
---
name: {{memory name}}
description: {{one-line description — used to decide relevance, so be specific}}
type: {{user | feedback | project | reference}}
---

{{memory content}}
```

### Memory index

Each agent maintains a `MEMORY.md` index in its memory directory. `MEMORY.md` is loaded into conversation context automatically — keep it under 200 lines. Each entry is one line under ~150 characters:

```
- [Title](file.md) — one-line hook
```

Never write memory content directly into `MEMORY.md` — it is only an index.

### Memory types

| Type | What to store | When to save |
|------|--------------|--------------|
| **user** | User's role, goals, preferences, knowledge level | When you learn details about the user that affect how you should respond |
| **feedback** | User corrections AND confirmed approaches | When user says "don't do X" or confirms a non-obvious approach worked |
| **project** | Ongoing work, goals, decisions, constraints not in code/git | When you learn who/what/why/when about project work. Convert relative dates to absolute |
| **reference** | Pointers to external systems (Linear, Grafana, Slack, etc.) | When you learn about external resources and their purpose |

### Feedback and project memory structure

Lead with the rule/fact, then:

- **Why:** the motivation or incident behind it.
- **How to apply:** when/where this kicks in.

### What NOT to save

- Code patterns, conventions, architecture, file paths — derivable from the codebase.
- Git history, recent changes — use `git log` / `git blame`.
- Debugging solutions — the fix is in the code, context is in the commit message.
- Anything already documented in this CLAUDE.md.
- Ephemeral task details or current conversation context.

### Memory hygiene

- Check for existing memories before creating new ones — update rather than duplicate.
- Verify memories before acting on them: file paths may have moved, functions may have been renamed.
- If a memory conflicts with current code state, trust the code and update or remove the stale memory.
- A memory that summarizes repo state is frozen in time — prefer `git log` or reading code for current state.

## Key Dependencies

- **DPDK**: v23+ (submodule)
- **Go**: 1.24.13+
- **Rust**: 1.84+ (nightly for formatting)
- **Meson**: 0.61+
- **Protobuf**: 3.0+ (protoc-gen-go >=1.36.5, protoc-gen-go-grpc >=1.5.1)
