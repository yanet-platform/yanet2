# AGENTS.md

Code/build/environment facts, under 8 KB. Process rules go in charters/linters.

## Project

YANET is a DPDK software router: C dataplane (`dataplane/`, `modules/*/dataplane/`), Go CP (`controlplane/`, `modules/*/controlplane/`, `operators/`), Rust CLI (`cli/`, `modules/*/cli/`), TypeScript/React UI (`web/` shell + `modules|operators|devices/<name>/web/`).

Data flow: Rust CLI → gRPC → Go gateway → gRPC → Go module control plane → shared memory → C dataplane. Updates are atomic; upper-layer failures preserve the last valid dataplane config.

## Build & test

```bash
git submodule update --init && meson setup build   # once; DPDK is a meson subproject
make all | make dataplane | make cli               # everything | meson compile -C build | cargo build --release --workspace
cd controlplane && go build ./...                  # Go
npm ci && npm run build -w web                     # web is an npm workspace: install at repo root
make setup-debug | make setup-asan                 # debug / ASan+UBSan builds
make test | make test-asan | make test-tsan        # Go + meson tests (cleans go cache first)
make test-functional                               # QEMU VM suite
meson test -C build <name>; go test ./modules/route/...
gofmt -w . ; clang-format -i <file> ; cargo +nightly-2026-08-28 fmt ; cargo clippy
make proto-lint | make lint-go | make lint/clang-syntax | make lint-commit
make proto-go                                      # *.pb.go, needed before go lint locally
make hooks                                         # install git hooks (once per clone)
make fuzz [MODULE=<name>]
make ai/agents                                     # regenerate agent charters from .rulesync/subagents/
```

## Layout

- `dataplane/` — DPDK binary (`main.c`, `config.c`, `dpdk.c`, `worker.c`, `drivers/`).
- `controlplane/` — `gateway/`, built-in services (`builtin/`), CGO shm bindings (`ffi/`), shared published-config store (`configstore/`), root protos (`ynpb/`), CP package (`yncp/`), entrypoint (`cmd/yncp-director`).
- `modules/` — packet-processing modules; `devices/` — device adapters (`plain`, `vlan`, `trafgen`), same layout.
- `operators/` — Go daemons above gateway: `pipeline`, `decap`, `forward`, `route` (`cmd/`, `internal/`, `operatorpb/`, Rust `cli/`), `bird-adapter`, `neighbours` (web here, process in private repo).
- `lib/` — C support libs (`controlplane`, `counters`, `dataplane`, `dataplane_ut`, `errors`, `filter`, `fwstate`, `logging`, `utils`); `api/` — public C API headers; `bindings/go/` — root CGO agent bindings.
- `common/` — `go/{xcfg,xcmd,xerror,xpacket,xgrpc,logging,metrics,readiness,testutils,…}`, `rust/{commonpb,filterpb,ynpb}`, `commonpb/`, `filterpb/`, `ttlmap/`, C headers `lpm.h`, `radix.h`, `hash.h`, `rcu.h`, `memory*.h`.
- `cli/` — Rust CLI workspace: `core/` (crate `yanet-cli`, aliased `ync`), `modules/{inspect,pipeline,function,counters,common}`.
- `lint/` — repo linters (`style`, `commit`, `protobuf`); `docs/`, `deploy/`, `debian/`, `etc/`, `subprojects/dpdk/`.

- `lab/` — reusable local QEMU lab library (manifests, scenarios, operator baseline); `cmd/yanet-lab/` — developer-only lab supervisor CLI (not packaged).
- `tests/functional/framework/` — QEMU/harness shared by functional tests and lab supervisor; imports `testing` but is not test-only.

### Module layout (canonical — decap, dscp, forward, route as reference)

```
modules/<name>/
  api/           C library for control-plane FFI (controlplane.c/h)
  bindings/go/   CGO wrapper consumed by controlplane
  controlplane/  <name>pb/ protos, mod.go (BuiltInModule, New(opts)), backend.go (shm write path), service.go (+_test), cfg.go
  dataplane/     config.h (shm config struct), dataplane.c/h (entry; hot paths are static inline in headers)
  cli/           Rust crate, build.rs runs tonic-prost-build (client and server modules)
  tests/  fuzzing/  [internal/]
```

Active modules: `route, acl, l3b, blackhole, forward, decap, nat64, fwstate, dscp, pdump, route-mpls, mirror`. Legacy: `pdump` (CGO in `controlplane/ffi.go`, no `bindings/`); `fwstate` partly migrated. `l3b` links virtual-service/session-table objects in `objects/l3b/`. Meson exports dataplane symbols via `--defsym new_module_<name>`.

Shared memory: `ffi.SharedMemory` → `shm.AgentAttach(name, instanceIdx, size)` → FFI `<name>_module_config_update()` with Go memory pinned by `runtime.Pinner` → atomic dataplane read. Exported Go APIs validate C-array indices (device IDs, queues, workers) on the Go side.

Rust CLI: `yanet-cli`, `yanet-cli-<module>`; dependency `ync = { path = "../../../cli/core", version = "0.1", package = "yanet-cli" }`; shared protos via `common/rust` `extern_path`. Register CLIs in root `Cargo.toml`, `Makefile` (`CLI_CORE_MODULES` / `CLI_MODULES`) and `debian/yanet2-cli.install`; missing either latter entry builds green but omits installation. Private gitignored CLIs use standalone workspaces.

Agent charters: `.rulesync/subagents/*.md`; `make ai/agents` generates gitignored client trees. Public skills: `.agents/skills/<name>/`, symlinked from `.claude/skills/`.

## Conventions

Read `.agents/conventions/{go,rust,c,ts}.md` for the language changed; `comments.md` and `tests.md` in that directory apply to all languages.

- New deployable components must support env overrides of file config. In Go, reuse `common/go/xcfg` with `xcfg.WithEnv()` (`YANET_` prefix): env > file > defaults, with the same decoding/validation. Document names; test precedence, absent overrides and invalid values.
- C-only edits need `go test -count=1` and clean `meson compile` (stale C archive risk). Shm-layout/`YANET_MODULE_ABI_VERSION` changes need `go clean -cache` and removal of standalone CGO binaries and `/dev/hugepages/yanet*`.
- Test logic and invariants, not human-readable CLI output (ASCII-only paths and canonical MAC/hex rendering are contracts and may be tested).
- Race tests need targeted `-run` repetition (~10–20 runs); one `-race` pass can miss it.
- `modules/*/tests/vm` exit code 127/3 means binaries are not staged, not a product defect.
- Benchmark the claimed path; `for b.Loop()` suppresses inlining. Check allocations against a real caller with `-gcflags=-m`.

## Worktrees

Write on task branches in linked worktrees from confirmed `origin/main`: `.claude/worktrees/<name>` (Claude Code), `.agent-state/worktrees/<name>` (Codex). Primary stays on `main`, no tracked edits; its gitignored `.arch/`, memory and private modules use absolute paths. First verify `git rev-parse --show-toplevel` and branch. Build gates (`make test|dataplane|fuzz|test-asan|test-functional`) require real local `build/`; go/cargo/npm/lint-only gates may symlink primary `build/` and `*.pb.go`. Never symlink `.claude/agents/`, `.codex/agents/`, `.opencode/agents/` into worktrees.

## Commits & PRs

- Commit/amend only when requested. Never `stash`/`checkout`/`reset`/`restore` to A/B pre-existing state; use `git show HEAD:<path>`.
- Subject: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>)[!]: brief`. Mandatory lowercase scope (comma-separated), lowercase brief, no final period/code detail. `commit-msg` and Commit Lint CI enforce it.
- No `Co-Authored-By` / `Generated with` footers.
- PR title follows subject format. Body: capitalized bullets ending in periods; `Closes #<n>.` when applicable; no `## Summary` or test-plan section.
- Use `gh`: reads freely; PR creation/merge, comments and issues only when requested.

## Agent memory

`.claude/agent-memory/<agent>/` (Codex: `.agent-state/agent-memory/<agent>/`): `MEMORY.md` index ≤20 `- [summary](file.md)` rows; lessons ≤5 lines / 600 bytes. Only code/build/environment facts absent here; no process rules, counters or retrospectives.

## Dependencies

DPDK v23+ (submodule) · Go 1.24.13+ · Rust 1.88+ (rustfmt nightly-2026-08-28) · Meson 0.61+ · CMake 3.5+ · Protobuf 3.0+ (protoc-gen-go ≥ 1.36.5, protoc-gen-go-grpc ≥ 1.5.1).
