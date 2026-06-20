# raii-sweep triage rules

How to turn a scan flag into a verdict. Every verdict requires reading the function bodies — the scanners only point, they never decide.

## The contract (canonical target shapes)

Four orthogonal primitives. `new`/`free` touch only the struct's storage; `init`/`fini` touch only its fields. Reference family: `lib/controlplane/config/cp_chain.c`.

`new` — allocate the struct, nothing else, NULL on OOM:

```c
struct foo *
foo_new(struct memory_context *mctx, ...) {
	struct foo *self = memory_balloc(mctx, sizeof(*self));
	if (self == NULL) {
		return NULL;
	}
	memset(self, 0, sizeof(*self));        // establishes fini's precondition
	SET_OFFSET_OF(&self->memory_context, mctx);
	return self;                            // NO field init, NO child alloc
}
```

`init` — fill fields; one `err:` label that calls `fini`:

```c
int
foo_init(struct foo *self, ...) {
	// self is zero-initialized (by foo_new, or memset here for caller-owned
	// structs) so foo_fini is safe at every failure point below.
	if (child_init(&self->child, ...) != 0) {
		goto err;                       // every error path: goto err
	}
	self->buf = memory_balloc(mctx, n);
	if (self->buf == NULL) {
		goto err;
	}
	return 0;
err:
	foo_fini(self);                         // the ONLY cleanup site
	return -1;
}
```

`fini` — release fields, idempotent, struct survives:

```c
void
foo_fini(struct foo *self) {
	struct buf *b = ADDR_OF(&self->buf);
	memory_bfree(mctx, b, n);               // memory_bfree is NULL-safe (no-op on NULL)
	SET_OFFSET_OF(&self->buf, NULL);        // reset is mandatory: else 2nd fini double-frees
	child_fini(&self->child);               // child_fini must itself be idempotent
}
```

`free` — deallocate the struct, NULL-safe, does NOT call fini:

```c
void
foo_free(struct foo *self) {
	if (self == NULL) {
		return;
	}
	memory_bfree(ADDR_OF(&self->memory_context), self, sizeof(*self));
}
```

Full teardown of a heap object is `foo_fini(p); foo_free(p);` (two calls). `new` is expected to be RARE — most structs are caller-owned and expose only `init`/`fini`.

## Naming axis (A) — renames, tracked in LEDGER.md

### A1 — misnamed fini (`free` that does not free the struct)

A `<prefix>_free` releases field memory but never deallocates the struct itself — the caller owns it. By convention this is `fini`. The misleading name is an ownership trap (readers assume the struct is gone; double-free / UAF at call sites). Detection: read the `free` body — no `memory_bfree(ctx, self, sizeof(*self))` / `free(self)` on the struct pointer → it is a `fini`. Fix: rename `free` → `fini`. If some call sites actually need heap deallocation, that is a separate finding — check what owns the struct there.

### A2 — non-canonical names (`create`/`destroy`/`cleanup`/`release`/`deinit`/`teardown`)

Map by semantics, never mechanically: allocates struct + returns pointer → `_new`; fills caller-owned struct → `_init`; releases fields, struct survives → `_fini`; deallocates struct → `_free`. A `destroy` that finis-and-frees is a `free` only if it also deallocates the struct; if it merely releases fields it is a `fini`. Watch `cleanup` that mixes both halves.

### A3 — free/fini without a visible allocator pair

A `_free`/`_fini` exists but nothing named `_new`/`_init`/`_create` allocates it. Find the actual allocating function. Verdict: allocator is a collector/getter whose primary job is not allocation → `fp-other` (record the pairing); or the allocator deserves the `_new` name → A2 rename.

## Structural axis (B) — correctness, tracked in LEAKS.md

### B1 — missing destructor (leak)

`init`/`new` allocates (directly or via children with their own fini) but no code path releases it. Confirm with a read; add the missing `fini`/`free` and wire it into every owner's teardown. A `bug-hunter confirm` (ASan/LSan repro) before the fix is encouraged; the ASan gate (Phase 4) is mandatory.

### B2 — init error-path defect (leak / double-free / UAF) — the archetype

The `lib/filter/compiler/` `*_attr_init` family is the model. Each init allocates a wrapper with `memory_balloc`, publishes it via `SET_OFFSET_OF(data, wrapper)`, runs a builder. The driver `filter_init` (`compiler.h`) calls each `.init`; on any failure it calls each lookup's `.free` via `if (v->data != NULL) free(ADDR_OF(&v->data))`. So the init contract is: **on failure, either leave `*data` pointing at a fully-owned object the free func can release, OR reset `*data` to NULL (`SET_OFFSET_OF(data, NULL)` stores 0, gating the free off) and release everything yourself.** `device.c`/`ipfrag.c`/`vlan.c` honor it (reference). The two failure modes:

- **dangling `*data`** — init frees the wrapper/children but leaves `*data` pointing at freed memory → the driver re-enters the free func → double-free / UAF.
- **leaked wrapper** — init nulls `*data` but forgets to `memory_bfree` the wrapper struct → leak each failed update.

Canonical fix (match `device.c`): NULL-check the `memory_balloc`; on builder failure `SET_OFFSET_OF(data, NULL)` then `memory_bfree(ctx, wrapper, sizeof(*wrapper))`; ensure the free func early-returns on NULL. Reachability: ACL/forward config-update under arena/OOM exhaustion (`filter_init` runs on every filter rebuild). This same shape generalizes beyond the compiler: any init whose error path leaves an offset/child dangling, or leaks a partial allocation, is B2.

### B3 — non-idempotent fini

`fini` frees a child but does not reset it to NULL/zero after release, so a second fini double-frees the still-non-NULL pointer (and a fini on a partially-initialized struct dereferences garbage). `memory_bfree` is now NULL-safe (a NULL block is a no-op), so the *bfree itself* no longer corrupts on NULL — but the **reset is still mandatory** because the offset is still non-NULL after the free. Fix: release each child (`memory_bfree`/child-fini) then `SET_OFFSET_OF(&self->x, NULL)`. A trailing `memset(self, 0, sizeof(*self))` is the blunt form and is fine for POD-shaped finis. (The L-RIDX class — `memory_bfree(ctx, NULL, nonzero)` corrupting the arena — is closed at the primitive as of the `memory_bfree` NULL-safety change; verified by a full 87-site audit that found zero live breaches, all sites saved by either count-tracks-pointer or an explicit guard.)

**Idempotency is transitive.** A fini is idempotent only if every child fini/free it calls is itself idempotent. A fini may legitimately delegate — `cp_chain_fini` just calls `counter_registry_fini`, which ends in `memset(registry, 0, ...)`; that is conforming, not a B3, because the child resets internally. The defect is a *leaf* fini that frees a member but never resets it: `lpm_free` (`common/lpm.h`) bfrees `lpm->pages` without `SET_OFFSET_OF(&lpm->pages, NULL)`, so a second call double-frees and it poisons every parent fini that embeds a `struct lpm` (nat64/route/forward). Header-only static-inline leaves (`lpm`, `btree_*`, `radix`) escape the `.c`-only scanner — the leak-hunt workflow checks them.

### B4 — non-NULL-safe free (dereferences self without a guard)

A `_free(self)` **dereferences** `self` (`self->x` / `self[i]`) without `if (self == NULL) return;`. Note `free(NULL)` and `memory_bfree(ctx, NULL, 0)` are already no-ops, so a `_free` that merely passes `self` to `free`/`memory_bfree` is NULL-safe even without an explicit guard — only a real dereference is the defect. The archetype is the agent.c info-list collectors: `cp_function_list_info_free` / `cp_agent_list_info_free` loop over `self->count` before the final `free`, and their producers return NULL on OOM into a Go `defer ..._free(ptr)` with no nil check. The bare-`free` siblings (`dp_module_list_info_free`) are correctly NULL-tolerant. Fix: add the guard (and ideally make the Go caller/producer nil-safe too). **Applies to `_free` only.** A `_fini` gets a valid `self` by contract — its idempotency concern is partial/zero *fields*, not a NULL `self` — so a `_fini` without a `self == NULL` guard is NOT a B4.

### B5 — orthogonality break

`new` that also initializes fields / allocates children (should be pure alloc), or `free` that calls `fini` (should be pure dealloc). **Decision-gated, low priority.**

**Settled policy (2026-06-20):** `new = pure alloc` binds leaf/utility structs (`lpm`, `btree_*`, `value_table`, …) and the `cp_*` config quartet (`cp_chain`/`cp_function`/`cp_pipeline`/`cp_device`). Module-config and device **vtable constructors may stay fused** (`new` = `balloc + cp_module_init + *_data_init`) — that is an accepted shape, NOT a B5 defect; do not flag or split it. Only flag B5 when a genuinely-leaf/util or `cp_*`-family `new` fuses init, or for a brand-new constructor written fused.

The codebase tolerates fused module-config constructors (`decap/dscp/route/pdump/route-mpls/blackhole/fwstate_module_config_new`, `cp_device_plain/vlan_new` — all renamed `create→new` in earlier runs while staying fused) and composite `free = fini + free` destructors. Note a `free` calling `fini` is often **conforming**, not a defect: it is the documented "full teardown of a heap object = fini then free" when an orthogonal `fini` already exists and is independently reused on stack/embedded instances (`cp_device_config_fini` at `zone.c`; `route_mpls_module_config_fini` reused by the `_update` error path). Splitting those would break the registry/Go-binding single-callback contract. Do NOT mass-split without the user's explicit call; splitting `new` into `new`+`init` is wide churn and reverses recently-merged work. Flag and record; pick up only when the user opts in or when a family is already open for another reason.

### B6 — constructor reads uninitialized fields on its error path (garbage-read)

A constructor allocates the struct WITHOUT zero-init (no `memset`, no full per-field init) and its error path calls a destructor that reads a field the init never set → wild read / wild free. Archetype: `counter_storage_spawn` (`lib/counters/counters.c`) `memory_balloc`s without memset, `counter_storage_init` sets only 3 offsets (never `counter_value_handles`), and any early `goto error` calls `counter_storage_free` which reads `ADDR_OF(&storage->counter_value_handles)` on garbage. This is the most dangerous shape because the teardown call is correct in *form*. Fix: zero-init the struct up front (so the destructor's field reads are well-defined) or have init set every field before the first failure point. Non-canonical constructor verbs (`spawn`/`copy`/`clone`/`build`) hide this class — they are now in the scanner's constructor vocabulary.

## Cross-cutting invariants (assumed by the contract, checked by reading)

These are not separate scan buckets — they are the properties the single-`err:`/idempotent-`fini` discipline silently depends on. Verify them when auditing any non-trivial init/fini:

- **Zero-init before the first goto.** A caller-owned `init` must `memset`/zero `self` (or `new` must have) before any error path can call `fini` — else fini reads garbage (B6). The reference `cp_chain_new` memsets; `cp_chain_init` then relies on it.
- **Partial-publish pairing.** Every field published via `SET_OFFSET_OF(&self->x, child)` must, on every later error path, be either un-published (`SET_OFFSET_OF(&self->x, NULL)`) or owned-and-released by exactly one path. The B2 `device.c` shape generalizes: publish late, or null on error. A field published early and freed both locally and by the fini is a double-free.
- **memory_context teardown ordering.** When a struct embeds a `memory_context` and charges child allocations to it, the fini must free those children BEFORE `memory_context_fini` (which reclaims the whole context). `route_module_config_free` calls `data_fini` (frees arrays from the module's context) before `cp_module_fini` (finis that context); reversing them is a use-after-fini.
- **Non-owning aliases / borrowed offsets.** A child may be referenced from an owning tree AND a non-owning registry/index. Exactly one owner frees it; the registry's fini must free only its own items array, never the borrowed objects (`cp_counter.c` counter-storage registry deliberately does not free the storages the ectx tree owns). The safety rests on a free-order invariant — flag any registry fini that frees borrowed offsets, and treat a registry that hands out live offsets into another subsystem's objects as a latent-UAF note, not a clean pass.

## False positives (record verdict, never re-triage)

- **No-resource init** — init only assigns scalars/pointers into caller-owned memory and allocates nothing (`spinlock_init`, `tsc_clock_init`, `packet_front_init` into arena): no fini needed → `fp-no-fini-needed`.
- **Process-lifetime init** — `dataplane_init`, `dp_storage_init`, fuzz `LLVMFuzzerInitialize`: adding fini without a parent teardown is dead code → `fp-no-fini-needed` until the parent gains teardown. (`dp_storage_init`'s unchecked `cp_agent_registry` alloc is in this class — process-lifetime bootstrap.)
- **Stacked-ladder init is NOT a defect.** A leak-free init with multiple fall-through `error_xxx:` labels (e.g. `nat64_module_config_data_init`, `cp_device_config_init`) is correct — it is merely not the preferred single-`err:`+idempotent-`fini` shape. The read-test that separates a leak-free ladder from a leaky one: each error label must free *exactly* the resources live at its entry point, none twice, in reverse allocation order. If that holds it is style-only — convert opportunistically (low priority); NEVER record it as a leak. A fused `_create`/`_new` paired with a matching `_free` that every `goto error` reaches is the same: leak-free, decision-gated, not a defect.
- **container_of module-config free guarded by the Go binding.** A `<mod>_module_config_free(struct cp_module *cp_module)` recovers the struct via `container_of` and dereferences it without a C-side NULL guard, but its only caller is the Go binding `ModuleConfig.Free()` which guards `if ptr := m.asRawPtr(); ptr != nil` before the CGO call. The C side never sees NULL → adding a guard is redundant, not a fix → `fp-other` (note the Go guard). The genuine B4s are the agent.c info-list collectors whose NULL flows from an OOM producer into a guardless Go `defer`.
- **Bare `free(self)` / `memory_bfree(ctx, self, sizeof)` with no deref** — NULL-safe already (libc `free(NULL)` and `memory_bfree(.,.,0)` are no-ops); a missing explicit guard is not a B4 unless the body dereferences `self`.
- **Factory structs** — own nothing directly (pages live in downstream containers); an empty placeholder fini that zeroes fields is correct and must say so in a comment.
- **Slot-reclaim / collector / DPDK callback frees** — `worker_packet_free` (mbuf returned to pool), `data_pipe_item_free` (ring slot), `fwstate_outdated_layers_free` (collector pair), `sock_dev_queue_release` (DPDK `eth_dev_ops` callback): not heap lifecycle → `fp-other`, do not touch DPDK callbacks at all.
- **Internal helpers** — double-underscore / clearly-private helpers (`__ttlmap_lock_init`) follow their owner's convention; flag only if the owner family is broken.
- **Test fixtures** (`test_mempool_create`, `test_ring_init`, …) — lowest value; rename only opportunistically when their family is already in a PR.

## Priority ladder (low risk first)

1. **B1/B2/B3 structural defects anywhere** — real leaks/double-free/UAF outrank all renames and all B5.
2. **common/ and lib/ leaf utilities** (header-only, all callers in-tree C, no FFI): `lpm`, `radix`, `btree_u*`, `value_table`, `remap_table`, segments classifiers.
3. **lib/controlplane and lib/dataplane families** (`cp_*`, ectx families) — wide caller fan-out, still C-only.
4. **modules/*/api and devices/*/api** — renames cross the CGO boundary; Go bindings updated in the same PR by coder-go.
5. **dataplane core init paths** (`dpdk_init`, `worker`, `device`) — highest blast radius, take last with a clear teardown story.
6. **B5 orthogonality + style (stacked→single-label)** — decision-gated / opportunistic, lowest priority.

## Brief template (coder-c)

For a **rename** the brief must contain:

- absolute worktree path; first action `cd` + `git rev-parse --show-toplevel` to confirm;
- the exact rename map: every `old_symbol → new_symbol`;
- the full consumer list (file:line) from Phase 2 — the coder verifies it, not discovers it;
- invariants to enforce while touching the family: fini idempotent (NULL-guard + reset each child), free NULL-safe, init zero-inits self and unwinds via a single `err:` → fini, `// FIXME free X` comments are bugs to resolve;
- prohibitions: no struct layout changes, no adjacent restructuring, no gratuitous doc comments, no banner comments, `//` comment style, no destructive git ops, no `git commit`;
- closing check: `grep -rn '<old_symbol>'` from repo root → zero hits.

For a **B-class fix** the brief instead names: the exact defect (file:line, mechanism — leak / double-free / UAF / OOM-deref), the canonical fix shape (e.g. the B2 `SET_OFFSET_OF(data, NULL)` + `memory_bfree(wrapper)` pattern), the reference function it should match (`device.c`, `cp_chain.c`), and the same prohibitions. Require an ASan-clean local run.

For **coder-go** (when FFI symbols renamed): same worktree, only the `C.<symbol>` call sites and any Go wrapper names; receiver `m`; do not touch unrelated lines or comments.
