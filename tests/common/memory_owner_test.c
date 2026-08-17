// Functional tests for struct memory_owner: a block_allocator that grows
// on demand by borrowing arenas from a parent memory context and returns
// them wholesale on release.

#include "common/memory.h"
#include "common/memory_owner.h"
#include "common/numutils.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

#define PARENT_ARENA_ALIGN (1u << 21) // 2 MiB
#define PARENT_RAW_SZ ((size_t)(1u << 22))

// A parent memory_context over one 2 MiB arena, the role cp_config's
// context plays for real agents.
struct parent_fixture {
	void *raw;
	void *arena;
	struct block_allocator ba;
	struct memory_context ctx;
};

static int
parent_fixture_init(struct parent_fixture *fx) {
	fx->raw = malloc(PARENT_RAW_SZ + PARENT_ARENA_ALIGN);
	TEST_ASSERT(fx->raw != NULL, "failed to allocate parent raw buffer");
	uintptr_t aligned = ((uintptr_t)fx->raw + PARENT_ARENA_ALIGN - 1) &
			    ~(uintptr_t)(PARENT_ARENA_ALIGN - 1);
	fx->arena = (void *)aligned;

	TEST_ASSERT(
		block_allocator_init(&fx->ba) == 0,
		"parent allocator init failed"
	);
	block_allocator_put_arena(&fx->ba, fx->arena, PARENT_ARENA_ALIGN);
	TEST_ASSERT(
		memory_context_init(&fx->ctx, "parent", &fx->ba) == 0,
		"parent context init failed"
	);
	return 0;
}

static void
parent_fixture_fini(struct parent_fixture *fx) {
	memory_context_fini(&fx->ctx);
	free(fx->raw);
}

// A context bound to the owner's allocator, the shape module contexts
// will take: named, allocating through the owner, not spliced anywhere.
static int
owner_context_init(
	struct memory_context *ctx, struct memory_owner *owner, const char *name
) {
	TEST_ASSERT(
		memory_context_init(ctx, name, &owner->allocator) == 0,
		"owner-bound context init failed"
	);
	return 0;
}

// Splices an owner-bound context into a parent context's child list under
// the parent's allocator lock, reproducing the shape the cp_module
// lifecycle will create: child list on the parent, allocations on the
// owner's separate allocator.
static void
owner_child_splice(
	struct memory_context *child,
	struct memory_context *parent,
	struct block_allocator *parent_alloc
) {
	spinlock_lock(&parent_alloc->lock);
	SET_OFFSET_OF(&child->parent, parent);
	EQUATE_OFFSET(&child->next_sibling, &parent->first_child);
	SET_OFFSET_OF(&parent->first_child, child);
	spinlock_unlock(&parent_alloc->lock);
}

// Growth on exhaustion: the owner starts empty, the first allocation
// borrows one 64 KiB granule, and a request larger than the granule grows
// to the next power of two covering it.
static int
test_grow_on_exhaustion(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	struct memory_owner owner;
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner") == 0,
		"memory_owner_init failed"
	);
	TEST_ASSERT(owner.arena_count == 0, "fresh owner must have no arenas");

	struct memory_context octx;
	TEST_ASSERT(
		owner_context_init(&octx, &owner, "owner-ctx") == 0,
		"owner-bound context init failed"
	);

	size_t parent_base = block_allocator_free_size(&fx.ba);

	void *p = memory_balloc(&octx, 128);
	TEST_ASSERT(p != NULL, "first owner allocation must trigger growth");
	TEST_ASSERT(owner.arena_count == 1, "first allocation must grow once");

	struct memory_arena *arenas = ADDR_OF(&owner.arenas);
	TEST_ASSERT(
		arenas[0].size ==
			MEMORY_OWNER_MIN_GRANULE - 2 * ASAN_RED_ZONE,
		"small request must borrow the minimum granule, got %" PRIu64,
		arenas[0].size
	);
	// One whole granule block is ingested; the 128-byte request takes a
	// single pool block out of it. Express both through the allocator's
	// pool math so red-zone inflation stays exact.
	size_t ingested = block_allocator_pool_size(
		&owner.allocator,
		block_allocator_pool_index(
			&owner.allocator,
			(size_t)arenas[0].size + 2 * ASAN_RED_ZONE
		)
	);
	size_t taken = block_allocator_pool_size(
		&owner.allocator,
		block_allocator_pool_index(
			&owner.allocator, 128 + 2 * ASAN_RED_ZONE
		)
	);
	TEST_ASSERT(
		block_allocator_free_size(&owner.allocator) == ingested - taken,
		"fresh granule must be one whole block minus the request taken "
		"from it"
	);
	// Both the granule and the arenas array consume pool-rounded blocks
	// in the parent, red zones included; compute them with the
	// allocator's own pool math to stay exact in sanitized builds too.
	size_t granule_block = block_allocator_pool_size(
		&fx.ba,
		block_allocator_pool_index(&fx.ba, MEMORY_OWNER_MIN_GRANULE)
	);
	size_t array_internal =
		owner.arena_capacity * sizeof(struct memory_arena) +
		2 * ASAN_RED_ZONE;
	size_t array_block = block_allocator_pool_size(
		&fx.ba, block_allocator_pool_index(&fx.ba, array_internal)
	);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) ==
			parent_base - granule_block - array_block,
		"parent must lose exactly one granule plus the arenas array "
		"to the borrow"
	);

	memory_bfree(&octx, p, 128);

	// A request above the minimum granule borrows the covering pool
	// block, minus the parent's own red-zone pair.
	void *big = memory_balloc(&octx, 200000);
	TEST_ASSERT(big != NULL, "grow-to-need allocation failed");
	TEST_ASSERT(owner.arena_count == 2, "second allocation must grow");
	arenas = ADDR_OF(&owner.arenas);
	TEST_ASSERT(
		arenas[1].size ==
			block_allocator_pool_size(
				&owner.allocator,
				block_allocator_pool_index(
					&owner.allocator,
					200000 + 2 * ASAN_RED_ZONE
				)
			) -
				2 * ASAN_RED_ZONE,
		"large request must borrow the covering pool block"
	);
	memory_bfree(&octx, big, 200000);

	memory_owner_release_all(&owner);
	memory_context_fini(&octx);
	parent_fixture_fini(&fx);
	return 0;
}

// release_all returns every borrowed byte to the parent, and the owner
// re-inits and keeps working after a release.
static int
test_release_all_returns_arenas(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	size_t parent_base = block_allocator_free_size(&fx.ba);

	struct memory_owner owner;
	struct memory_context octx;
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner") == 0,
		"memory_owner_init failed"
	);
	TEST_ASSERT(
		owner_context_init(&octx, &owner, "owner-ctx") == 0,
		"owner-bound context init failed"
	);

	void *slots[6] = {0};
	size_t sizes[6] = {8, 128, 512, 4096, 20000, 100000};
	for (size_t idx = 0; idx < 6; ++idx) {
		slots[idx] = memory_balloc(&octx, sizes[idx]);
		TEST_ASSERT(
			slots[idx] != NULL, "alloc of %zu failed", sizes[idx]
		);
	}
	for (size_t idx = 0; idx < 6; ++idx) {
		memory_bfree(&octx, slots[idx], sizes[idx]);
	}

	TEST_ASSERT(
		owner.arena_count > 0, "allocations must have grown the owner"
	);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) < parent_base,
		"parent must hold borrowed arenas before release"
	);

	memory_owner_release_all(&owner);

	TEST_ASSERT(owner.arena_count == 0, "release must reset arena_count");
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"release_all must return every borrowed byte to the parent"
	);

	// Re-init and reuse after a release.
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner2") == 0,
		"re-init after release failed"
	);
	void *p = memory_balloc(&octx, 64);
	TEST_ASSERT(p != NULL, "allocation after re-init failed");
	TEST_ASSERT(owner.arena_count == 1, "re-init owner must grow afresh");
	memory_bfree(&octx, p, 64);
	memory_owner_release_all(&owner);

	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"second release must also return every borrowed byte"
	);

	memory_context_fini(&octx);
	parent_fixture_fini(&fx);
	return 0;
}

// memory_brealloc inside an owner-bound context, including a realloc big
// enough to trigger growth for the destination.
static int
test_realloc_in_owner_context(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	struct memory_owner owner;
	struct memory_context octx;
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner") == 0,
		"memory_owner_init failed"
	);
	TEST_ASSERT(
		owner_context_init(&octx, &owner, "owner-ctx") == 0,
		"owner-bound context init failed"
	);

	uint8_t *a = memory_balloc(&octx, 100);
	TEST_ASSERT(a != NULL, "initial alloc failed");
	for (size_t idx = 0; idx < 100; ++idx) {
		a[idx] = (uint8_t)idx;
	}
	uint64_t arenas_before = owner.arena_count;

	// 100000 > 64 KiB, so the destination allocation grows a new granule.
	uint8_t *b = memory_brealloc(&octx, a, 100, 100000);
	TEST_ASSERT(b != NULL, "realloc with growth failed");
	TEST_ASSERT(
		owner.arena_count > arenas_before,
		"growing realloc must borrow a new arena"
	);
	for (size_t idx = 0; idx < 100; ++idx) {
		TEST_ASSERT(
			b[idx] == (uint8_t)idx,
			"realloc corrupted payload at %zu",
			idx
		);
	}

	// Shrink back: no growth, payload preserved.
	uint8_t *c = memory_brealloc(&octx, b, 100000, 64);
	TEST_ASSERT(c != NULL, "shrinking realloc failed");
	for (size_t idx = 0; idx < 64; ++idx) {
		TEST_ASSERT(
			c[idx] == (uint8_t)idx,
			"shrink corrupted payload at %zu",
			idx
		);
	}
	memory_bfree(&octx, c, 64);

	// realloc-to-zero frees.
	uint8_t *d = memory_balloc(&octx, 32);
	TEST_ASSERT(d != NULL, "alloc before realloc-to-zero failed");
	TEST_ASSERT(
		memory_brealloc(&octx, d, 32, 0) == NULL,
		"realloc to zero must return NULL"
	);

	memory_owner_release_all(&owner);
	memory_context_fini(&octx);
	parent_fixture_fini(&fx);
	return 0;
}

// Single-threaded shape of the memory_context_fini parent-lock fix: a
// child bound to a different allocator than the parent unlinks cleanly.
static int
test_fini_owner_child_under_foreign_parent(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	struct memory_owner owner;
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner") == 0,
		"memory_owner_init failed"
	);

	// Two owner-bound children plus one plain child on the same parent.
	struct memory_context ob1, ob2, plain;
	owner_context_init(&ob1, &owner, "ob1");
	owner_context_init(&ob2, &owner, "ob2");
	owner_child_splice(&ob1, &fx.ctx, &fx.ba);
	owner_child_splice(&ob2, &fx.ctx, &fx.ba);
	TEST_ASSERT(
		memory_context_init_from(&plain, &fx.ctx, "plain") == 0,
		"plain child init failed"
	);

	size_t children = 0;
	struct memory_context *child = ADDR_OF(&fx.ctx.first_child);
	while (child != NULL) {
		++children;
		child = ADDR_OF(&child->next_sibling);
	}
	TEST_ASSERT(
		children == 3, "parent must hold 3 children, got %zu", children
	);

	// Fini a middle child: the unlink touches the parent's list, the
	// detach its own; allocators differ for the owner-bound ones.
	memory_context_fini(&ob1);
	children = 0;
	child = ADDR_OF(&fx.ctx.first_child);
	while (child != NULL) {
		TEST_ASSERT(
			child != &ob1, "fini'd owner child must be unlinked"
		);
		++children;
		child = ADDR_OF(&child->next_sibling);
	}
	TEST_ASSERT(
		children == 2, "parent must hold 2 children, got %zu", children
	);

	memory_context_fini(&ob2);
	memory_context_fini(&plain);
	TEST_ASSERT(
		ADDR_OF(&fx.ctx.first_child) == NULL,
		"parent must have no children left"
	);

	memory_owner_release_all(&owner);
	parent_fixture_fini(&fx);
	return 0;
}

#if defined(YANET_DEBUG)
// Wrong-allocator tripwire: a block borrowed by the parent's own
// allocator, freed through the owner-bound context, must abort.
static void
phase_tripwire_wrong_free(void) {
	struct parent_fixture fx;
	if (parent_fixture_init(&fx) != 0) {
		_exit(3);
	}

	struct memory_owner owner;
	struct memory_context octx;
	if (memory_owner_init(&owner, &fx.ctx, "owner") != 0) {
		_exit(3);
	}
	if (owner_context_init(&octx, &owner, "owner-ctx") != 0) {
		_exit(3);
	}

	// Prime the owner so its tripwire state is fully built.
	void *own = memory_balloc(&octx, 64);
	if (own == NULL) {
		_exit(3);
	}

	void *foreign = memory_balloc(&fx.ctx, 64);
	if (foreign == NULL) {
		_exit(3);
	}

	memory_bfree(&octx, foreign, 64);

	// Not reached when the tripwire fires.
	_exit(0);
}

static int
test_tripwire_wrong_free_aborts(void) {
	pid_t pid = fork();
	TEST_ASSERT(pid >= 0, "fork failed");
	if (pid == 0) {
		phase_tripwire_wrong_free();
		_exit(0);
	}
	int status = 0;
	TEST_ASSERT(waitpid(pid, &status, 0) == pid, "waitpid failed");
	TEST_ASSERT(
		WIFSIGNALED(status) && WTERMSIG(status) == SIGABRT,
		"freeing a foreign block through the owner-bound context must "
		"abort (status %d)",
		status
	);
	return 0;
}
#else
static int
test_tripwire_wrong_free_aborts(void) {
	LOG(INFO, "tripwire compiled out (non-debug build), skipping");
	return 0;
}
#endif

// A growth failure must surface as a clean NULL from balloc with no lock
// left held and no arena half-tracked. The owner's own embedded
// allocator is the only growth binding, so both entry points — a
// context bound to the owner and the raw allocator — share one
// contract.
static int
test_grow_failure_propagates(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	struct memory_owner owner;
	struct memory_context octx;
	TEST_ASSERT(
		memory_owner_init(&owner, &fx.ctx, "owner") == 0,
		"memory_owner_init failed"
	);
	TEST_ASSERT(
		owner_context_init(&octx, &owner, "owner-ctx") == 0,
		"owner-bound context init failed"
	);

	// Exhaust the parent so the owner's own growth fails.
	void *drain = memory_balloc(
		&fx.ctx, block_allocator_free_size(&fx.ba) - 2 * ASAN_RED_ZONE
	);
	TEST_ASSERT(drain != NULL, "parent drain failed");

	TEST_ASSERT(
		memory_balloc(&octx, 4096) == NULL,
		"balloc must return NULL when growth fails"
	);
	TEST_ASSERT(
		owner.arena_count == 0,
		"failed growth must not leave a tracked arena"
	);
	TEST_ASSERT(
		block_allocator_free_size(&owner.allocator) == 0,
		"owner free_size after failed growth proves its lock is "
		"released and nothing was ingested"
	);

	// Same contract through the raw embedded allocator: the failed
	// grow falls through to the final rescan, which still finds
	// nothing, and both locks come back released.
	TEST_ASSERT(
		block_allocator_balloc(&owner.allocator, 16) == NULL,
		"failing grow must yield NULL"
	);
	TEST_ASSERT(
		block_allocator_free_size(&owner.allocator) == 0,
		"free_size after the raw-allocator failure proves the owner "
		"lock is released"
	);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == 0,
		"drained parent free_size proves the parent lock is released"
	);

	// The owner still works for blocks already inside its arenas once
	// the parent recovers: release the drain, grow again.
	memory_bfree(&fx.ctx, drain, PARENT_ARENA_ALIGN - 2 * ASAN_RED_ZONE);
	void *p = memory_balloc(&octx, 4096);
	TEST_ASSERT(p != NULL, "owner must recover after parent recovers");
	memory_bfree(&octx, p, 4096);

	memory_owner_release_all(&owner);
	memory_context_fini(&octx);
	parent_fixture_fini(&fx);
	return 0;
}

// Owners chain through contexts: B borrows from a context bound to A's
// allocator, so growing B cascades B -> A -> root. This exercises the
// owner -> parent lock nesting the grow and release comments promise,
// and the children-first teardown returns the root to its baseline.
static int
test_chained_owners(void) {
	struct parent_fixture fx;
	TEST_ASSERT(parent_fixture_init(&fx) == 0, "fixture init failed");

	size_t parent_base = block_allocator_free_size(&fx.ba);

	struct memory_owner owner_a;
	TEST_ASSERT(
		memory_owner_init(&owner_a, &fx.ctx, "A") == 0,
		"owner A init failed"
	);
	struct memory_context actx;
	TEST_ASSERT(
		memory_context_init(&actx, "a-ctx", &owner_a.allocator) == 0,
		"A-bound context init failed"
	);

	struct memory_owner owner_b;
	TEST_ASSERT(
		memory_owner_init(&owner_b, &actx, "B") == 0,
		"owner B init failed"
	);
	struct memory_context bctx;
	TEST_ASSERT(
		memory_context_init(&bctx, "b-ctx", &owner_b.allocator) == 0,
		"B-bound context init failed"
	);

	// One allocation through B cascades: B misses, borrows its granule
	// from A, A misses, borrows from the root. B's granule consumes
	// A's whole ingested block, so B's arenas array then forces a
	// second A granule.
	void *p = memory_balloc(&bctx, 256);
	TEST_ASSERT(p != NULL, "chained allocation failed");
	TEST_ASSERT(owner_b.arena_count == 1, "B must borrow from A");
	size_t b_ask = MEMORY_OWNER_MIN_GRANULE - 2 * ASAN_RED_ZONE;
	TEST_ASSERT(
		ADDR_OF(&owner_b.arenas)[0].size == b_ask,
		"B granule must be minimum-sized"
	);

	size_t a_granule = b_ask;
	size_t ia_block = block_allocator_pool_size(
		&owner_a.allocator,
		block_allocator_pool_index(
			&owner_a.allocator, a_granule + 2 * ASAN_RED_ZONE
		)
	);
	size_t gb_block = block_allocator_pool_size(
		&owner_a.allocator,
		block_allocator_pool_index(&owner_a.allocator, b_ask)
	);
	size_t ab_block = block_allocator_pool_size(
		&owner_a.allocator,
		block_allocator_pool_index(
			&owner_a.allocator,
			4 * sizeof(struct memory_arena) + 2 * ASAN_RED_ZONE
		)
	);
	uint64_t expect_a_count = (ia_block - gb_block >= ab_block) ? 1 : 2;

	TEST_ASSERT(
		owner_a.arena_count == expect_a_count,
		"A must grow exactly %" PRIu64 " times, got %" PRIu64,
		expect_a_count,
		owner_a.arena_count
	);
	struct memory_arena *a_arenas = ADDR_OF(&owner_a.arenas);
	for (uint64_t idx = 0; idx < owner_a.arena_count; ++idx) {
		TEST_ASSERT(
			a_arenas[idx].size == a_granule,
			"every A granule must be %zu bytes",
			a_granule
		);
	}

	// Byte accounting at A while B holds its borrow: whole granule
	// blocks ingested, B's granule and B's arenas array taken out,
	// expressed through the allocator's own pool math so red zones
	// stay exact in sanitized builds.
	TEST_ASSERT(
		block_allocator_free_size(&owner_a.allocator) ==
			expect_a_count * ia_block - gb_block - ab_block,
		"A must account for exactly B's granule and arenas array"
	);

	memory_bfree(&bctx, p, 256);

	// Teardown children-first: releasing B returns its granule and
	// array to A's pools, then releasing A returns the root to its
	// exact baseline.
	memory_owner_release_all(&owner_b);
	TEST_ASSERT(owner_a.arena_count == expect_a_count, "A outlives B");
	TEST_ASSERT(
		block_allocator_free_size(&owner_a.allocator) ==
			expect_a_count * ia_block,
		"releasing B must return its whole borrow to A's pools"
	);
	memory_owner_release_all(&owner_a);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"chained teardown must return the root to its exact baseline"
	);

	memory_context_fini(&bctx);
	memory_context_fini(&actx);
	parent_fixture_fini(&fx);
	return 0;
}

// A grow-to-need request just under the parent's public maximum must
// still borrow successfully under ASan: the parent block class is
// derived from the requesting pool, and asking for the block minus the
// parent's red-zone pair keeps its own addition inside the same class
// instead of rounding past the public maximum.
static int
test_asan_max_range_grow(void) {
	size_t arena_sz = ((size_t)128u << 20); // 128 MiB
	size_t arena_align = ((size_t)128u << 20);
	void *raw = malloc(arena_sz + arena_align);
	TEST_ASSERT(raw != NULL, "failed to allocate max-range raw buffer");
	uintptr_t aligned = ((uintptr_t)raw + arena_align - 1) &
			    ~(uintptr_t)(arena_align - 1);
	void *arena = (void *)aligned;

	struct block_allocator ba;
	struct memory_context ctx;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "parent allocator init failed"
	);
	block_allocator_put_arena(&ba, arena, arena_sz);
	TEST_ASSERT(
		memory_context_init(&ctx, "max-range-parent", &ba) == 0,
		"parent context init failed"
	);
	size_t parent_base = block_allocator_free_size(&ba);

	struct memory_owner owner;
	TEST_ASSERT(
		memory_owner_init(&owner, &ctx, "max-range-owner") == 0,
		"owner init failed"
	);
	struct memory_context octx;
	TEST_ASSERT(
		memory_context_init(&octx, "max-range-alloc", &owner.allocator) ==
			0,
		"owner context init failed"
	);

	// Above the 32 MiB rounding midpoint: grows through a 64 MiB-class
	// parent block without the red zones pushing it a pool higher.
	size_t request = ((size_t)40u << 20);
	void *block = memory_balloc(&octx, request);
	TEST_ASSERT(block != NULL, "max-range grow failed under red zones");
	memset(block, 0x5a, request);
	memory_bfree(&octx, block, request);

	TEST_ASSERT(owner.arena_count == 1, "one grow must cover the request");
	memory_owner_release_all(&owner);
	TEST_ASSERT(
		block_allocator_free_size(&ba) == parent_base,
		"release must return the granule"
	);

	memory_context_fini(&octx);
	memory_context_fini(&ctx);
	free(raw);
	return 0;
}

#if ASAN_RED_ZONE > 0
// Child phase for the wholesale-release poisoning test: fills one granule
// with two half-block allocations, releases the owner wholesale, then
// reads the upper allocation. The release must poison the entire
// pool-rounded block, not just the public granule, or the read survives.
static void
phase_release_poisons_upper_half(void) {
	size_t arena_sz = ((size_t)1 << 20);
	size_t arena_align = ((size_t)1 << 20);
	void *raw = malloc(arena_sz + arena_align);
	if (raw == NULL) {
		_exit(3);
	}
	uintptr_t aligned = ((uintptr_t)raw + arena_align - 1) &
			    ~(uintptr_t)(arena_align - 1);

	struct block_allocator ba;
	if (block_allocator_init(&ba) != 0) {
		_exit(3);
	}
	block_allocator_put_arena(&ba, (void *)aligned, arena_sz);

	struct memory_context ctx;
	if (memory_context_init(&ctx, "poison-parent", &ba) != 0) {
		_exit(3);
	}
	struct memory_owner owner;
	if (memory_owner_init(&owner, &ctx, "poison-owner") != 0) {
		_exit(3);
	}
	struct memory_context octx;
	if (memory_context_init(&octx, "poison-alloc", &owner.allocator) != 0) {
		_exit(3);
	}

	size_t granule_block = block_allocator_pool_size(
		&owner.allocator,
		block_allocator_pool_index(
			&owner.allocator, MEMORY_OWNER_MIN_GRANULE
		)
	);
	size_t half_req = granule_block / 2 - 2 * ASAN_RED_ZONE;
	void *first = memory_balloc(&octx, half_req);
	void *second = memory_balloc(&octx, half_req);
	if (first == NULL || second == NULL) {
		_exit(3);
	}

	memory_owner_release_all(&owner);

	volatile unsigned char *upper =
		first > second ? (volatile unsigned char *)first
			       : (volatile unsigned char *)second;
	// The granule plus its red-zone pair equals the whole parent
	// block, so the symmetric free poisons every byte of it. The last
	// byte of the upper half is the farthest from the granule's start:
	// any sizing that leaves a rounded-up gap unpoisoned lets this
	// read survive.
	unsigned char probe = upper[half_req - 1];
	(void)probe;
	_exit(0);
}

static int
test_release_poisons_whole_block(void) {
	pid_t pid = fork();
	TEST_ASSERT(pid >= 0, "fork failed");
	if (pid == 0) {
		phase_release_poisons_upper_half();
		_exit(0);
	}
	int status = 0;
	TEST_ASSERT(waitpid(pid, &status, 0) == pid, "waitpid failed");
	TEST_ASSERT(
		!(WIFEXITED(status) &&
		  (WEXITSTATUS(status) == 0 || WEXITSTATUS(status) == 3)),
		"reading a live block after wholesale release must be trapped "
		"(clean exit or setup failure, status %d)",
		status
	);
	return 0;
}
#else
static int
test_release_poisons_whole_block(void) {
	LOG(INFO, "poison check compiled out (non-asan build), skipping");
	return 0;
}
#endif

int
main(void) {
	log_enable_name("info");

	if (test_grow_on_exhaustion() != 0) {
		LOG(ERROR, "test_grow_on_exhaustion failed");
		return -1;
	}
	if (test_release_all_returns_arenas() != 0) {
		LOG(ERROR, "test_release_all_returns_arenas failed");
		return -1;
	}
	if (test_realloc_in_owner_context() != 0) {
		LOG(ERROR, "test_realloc_in_owner_context failed");
		return -1;
	}
	if (test_fini_owner_child_under_foreign_parent() != 0) {
		LOG(ERROR, "test_fini_owner_child_under_foreign_parent failed");
		return -1;
	}
	if (test_tripwire_wrong_free_aborts() != 0) {
		LOG(ERROR, "test_tripwire_wrong_free_aborts failed");
		return -1;
	}
	if (test_grow_failure_propagates() != 0) {
		LOG(ERROR, "test_grow_failure_propagates failed");
		return -1;
	}
	if (test_chained_owners() != 0) {
		LOG(ERROR, "test_chained_owners failed");
		return -1;
	}
	if (test_asan_max_range_grow() != 0) {
		LOG(ERROR, "test_asan_max_range_grow failed");
		return -1;
	}
	if (test_release_poisons_whole_block() != 0) {
		LOG(ERROR, "test_release_poisons_whole_block failed");
		return -1;
	}

	LOG(INFO, "memory_owner tests: OK");
	return 0;
}
