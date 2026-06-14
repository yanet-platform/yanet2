# raii-sweep triage rules

How to turn a scan flag into a verdict. Every verdict requires reading the function bodies — the scan only points, it never decides.

## Violation classes

### V1 — misnamed fini (`free` that does not free the struct)

The most common and most dangerous class: `<prefix>_free` releases field memory and finalizes the memory context but never deallocates the struct itself — the caller owns it. By convention this function is `fini`. The misleading name is an ownership trap (readers assume the struct is gone; double-free and use-after-free hazards at call sites).

Detection: read the `free` body — if there is no `memory_bfree(ctx, self, sizeof(*self))` / `free(self)` on the struct pointer itself, it is a fini. Confirmed examples at first scan: `lpm_free`, `btree_u*_free`, `radix_free`, `value_table_free`, `remap_table_free`, `filter_free`.

Fix: rename `free` → `fini` (and verify init/fini invariants: idempotent on zero-init, self-cleanup on constructor error paths). If some call sites actually need heap deallocation, that is a separate finding — check what owns the struct there.

### V2 — non-canonical names (`create`/`destroy`/`cleanup`/`release`/`deinit`/`teardown`)

Map by semantics, never mechanically:

- allocates struct + returns pointer → `_new`
- initializes caller-owned struct in place → `_init`
- releases fields, struct survives → `_fini`
- deallocates struct → `_free`

A `create` that both allocates and initializes is simply `new` (new is expected to call init or inline it). A `destroy` that finis and frees is simply `free`. Watch for `cleanup` functions that mix both halves — split is optional; pick the name matching what callers rely on.

### V3 — missing destructor (init/new allocates, nothing releases)

Real bug class (leak), not a rename. Confirm: does init/new allocate memory (directly or via embedded children with their own fini)? Does any code path release it? If not — add the missing `fini`/`free` and wire it into every owner's teardown path.

Before burning budget on a non-obvious leak, a `bug-hunter confirm` dispatch (ASan repro) is allowed but optional — the ASan test gate in Phase 4 is mandatory either way.

### V4 — free without visible allocator pair

`<prefix>_free` exists but nothing named `<prefix>_new/init/create` allocates it (e.g. `cp_*_list_info_free`, `yanet_counter_handle_list_free`). Find the actual allocating function. Verdict is one of:

- allocator is a collector/getter whose primary job is not allocation → acceptable, record `fp-other` with the pairing in notes;
- allocator deserves the `_new` name → V2 rename.

## False positives (record verdict, never re-triage)

From coder-c's `lifecycle-fini-special-cases.md`:

- **No-resource init** — init only assigns scalars/pointers into caller-owned memory and allocates nothing (`spinlock_init`, `tsc_clock_init`, `packet_list_init`, `packet_front_init` into arena memory): no fini needed → `fp-no-fini-needed`.
- **Process-lifetime init** — `dataplane_init`, fuzz-harness `LLVMFuzzerInitialize` call sites: adding fini without a parent teardown is dead code → `fp-no-fini-needed` until the parent gains teardown.
- **Factory structs** — own nothing directly (pages live in downstream containers); an empty placeholder fini that zeroes fields is correct and must say so in a comment.
- **Internal helpers** — double-underscore or clearly-private helpers (`__ttlmap_lock_init`) follow the convention of their owner; flag only if the owner family is broken.
- **Test fixtures** (`test_mempool_create`, `test_ring_init`, …) — lowest value; rename only opportunistically when their family is already in a PR.

## Priority ladder (low risk first)

1. **V3 leaks anywhere** — real bugs outrank all renames.
2. **common/ and lib/ leaf utilities** (header-only, all callers in-tree C, no FFI): `lpm`, `radix`, `btree_u*`, `value_table`, `remap_table`, segments classifiers.
3. **lib/controlplane and lib/dataplane families** (`cp_*_create/free`, ectx families) — wide caller fan-out, still C-only.
4. **modules/*/api and devices/*/api** — renames cross the CGO boundary; Go bindings (`bindings/go/*/cgo.go` or legacy `controlplane/ffi.go`) updated in the same PR by coder-go.
5. **dataplane core init paths** (`dpdk_init`, `worker`, `device`) — highest blast radius, take last and only with a clear teardown story.

## Brief template (coder-c)

The Phase 3 brief must contain:

- absolute worktree path; first action `cd` + `git rev-parse --show-toplevel` to confirm;
- the exact rename map: every `old_symbol → new_symbol`;
- the full consumer list (file:line) you collected in Phase 2 — the coder verifies it, not discovers it;
- invariants to enforce while touching the family: fini idempotent on zero-init, free NULL-safe, constructor self-cleanup on error paths, `// FIXME free X` comments are bugs to resolve, POD-shaped fini may use release-resources + memset;
- prohibitions: no struct layout changes, no adjacent restructuring, no gratuitous doc comments, no banner comments, `//` comment style, no destructive git ops, no `git commit`;
- the closing check the coder must run: `grep -rn '<old_symbol>'` from repo root → zero hits.

For coder-go (when FFI symbols renamed): same worktree, only the `C.<symbol>` call sites and any Go wrapper names; receiver `m`; do not touch unrelated lines or comments.
