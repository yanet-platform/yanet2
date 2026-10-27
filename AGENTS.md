# AGENTS.md

Keep under 8 KB: code, build/environment facts and repository review rules. Process rules belong in skills, hooks or linters.

## Project

YANET is a high-performance software router on DPDK: C + DPDK dataplane (`dataplane/`, `modules/*/dataplane/`), Go control plane (`controlplane/`, `modules/*/controlplane/`, `operators/`), Rust CLIs (`cli/`, `modules/*/cli/`), TypeScript/React web UI (`web/` shell + co-located `modules|operators|devices/<name>/web/`).

Data flow: CLI (Rust) → gRPC → gateway (Go) → gRPC → module control plane (Go) → shared memory → dataplane (C). The dataplane keeps the last valid config if upper layers fail; updates apply atomically.

## Build & test

```bash
git submodule update --init && meson setup build   # once; DPDK is a meson subproject
make all | make build | make cli                   # everything | meson compile -C build | cargo build --release --workspace --bins
npm ci && npm run build -w web                     # web is an npm workspace: install at repo root
make setup-debug | make setup-asan                 # debug / ASan+UBSan builds
make test | make test-asan | make test-tsan        # Go + meson tests
make test-functional                               # QEMU VM suite
meson test -C build <name>; make build && go test -count=1 ./modules/route/...
make proto-lint | make lint-go | make lint/clang-syntax | make lint-commit
make proto-go                                      # *.pb.go, needed before go lint locally
make gate                                          # CI checks for the layers changed since origin/main; prints failures only
make hooks                                         # install git hooks (once per clone)
make fuzz [MODULE=<name>]
```

[Shared Docker caches](docs/docker-cache.md).

## Layout

- `dataplane/` — DPDK binary (`main.c`, `config.c`, `dpdk.c`, `worker.c`, `drivers/`).
- `controlplane/` — `gateway/`, built-in services (`builtin/`), CGO shm bindings (`ffi/`), published-config store (`configstore/`), root protos (`ynpb/`), `yncp/`, entrypoint `cmd/yncp-director`.
- `modules/` — packet-processing modules; `devices/` — device adapters (`plain`, `vlan`, `trafgen`, `vxlan`), same layout.
- `operators/` — Go daemons above the gateway: `pipeline`, `decap`, `forward`, `route` (`cmd/` + `internal/` + `operatorpb/` + `cli/`), `bird-adapter`, `neighbours` (web only here).
- `lib/` — C support libs (`controlplane`, `dataplane`, `dataplane_ut`, `filter`, `fwstate`, …), `rust/` (Rust module SDK, own cargo workspace); `api/` — public C API headers; `bindings/go/` — root CGO agent bindings.
- `common/` — shared code: `go/` (`xcfg`, `xcmd`, `xerror`, `xgrpc`, `logging`, …), `rust/`, `commonpb/`, `filterpb/`, `ttlmap/`, C headers (`lpm.h`, `hash.h`, `memory*.h`).
- `cli/` — Rust CLI workspace: `core/` (crate `yanet-cli`, aliased `ync`), `modules/{inspect,pipeline,function,counters,common}`.
- `lint/` — repo linters (`style`, `commit`, `protobuf`); `docs/`, `deploy/`, `debian/`, `etc/`, `subprojects/dpdk/`.

- `lab/` — local QEMU lab library; `cmd/yanet-lab/` — developer-only lab CLI (not packaged); `tests/functional/framework/` — QEMU harness shared by functional tests and the lab (imports `testing`).

### Module layout (canonical — decap, dscp, forward, route as reference)

```
modules/<name>/
  api/           C library for control-plane FFI (controlplane.c/h)
  bindings/go/   CGO wrapper consumed by controlplane
  controlplane/  <name>pb/ protos, mod.go (BuiltInModule, New(opts)), backend.go (shm write path), service.go (+_test), cfg.go
  dataplane/     config.h (shm config struct), dataplane.c/h (entry; hot paths are static inline in headers)
  cli/           Rust crate (build.rs runs ync-build)
  tests/  fuzzing/  [internal/]
```

Active modules: `route, acl, l3b, blackhole, forward, decap, nat64, fwstate, dscp, pdump, route-mpls, mirror`. Legacy shape: `pdump` (CGO in `controlplane/ffi.go`, no `bindings/`); `fwstate` partially migrated. The `l3b` module links virtual-service and session-table objects under `objects/l3b/`. Dataplane symbols are exported via meson `--require-defined=new_module_<name>`.

Shared-memory pattern: `ffi.SharedMemory` → `shm.AgentAttach(name, instanceIdx, size)` → write the C config through FFI (`<name>_module_config_update()`) with Go memory pinned by `runtime.Pinner` → the dataplane reads it atomically. Exported Go APIs whose arguments index C arrays (device IDs, queue/worker indices) validate the range on the Go side.

Rust CLI (`code-cli` skill has the rest): a CLI is registered in THREE places that move together — root `Cargo.toml` members, root `Makefile` (`CLI_CORE_MODULES` / `CLI_MODULES`), `debian/yanet2-cli.install`; a miss in the last two builds green and is never installed. Private CLIs are standalone workspaces.

## Conventions

Language rules: `.agents/conventions/{go,rust,c,ts}.md` — read the one for the language you touch (Claude Code loads them by path via `.claude/rules/`). Comments (all languages): `.agents/conventions/comments.md`. Tests (all languages): `.agents/conventions/tests.md`.

- After native changes run `meson compile -C build` before direct Go commands; `go test` needs the cache-line and sanitizer CGO flags that `make test`/`make test-asan` supply. Shm layout or `YANET_MODULE_ABI_VERSION` changes need rebuilt standalone CGO binaries and coordinated restarts; stop attached processes before removing stale `/dev/hugepages/yanet*`.
- Skills live in `.agents/skills/<name>/` (symlinked from `.claude/skills/`). Hooks in `.agents/hooks/` (`.claude/settings.json`, `.codex/hooks.json`; Codex needs `/hooks` trust) format edited files and refuse writes on `main`, whole-tree `git add`, `--no-verify`, `git stash` and git commands that discard changes.
- Test invariants, not CLI prose (ASCII-only paths and canonical MAC/hex are contracts). Dataplane-module behavior must use and extend `dataplane_ut`; see `.agents/conventions/tests.md`.
- Race tests need targeted `-run` repetition (~10–20 runs).
- `modules/*/tests/vm` exit code 127/3 means binaries are not staged, not a product defect.
- Benchmarks exercise the claimed path; `for b.Loop()` suppresses inlining — check allocations with `-gcflags=-m`.

## Worktrees

Use linked worktrees at `.claude/worktrees/` (Claude Code) or `.agent-state/worktrees/` (Codex), on task branches from confirmed `origin/main`. Confirm root and branch first. Keep primary on `main`; write only ignored trees there, by absolute path. Gates using `build/` need their own build and initialized submodules; cargo/npm/lint-only gates may link primary `build/` and `*.pb.go`.

## Commits & PRs

- Write every PR review comment in English only, including inline comments, summaries, and replies. Do not mix languages.
- Never `git commit`/`--amend` unless the request asks for it; never `stash`/`checkout`/`reset`/`restore` to A/B a pre-existing state — read `git show HEAD:<path>` instead.
- Subject: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>)[!]: brief` — scope mandatory (lowercase, comma-separated), brief lowercase, no trailing period, no code-level detail. Enforced by the `commit-msg` hook and the Commit Lint CI job.
- No `Co-Authored-By` / `Generated with` footers.
- PR title = commit subject format. Body: bullets starting with a capital and ending with a period, `Closes #<n>.` when applicable, no `## Summary` header, no test-plan section.
- GitHub via `gh`: reads freely, writes (PRs, comments, issues) only when the task asks.

## Working with agents

- One agent writes a change; subagents explore, reproduce, measure or review (`review-change`) in fresh context. Parallel writers only for independent PRs in own worktrees.
- A change to a shared primitive, return value or data-flow site starts with a grep of every consumer, generated and gitignored trees included.
- Verify a relayed claim against the code before repeating it; when readings diverge materially, ask with concrete options.

## Dependencies

DPDK v23+ (submodule) · Go 1.27.1+ · Rust 1.99+ (rustfmt nightly-2026-10-22) · Meson 1.3+ · CMake 3.5+ · Protobuf 3.0+ (protoc-gen-go ≥ 1.36.5, protoc-gen-go-grpc ≥ 1.5.1).
