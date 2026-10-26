# Recurring review findings (Codex pattern digest)

Distilled from Codex review rounds on PRs #2943–#3076. These six classes account
for most blocking findings; walk this list explicitly before every push. Each item
names the check, not the fix — the fix depends on the change.

## 1. Shared-memory layout changes

Any edit to a struct that crosses the control-plane/dataplane shm boundary —
field added, removed, reordered, resized, or inserted between existing fields —
is a compatibility break even when the total size is unchanged.

- Bump `DP_CONFIG_READY_MAGIC` when a shm-published struct changes layout.
  `YANET_MODULE_ABI_VERSION` covers only plugins; the magic covers everything
  else (struct agent, registry items, module/device/object structs).
- Mixed-version attach means wrong offsets or wrong free size class, not a
  clean error. A stale `.so` reading old offsets with an unchanged ABI version
  is still a break — bump that too when a module struct shifts.

## 2. Lifetime and reclamation races

The dominant class in the config-generation system. Check every path, not the
happy one:

- Refcount zero-transitions with relaxed atomics need release/acquire ordering;
  plain `++`/`--` racing an atomic on the same variable is a data race.
- Failed `try_lock` handoffs must not drop deferred sweeps — walk the error and
  destroy paths: who unlocks without sweeping, and does that leak permanently?
- Destroy/validation paths may not dereference owner/module/name storage after
  another thread could have reclaimed it — check the window before each lock
  acquisition.
- Hidden refcount holds taken inside a public call need the paired release in
  the documented contract, or existing callers break reclamation.
- Locks (and zero-initialized atomics/spinlocks) in structs built by test
  harnesses must be explicitly initialized.
- Race tests: registered in the suite `make test-tsan` actually selects;
  threads stopped/joined on partial creation failure; bounded retries that
  fail, not hang.

## 3. Renames and compatibility

A rename is only done when every consumer is updated:

- Frozen proto fields are never renamed — add a new field, keep the old one,
  populate both during migration, and prefer the new field when present on
  mixed-version deployments. Proto3 scalars without a presence signal decode
  to their zero value, so plain null-coalescing cannot tell an old sender from
  an explicit default: the new field needs `optional`/`oneof` presence, or the
  migration must define its zero-value fallback explicitly. Consumers: CLI,
  web, Go, Rust.
- Grep the old name across all languages including generated code, CLI help
  text, web tooltips/placeholders, JSDoc, and test failure messages. The web
  build does not type-check by default — that is where stale references hide.
- Parameter and struct-field renames reach constructors, callers, and doc
  comments (`@param`, lock contracts, "device ids" phrasing).

## 4. Safe-Rust wrappers around C

Anything exposing C memory as safe Rust:

- Bounds checks must be real (`if` + error), not `debug_assert!` — release
  builds do the unchecked arithmetic.
- Typed storage handed out over FFI must validate size and alignment against
  the declared type, and never hand out aliasing `&mut` to the same storage.
- Descriptors and layout/size fields published to C must be fully initialized
  — a zeroed value silently disables whatever check consumes it.
- Dropping a wrapper type must reclaim the C resource (mbuf, slot), or the pool
  drains.
- Bit-flag reads: verify the mask against the spec, not the neighboring bit
  (`0x01` vs `0x10` class of bug).

## 5. Tests that cannot fail

- A new test must fail on the defect it pins: check the assertion fails against
  the pre-change implementation (mutate or reason it through).
- The comment says what the assertion verifies ("verifies that …"), and asserts
  it — not the opposite, and not the setup.
- Churn/loop tests must actually exercise the window they claim (entries
  surviving across rounds, not removed every round).

## 6. Comment and doc contracts

Enforced as strictly as code; most P2/P3 findings:

- No internal identifiers in prose — with only the exceptions
  `.agents/conventions/comments.md` itself allows: a doc comment opening with
  its own symbol's name, cross-references whose relationship is the invariant,
  and external contract identifiers that are themselves the contract under
  test (a gRPC code, an HTTP status). Ordinary field and argument names are
  not exempt. Go test comments still open with the test's own name per
  `.agents/conventions/tests.md`. Within those exceptions:
  brief-then-blank-then-detail structure; no comment restating a test name or
  the format string below it; no comments describing rejected alternative
  implementations.
- After any refactor, sweep for stale contracts: locks a function no longer
  takes, `@param`s that no longer exist, semantics comments still describing
  the removed field, "re-deriving" wording after the mechanism changed.
- Docs restating a rule elsewhere (sizes, versions, placeholders) go stale on
  every bump — update them in the same PR.
