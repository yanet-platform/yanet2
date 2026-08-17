# AGENTS.md

Guidance for AI coding agents. Keep it under 8 KB: facts about the code, build and environment that every agent needs — process rules belong in a charter or a lint.

## Project

YANET is a high-performance software router on DPDK: C + DPDK dataplane (`dataplane/`, `modules/*/dataplane/`), Go control plane (`controlplane/`, `modules/*/controlplane/`, `operators/`), Rust CLIs (`cli/`, `modules/*/cli/`), TypeScript/React web UI (`web/` shell + co-located `modules|operators|devices/<name>/web/`).

Data flow: CLI (Rust) → gRPC → gateway (Go) → gRPC → module control plane (Go) → shared memory → dataplane (C). The dataplane keeps the last valid config if upper layers fail; updates apply atomically.

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
gofmt -w . ; clang-format -i <file> ; cargo +nightly fmt ; cargo clippy
make proto-lint | make lint-go | make lint/clang-syntax | make lint-commit
make proto-go                                      # *.pb.go, needed before go lint locally
make hooks                                         # install git hooks (once per clone)
make fuzz [MODULE=<name>]
make ai/agents                                     # regenerate agent charters from .rulesync/subagents/
```

## Layout

- `dataplane/` — DPDK binary (`main.c`, `config.c`, `dpdk.c`, `worker.c`, `drivers/`).
- `controlplane/` — gateway (`gateway/`), built-in services (`builtin/`), CGO shm bindings (`ffi/`), root protos (`ynpb/`), control-plane package (`yncp/`), entrypoint (`cmd/yncp-director`).
- `modules/` — packet-processing modules; `devices/` — device adapters (`plain`, `vlan`, `trafgen`), same layout.
- `operators/` — long-running Go orchestration daemons above the gateway: `pipeline`, `decap`, `forward`, `route` (each `cmd/` + `internal/` + `operatorpb/` + Rust `cli/`), `bird-adapter`, `neighbours` (web only here; process in the private repo).
- `lib/` — C support libs (`controlplane`, `counters`, `dataplane`, `dataplane_ut`, `errors`, `filter`, `fwstate`, `logging`, `utils`); `api/` — public C API headers; `bindings/go/` — root CGO agent bindings.
- `common/` — shared code: `go/` (`xcfg`, `xcmd`, `xerror`, `xnetip`, `xpacket`, `xgrpc`, `logging`, `metrics`, `readiness`, `testutils`, …), `rust/` (`commonpb`, `filterpb`, `ynpb`, `bitmap`), `commonpb/`, `filterpb/`, `ttlmap/`, C headers (`lpm.h`, `radix.h`, `hash.h`, `rcu.h`, `memory*.h`).
- `cli/` — Rust CLI workspace: `core/` (crate `yanet-cli`, aliased `ync`), `modules/{inspect,pipeline,function,counters,common}`.
- `lint/` — repo linters (`style`, `commit`, `protobuf`); `docs/`, `deploy/`, `debian/`, `etc/`, `subprojects/dpdk/`.

### Module layout (canonical — decap, dscp, forward, route as reference)

```
modules/<name>/
  api/           C library for control-plane FFI (controlplane.c/h)
  bindings/go/   CGO wrapper consumed by controlplane
  controlplane/  <name>pb/ protos, mod.go (BuiltInModule, New(opts)), backend.go (shm write path), service.go (+_test), cfg.go
  dataplane/     config.h (shm config struct), dataplane.c/h (entry; hot paths are static inline in headers)
  cli/           Rust crate, build.rs runs tonic-build (client only)
  tests/  fuzzing/  [internal/]
```

Active modules: `route, acl, balancer2, blackhole, forward, decap, nat64, fwstate, dscp, pdump, route-mpls, mirror`. Legacy shape: `pdump` (CGO in `controlplane/ffi.go`, no `bindings/`); `fwstate` partially migrated; `balancer2` early-stage (`controlplane/` = protos only). Dataplane symbols are exported via meson `--defsym new_module_<name>`.

Shared-memory pattern: `ffi.SharedMemory` → `shm.AgentAttach(name, instanceIdx, size)` → write the C config through FFI (`<name>_module_config_update()`) with Go memory pinned by `runtime.Pinner` → the dataplane reads it atomically. Exported Go APIs whose arguments index C arrays (device IDs, queue/worker indices) validate the range on the Go side.

Rust CLI: binaries `yanet-cli`, `yanet-cli-<module>`; dependency `ync = { path = "../../../cli/core", version = "0.1", package = "yanet-cli" }`; shared protos via `common/rust` `extern_path`. A CLI is registered in THREE places that move together: root `Cargo.toml` members, root `Makefile` (`CLI_CORE_MODULES` / `CLI_MODULES`), `debian/yanet2-cli.install` — a miss in the last two builds green and is never installed. Private (gitignored) CLIs are standalone workspaces, not root members.

Agents: charters are authored in `.rulesync/subagents/*.md` (`make ai/agents` generates the gitignored client trees); public skills live in `.agents/skills/<name>/`, symlinked from `.claude/skills/`.

## Conventions

Language rules: `.claude/conventions/{go,rust,c,ts}.md` — read the one for the language you touch.

- C-only changes still need `go test -count=1` and a clean `meson compile`: Go can link a stale C archive. A shared-memory layout or `YANET_MODULE_ABI_VERSION` change needs `go clean -cache`, removal of standalone CGO binaries and of `/dev/hugepages/yanet*`.
- Test logic and invariants, not human-readable CLI output (ASCII-only paths and canonical MAC/hex rendering are contracts and may be tested).
- Race tests need targeted `-run` repetition (~10–20 runs); one `-race` pass can miss it.
- `modules/*/tests/vm` exit code 127/3 means binaries are not staged, not a product defect.
- Benchmarks must exercise the claimed path; `for b.Loop()` suppresses inlining — cross-check allocations against a real caller with `-gcflags=-m`.

## Worktrees

Every writing task runs in a linked worktree on a task branch from confirmed `origin/main`: `.claude/worktrees/<name>` (Claude Code) or `.agent-state/worktrees/<name>` (Codex). The primary checkout stays on `main` and takes no tracked-file writes; gitignored trees (`.arch/`, agent memory, private modules) are worked at the primary with absolute paths. Confirm `git rev-parse --show-toplevel` and the branch before the first write. A gate that produces or consumes `build/` (`make test|dataplane|fuzz|test-asan|test-functional`) needs the worktree's own real `build/`; go/cargo/npm/lint-only gates may symlink the primary `build/` and its `*.pb.go`. Never symlink `.claude/agents/`, `.codex/agents/` or `.opencode/agents/` into a worktree.

## Commits & PRs

- Never `git commit`/`--amend` unless the request asks for it; never `stash`/`checkout`/`reset`/`restore` to A/B a pre-existing state — read `git show HEAD:<path>` instead.
- Subject: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>)[!]: brief` — scope mandatory (lowercase, comma-separated), brief lowercase, no trailing period, no code-level detail. Enforced by the `commit-msg` hook and the Commit Lint CI job.
- No `Co-Authored-By` / `Generated with` footers.
- PR title = commit subject format. Body: bullets starting with a capital and ending with a period, `Closes #<n>.` when applicable, no `## Summary` header, no test-plan section.
- GitHub is driven with `gh`: reads freely, writes (PR create/merge, comments, issues) only when the task asks for them.

## Agent memory

`.claude/agent-memory/<agent>/` (Codex: `.agent-state/agent-memory/<agent>/`): `MEMORY.md` is an index of at most 20 `- [summary](file.md)` rows; a lesson is at most 5 lines / 600 bytes; only facts about the code, build or environment that this file does not already state — never process rules, counters or retrospectives.

## Dependencies

DPDK v23+ (submodule) · Go 1.24.13+ · Rust 1.88+ (nightly fmt) · Meson 0.61+ · Protobuf 3.0+ (protoc-gen-go ≥ 1.36.5, protoc-gen-go-grpc ≥ 1.5.1).
