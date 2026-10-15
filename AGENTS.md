# AGENTS.md

Agent guidance, under 8 KB: code/build/environment facts; process rules go in charters or lints.

## Project

YANET is a high-performance DPDK software router: C + DPDK dataplane (`dataplane/`, `modules/*/dataplane/`), Go control plane (`controlplane/`, `modules/*/controlplane/`, `operators/`), Rust CLIs (`cli/`, `modules/*/cli/`), TypeScript/React web UI (`web/` shell + co-located `modules|operators|devices/<name>/web/`).

Flow: Rust CLI -> gRPC -> Go gateway -> gRPC -> Go module control plane -> shared memory -> C dataplane. Updates are atomic; dataplane keeps its last valid config if upper layers fail.

## Build & test

```bash
git submodule update --init && meson setup build # once; DPDK is a meson subproject
make all | make dataplane | make cli # everything | meson compile -C build | cargo build --release --workspace
make dataplane && (cd controlplane && go build ./...) # prepare native inputs before direct Go
npm ci && npm run build -w web # npm workspace: install at repo root
make setup-debug | make setup-asan # debug / ASan+UBSan builds
make test | make test-asan | make test-tsan # Go+meson tests
make test-functional # QEMU VM suite
meson test -C build <name>; make dataplane && go test -count=1 ./modules/route/...
gofmt -w . ; clang-format -i <file> ; cargo +nightly-2026-10-22 fmt ; cargo clippy
make proto-lint | make lint-go | make lint/clang-syntax | make lint-commit
make proto-go # *.pb.go, needed before go lint locally
make hooks # install git hooks (once per clone)
make fuzz [MODULE=<name>]
make ai/agents # regenerate agent charters from .rulesync/subagents/
```

[Docker caches](docs/docker-cache.md): Go caches and ccache shared with containers.

## Layout

- `dataplane/`: DPDK binary (`main.c`, `config.c`, `dpdk.c`, `worker.c`, `drivers/`).
- `controlplane/`: gateway (`gateway/`), built-in services (`builtin/`), CGO shm bindings (`ffi/`), shared module-service published-config store (`configstore/`), root protos (`ynpb/`), control-plane pkg (`yncp/`), entrypoint (`cmd/yncp-director`).
- `modules/`: packet-processing modules; `devices/`: device adapters (`plain`, `vlan`, `trafgen`, `vxlan`), same layout.
- `operators/`: long-running Go orchestration daemons above the gateway: `pipeline`, `decap`, `forward`, `route` (each `cmd/`+`internal/`+`operatorpb/`+Rust `cli/`), `bird-adapter`, `neighbours` (web only here; process in the private repo).
- `lib/`: C support libs (`controlplane`, `counters`, `dataplane`, `dataplane_ut`, `errors`, `filter`, `fwstate`, `logging`, `utils`); `api/`: public C API headers; `bindings/go/`: root CGO agent bindings.
- `common/`: shared code: `go/` (`xcfg`, `xcmd`, `xerror`, `xpacket`, `xgrpc`, `logging`, `metrics`, `readiness`, `testutils`, …), `rust/` (`commonpb`, `filterpb`, `ynpb`), `commonpb/`, `filterpb/`, `ttlmap/`, C headers (`lpm.h`, `radix.h`, `hash.h`, `rcu.h`, `memory*.h`).
- `cli/`: Rust CLI workspace: `core/` (crate `yanet-cli`, aliased `ync`), `modules/{inspect,pipeline,function,counters,common}`.
- `lint/`: repo linters (`style`, `commit`, `protobuf`); `docs/`, `deploy/`, `debian/`, `etc/`, `subprojects/dpdk/`.
- `lab/`: reusable local QEMU lab library (manifests, scenarios, operator baseline); `cmd/yanet-lab/`: dev-only lab supervisor CLI (not packaged).
- `tests/functional/framework/`: shared QEMU/harness infrastructure for functional tests and lab supervisor (not purely test code; imports `testing`).

### Module layout (canonical: decap, dscp, forward, route)

```
modules/<name>/
api/ C library for control-plane FFI (controlplane.c/h)
bindings/go/ CGO wrapper consumed by controlplane
controlplane/ <name>pb/ protos, mod.go (BuiltInModule, New(opts)), backend.go (shm write path), service.go (+_test), cfg.go
dataplane/ config.h (shm config struct), dataplane.c/h (entry; hot paths static inline in headers)
cli/ Rust crate, build.rs runs tonic-prost-build (client and server modules)
tests/ fuzzing/ [internal/]
```

Active modules: `route, acl, l3b, blackhole, forward, decap, nat64, fwstate, dscp, pdump, route-mpls, mirror`. Legacy shape: `pdump` (CGO in `controlplane/ffi.go`, no `bindings/`); `fwstate` partially migrated. `l3b` links virtual-service and session-table objects under `objects/l3b/`. Dataplane symbols exported via meson `--defsym new_module_<name>`.

Shared-memory pattern: `ffi.SharedMemory` -> `shm.AgentAttach(name, instanceIdx, size)` -> write C config through FFI (`<name>_module_config_update()`) with Go memory pinned by `runtime.Pinner` -> dataplane reads it atomically. Exported Go APIs validate C-array index args (device IDs, queue/worker indices) in Go.

Rust CLI: binaries `yanet-cli`, `yanet-cli-<module>`; dependency `ync = { path = "../../../cli/core", package = "yanet-cli" }`; shared protos via `common/rust` `extern_path`. CLIs register in THREE places that move together: root `Cargo.toml` members, root `Makefile` (`CLI_CORE_MODULES`/`CLI_MODULES`), `debian/yanet2-cli.install`; missing either of the last two builds green and is never installed. Private (gitignored) CLIs are standalone workspaces, not root members.

Agents: charters in `.rulesync/subagents/*.md` (`make ai/agents` generates gitignored client trees); public skills in `.agents/skills/<name>/`, symlinked from `.claude/skills/`.

## Conventions

Language rules: `.agents/conventions/{go,rust,c,ts}.md`: read rules for each language you touch. Comments and tests (all languages): `.agents/conventions/comments.md`, `.agents/conventions/tests.md`.

- After native changes, run `meson compile -C build` before direct Go to rebuild C inputs/CGO fingerprints; use `go test -count=1` with configured cache-line/sanitizer CGO flags (`make test`/`make test-asan` supply them). Shared-memory layout or `YANET_MODULE_ABI_VERSION` changes also need rebuilt standalone CGO binaries and coordinated process restarts; stop attached processes before removing stale `/dev/hugepages/yanet*`.
- Test logic and invariants, not human-readable CLI output (ASCII-only paths and canonical MAC/hex rendering are testable contracts).
- Race tests need targeted `-run` repetition (~10-20 runs); one `-race` pass can miss it.
- `modules/*/tests/vm` exit code 127/3 means binaries not staged, not a product defect.
- Benchmarks must exercise the claimed path; `for b.Loop()` suppresses inlining; cross-check allocations against a real caller with `-gcflags=-m`.

## Worktrees

Write in a linked worktree on a task branch from confirmed `origin/main`: `.claude/worktrees/<name>` (Claude Code) or `.agent-state/worktrees/<name>` (Codex). Primary stays on `main`, no tracked-file writes; work on gitignored trees (`.arch/`, agent memory, private modules) there via absolute paths. Confirm `git rev-parse --show-toplevel` and branch before writing. Gates using `build/` (`make test|dataplane|fuzz|test-asan|test-functional`) need a real worktree-local `build/`; go/cargo/npm/lint-only gates may symlink primary `build/` and `*.pb.go`. Never symlink `.claude/agents/`, `.codex/agents/` or `.opencode/agents/` into worktrees.

## Commits & PRs

- Never `git commit`/`--amend` unless asked; never `stash`/`checkout`/`reset`/`restore` to A/B a pre-existing state; read `git show HEAD:<path>` instead.
- Subject: `feat|fix|refactor|perf|chore|docs|test|build|ci|style(<scope>)[!]: brief` - scope mandatory (lowercase, comma-separated), brief lowercase, no trailing period, no code-level detail. Enforced by `commit-msg` hook and Commit Lint CI job.
- No `Co-Authored-By`/`Generated with` footers.
- PR title = commit subject format. Body: bullets start with a capital, end with a period, `Closes #<n>.` when applicable, no `## Summary` header, no test-plan section.
- GitHub is driven with `gh`: reads freely, writes (PR create/merge, comments, issues) only when the task asks.

## Agent memory

`.claude/agent-memory/<agent>/` (Codex: `.agent-state/agent-memory/<agent>/`): `MEMORY.md` has at most 20 `- [summary](file.md)` rows. Lessons: at most 5 lines/600 bytes, only code/build/environment facts absent here; never process rules, counters or retrospectives.

## Dependencies

DPDK v23+ (submodule) · Go 1.27.1+ · Rust 1.99+ (rustfmt nightly-2026-10-22) · Meson 0.61+ · CMake 3.5+ · Protobuf 3.0+ (protoc-gen-go ≥ 1.36.5, protoc-gen-go-grpc ≥ 1.5.1).
