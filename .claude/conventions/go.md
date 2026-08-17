# Go conventions

Loaded on demand by the agent writing or reviewing Go; this is the single source for these rules.

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
- **Comments**: English, period-terminated, list production callers only, and follow AGENTS.md's one-line comment rule as a single unwrapped line, even past ~80 columns; an exported doc comment opens with the symbol's own name.
- **Call wrapping**: once a call spans multiple lines, the last argument gets a trailing comma and the closing paren stands alone on its own line — never `"...", counter)` sharing a line with the paren. A leading label may ride the call line (`log.Debug("msg",`); else the open paren stands alone, and what follows may be one argument per line or packed together. `gofmt` accepts every shape, so this is applied by hand.
- **Tests**: table-driven with `require.NoError(t, err)`; production comments never mention tests. `_test.go` uses `package <pkg>_test` and `testpkg` gates it; `package main` is exempt because it is unimportable.
