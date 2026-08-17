// Threaded stress tests for struct memory_owner: concurrent growth to
// parent exhaustion, the memory_context_fini parent-lock unlink raced
// against sibling splices, and the failed-grow rescan raced by two
// granule allocators. Every test asserts exact byte accounting and no
// hang; per-test detail lives with each test below.

#include "common/asan.h"
#include "common/memory.h"
#include "common/memory_owner.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <inttypes.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// The parent arena is aligned to its own 8 MiB size. block_allocator_
// put_arena caps a chunk by MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, not by the
// position's true alignment, so an 8 MiB arena on a merely 2 MiB-aligned
// base would be ingested as one misaligned 8 MiB block and corrupt every
// split below it.
#define PARENT_ARENA_ALIGN (1u << 23)
#define PARENT_ARENA_SIZE ((size_t)(8u << 20)) // 8 MiB
#define PARENT_RAW_SZ ((size_t)(16u << 20))

#define THREAD_COUNT 8
#define GROW_ITERATIONS 6000
#define SPLICE_ITERATIONS 4000
#define SLOTS_PER_THREAD 12

// A request whose internal size is exactly the minimum granule, so
// every allocation misses the free lists and races a fresh grow.
#define GRANULE_REQUEST (MEMORY_OWNER_MIN_GRANULE - 2 * ASAN_RED_ZONE)
#define GRANULE_RACE_THREADS 2
#define GRANULE_RACE_CAP (PARENT_ARENA_SIZE / MEMORY_OWNER_MIN_GRANULE)

// Mixed sizes so one 64 KiB granule serves many allocations and the
// borrow chain runs on nearly every alloc.
static const size_t kSizes[] = {8, 24, 96, 480, 1000, 2040, 3000};
#define NUM_SIZES (sizeof(kSizes) / sizeof(kSizes[0]))

static atomic_int g_failures = 0;

#define THREAD_CHECK(cond, msg, ...)                                           \
	do {                                                                   \
		if (!(cond)) {                                                 \
			LOG(ERROR, "THREAD ASSERT FAILED: " msg, ##__VA_ARGS__ \
			);                                                     \
			atomic_fetch_add(&g_failures, 1);                      \
		}                                                              \
	} while (0)

struct fixture {
	void *raw;
	struct block_allocator ba;
	struct memory_context ctx;
	struct memory_owner owner;
	struct memory_context octx;
};

static struct fixture *g_fx;

static int
fixture_init(struct fixture *fx) {
	fx->raw = malloc(PARENT_RAW_SZ + PARENT_ARENA_ALIGN);
	TEST_ASSERT(fx->raw != NULL, "failed to allocate raw buffer");
	uintptr_t aligned = ((uintptr_t)fx->raw + PARENT_ARENA_ALIGN - 1) &
			    ~(uintptr_t)(PARENT_ARENA_ALIGN - 1);

	TEST_ASSERT(
		block_allocator_init(&fx->ba) == 0,
		"parent allocator init failed"
	);
	block_allocator_put_arena(&fx->ba, (void *)aligned, PARENT_ARENA_SIZE);
	TEST_ASSERT(
		memory_context_init(&fx->ctx, "parent", &fx->ba) == 0,
		"parent context init failed"
	);
	TEST_ASSERT(
		memory_owner_init(&fx->owner, &fx->ctx, "owner") == 0,
		"memory_owner_init failed"
	);
	TEST_ASSERT(
		memory_context_init(
			&fx->octx, "owner-ctx", &fx->owner.allocator
		) == 0,
		"owner-bound context init failed"
	);
	return 0;
}

static void
fixture_fini(struct fixture *fx) {
	memory_context_fini(&fx->octx);
	memory_context_fini(&fx->ctx);
	free(fx->raw);
}

static int
cmp_ptr(const void *a, const void *b) {
	void *const *pa = (void *const *)a;
	void *const *pb = (void *const *)b;
	if (*pa < *pb) {
		return -1;
	}
	if (*pa > *pb) {
		return 1;
	}
	return 0;
}

// Splices an owner-bound context into the shared parent's child list the
// way the cp_module lifecycle will: list on the parent, allocations on
// the owner's separate allocator.
static void
owner_child_splice(struct memory_context *child, struct fixture *fx) {
	spinlock_lock(&fx->ba.lock);
	SET_OFFSET_OF(&child->parent, &fx->ctx);
	EQUATE_OFFSET(&child->next_sibling, &fx->ctx.first_child);
	SET_OFFSET_OF(&fx->ctx.first_child, child);
	spinlock_unlock(&fx->ba.lock);
}

// Alloc/free churn through the shared owner-bound context. A NULL
// allocation means the parent cannot satisfy growth any more, so the
// thread drains its remaining slots and stops.
static void *
grow_worker(void *arg) {
	unsigned seed = *(unsigned *)arg;

	void *slots[SLOTS_PER_THREAD];
	size_t slot_sizes[SLOTS_PER_THREAD];
	memset(slots, 0, sizeof(slots));
	memset(slot_sizes, 0, sizeof(slot_sizes));

	int exhausted = 0;
	for (int iter = 0; iter < GROW_ITERATIONS && !exhausted; ++iter) {
		int idx = (int)(rand_r(&seed) % SLOTS_PER_THREAD);
		if (slots[idx] != NULL) {
			memory_bfree(&g_fx->octx, slots[idx], slot_sizes[idx]);
			slots[idx] = NULL;
			continue;
		}

		size_t size = kSizes[rand_r(&seed) % NUM_SIZES];
		void *ptr = memory_balloc(&g_fx->octx, size);
		if (ptr == NULL) {
			exhausted = 1;
			continue;
		}
		THREAD_CHECK(
			((uintptr_t)ptr & 7) == 0,
			"unaligned block %p from the owner",
			ptr
		);
		memset(ptr, (int)(seed & 0xff), size);
		slots[idx] = ptr;
		slot_sizes[idx] = size;
	}

	for (int idx = 0; idx < SLOTS_PER_THREAD; ++idx) {
		if (slots[idx] != NULL) {
			memory_bfree(&g_fx->octx, slots[idx], slot_sizes[idx]);
			slots[idx] = NULL;
		}
	}
	return NULL;
}

// Walks every pool free list and reports a node whose address is not
// aligned to its pool's block size — the fingerprint of a mismatched or
// duplicated free having entered the in-band lists.
static int
check_pool_alignment(struct block_allocator *ba, const char *tag) {
	for (size_t p = 0; p < MEMORY_BLOCK_ALLOCATOR_EXP; ++p) {
		size_t blk = (size_t)1 << (MEMORY_BLOCK_ALLOCATOR_MIN_BITS + p);
		void *node = ADDR_OF(&ba->pools[p].free_list);
		size_t n = 0;
		while (node != NULL) {
			if ((uintptr_t)node & (blk - 1)) {
				LOG(ERROR,
				    "%s pool[%zu] node %p misaligned for block "
				    "size %zu (node %zu)",
				    tag,
				    p,
				    node,
				    blk,
				    n);
				return -1;
			}
			// The in-band link is poisoned between uses, exactly
			// as the allocator's own readers see it.
			asan_unpoison_memory_region(node, sizeof(void *));
			void *next = ADDR_OF((void **)node);
			asan_poison_memory_region(node, sizeof(void *));
			node = next;
			++n;
		}
		if (n != (size_t)ba->pools[p].free) {
			LOG(ERROR,
			    "%s pool[%zu] walked %zu nodes, counter says %llu",
			    tag,
			    p,
			    n,
			    (unsigned long long)ba->pools[p].free);
			return -1;
		}
	}
	return 0;
}

// Verifies that concurrent growth through one owner never corrupts the
// owner's pools: after the churn, an exhaustive alloc-until-NULL sweep
// recovers every borrowed byte exactly once, release_all returns the
// parent to its exact baseline, and the arena bookkeeping stays within
// the parent's capacity.
static int
test_concurrent_growth(void) {
	struct fixture fx;
	TEST_ASSERT(fixture_init(&fx) == 0, "fixture init failed");
	g_fx = &fx;
	atomic_store(&g_failures, 0);

	size_t parent_base = block_allocator_free_size(&fx.ba);

	pthread_t threads[THREAD_COUNT];
	unsigned seeds[THREAD_COUNT];
	for (int idx = 0; idx < THREAD_COUNT; ++idx) {
		seeds[idx] = (unsigned)(0xc0ffeeu + (unsigned)idx * 7919u);
		TEST_ASSERT(
			pthread_create(
				&threads[idx], NULL, grow_worker, &seeds[idx]
			) == 0,
			"pthread_create failed for thread %d",
			idx
		);
	}
	for (int idx = 0; idx < THREAD_COUNT; ++idx) {
		pthread_join(threads[idx], NULL);
	}

	TEST_ASSERT(
		atomic_load(&g_failures) == 0,
		"one or more grow workers observed a broken invariant"
	);

	TEST_ASSERT(
		check_pool_alignment(&fx.ba, "parent") == 0,
		"parent free lists corrupted during churn"
	);
	TEST_ASSERT(
		check_pool_alignment(&fx.owner.allocator, "owner") == 0,
		"owner free lists corrupted during churn"
	);

	TEST_ASSERT(fx.owner.arena_count > 0, "churn never grew the owner");
	size_t arena_bytes = 0;
	// What the owner's pools actually hold: each granule is ingested as
	// its pool-rounded true block, which red zones inflate.
	size_t ingested_bytes = 0;
	struct memory_arena *arenas = ADDR_OF(&fx.owner.arenas);
	for (uint64_t idx = 0; idx < fx.owner.arena_count; ++idx) {
		arena_bytes += (size_t)arenas[idx].size;
		ingested_bytes += block_allocator_pool_size(
			&fx.owner.allocator,
			block_allocator_pool_index(
				&fx.owner.allocator,
				(size_t)arenas[idx].size + 2 * ASAN_RED_ZONE
			)
		);
	}
	// Every granule is disjoint and at least 64 KiB, so the parent's
	// 8 MiB bounds the arena count even counting grow races.
	TEST_ASSERT(
		fx.owner.arena_count <=
			PARENT_ARENA_SIZE / MEMORY_OWNER_MIN_GRANULE,
		"arena_count %" PRIu64 " exceeds parent capacity",
		fx.owner.arena_count
	);

	size_t owner_free = block_allocator_free_size(&fx.owner.allocator);
	TEST_ASSERT(
		owner_free == ingested_bytes,
		"owner free size %zu must equal ingested arena bytes %zu",
		owner_free,
		ingested_bytes
	);

	// Finish exhausting the parent so the sweep below terminates on a
	// grow failure rather than borrowing fresh granules mid-sweep.
	void *drain[256];
	size_t drain_count = 0;
	for (;;) {
		void *p = memory_balloc(&fx.ctx, MEMORY_OWNER_MIN_GRANULE);
		if (p == NULL) {
			break;
		}
		TEST_ASSERT(
			drain_count < 256,
			"parent drain exceeded expected block count"
		);
		drain[drain_count++] = p;
	}

	// Exhaustive sweep over the real free lists with growth disabled by
	// the drained parent: duplicate pointers or a wrong leftover sum is
	// how a corrupted in-band list shows up.
	size_t sweep_cap = arena_bytes / MEMORY_BLOCK_ALLOCATOR_MIN_SIZE + 16;
	void **swept = malloc(sweep_cap * sizeof(void *));
	TEST_ASSERT(swept != NULL, "failed to allocate sweep bookkeeping");

	size_t swept_count = 0;
	for (;;) {
		void *p = memory_balloc(
			&fx.octx, MEMORY_BLOCK_ALLOCATOR_MIN_SIZE
		);
		if (p == NULL) {
			break;
		}
		TEST_ASSERT(
			swept_count < sweep_cap,
			"sweep exceeded cap %zu; free list likely corrupted",
			sweep_cap
		);
		swept[swept_count++] = p;
	}
#if !defined(HAVE_ASAN)
	// Exact only without red zones: they inflate every block's internal
	// size, shifting the split arithmetic. Uniqueness and the empty
	// leftover below still catch corruption under ASAN.
	TEST_ASSERT(
		swept_count == arena_bytes / MEMORY_BLOCK_ALLOCATOR_MIN_SIZE,
		"sweep recovered %zu blocks, expected exactly arena bytes / 8 "
		"(%zu)",
		swept_count,
		arena_bytes / MEMORY_BLOCK_ALLOCATOR_MIN_SIZE
	);
#endif
#if !defined(HAVE_ASAN)
	TEST_ASSERT(
		block_allocator_free_size(&fx.owner.allocator) == 0,
		"owner still reports free bytes after exhaustive sweep"
	);
#else
	// Red zones inflate the sweep request's internal size, so fragments
	// below it can survive; only progress, not emptiness, is assertable.
	TEST_ASSERT(
		block_allocator_free_size(&fx.owner.allocator) < arena_bytes,
		"owner free size did not shrink below the borrowed total"
	);
#endif

	qsort(swept, swept_count, sizeof(void *), cmp_ptr);
	for (size_t idx = 1; idx < swept_count; ++idx) {
		TEST_ASSERT(
			swept[idx] != swept[idx - 1],
			"sweep returned the same block twice: %p",
			swept[idx]
		);
	}

	// Round-trip every swept block back through the owner before the
	// wholesale release.
	for (size_t idx = 0; idx < swept_count; ++idx) {
		memory_bfree(
			&fx.octx, swept[idx], MEMORY_BLOCK_ALLOCATOR_MIN_SIZE
		);
	}
	free(swept);
	TEST_ASSERT(
		block_allocator_free_size(&fx.owner.allocator) ==
			ingested_bytes,
		"owner free size must round-trip to the ingested total"
	);

	memory_owner_release_all(&fx.owner);
	for (size_t idx = 0; idx < drain_count; ++idx) {
		memory_bfree(&fx.ctx, drain[idx], MEMORY_OWNER_MIN_GRANULE);
	}
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"release_all must return the parent to its exact baseline"
	);

	fixture_fini(&fx);
	return 0;
}

// One cycle of an owner-bound child: splice under the parent's lock,
// allocate through the owner's allocator, fini through the parent-lock
// unlink path.
static void
owner_child_cycle(struct fixture *fx, unsigned *seed) {
	struct memory_context child;
	if (memory_context_init(&child, "ob-child", &fx->owner.allocator) !=
	    0) {
		THREAD_CHECK(0, "owner child init failed");
		return;
	}
	owner_child_splice(&child, fx);

	size_t size = kSizes[rand_r(seed) % NUM_SIZES];
	void *p = memory_balloc(&child, size);
	if (p != NULL) {
		memory_bfree(&child, p, size);
	}

	memory_context_fini(&child);
}

// One cycle of a plain child sharing the parent's allocator.
static void
plain_child_cycle(struct fixture *fx, unsigned *seed) {
	struct memory_context child;
	if (memory_context_init_from(&child, &fx->ctx, "plain-child") != 0) {
		THREAD_CHECK(0, "plain child init failed");
		return;
	}

	size_t size = kSizes[rand_r(seed) % NUM_SIZES];
	void *p = memory_balloc(&child, size);
	if (p != NULL) {
		memory_bfree(&child, p, size);
	}

	memory_context_fini(&child);
}

// Owner-bound children, plain children, and direct parent churn all race
// on the parent's child list and its allocator.
static void *
splice_worker(void *arg) {
	struct fixture *fx = g_fx;
	unsigned seed = *(unsigned *)arg;

	for (int iter = 0; iter < SPLICE_ITERATIONS; ++iter) {
		switch (iter % 3) {
		case 0:
			owner_child_cycle(fx, &seed);
			break;
		case 1:
			plain_child_cycle(fx, &seed);
			break;
		case 2: {
			size_t size = kSizes[rand_r(&seed) % NUM_SIZES];
			void *p = memory_balloc(&fx->ctx, size);
			if (p != NULL) {
				memory_bfree(&fx->ctx, p, size);
			}
			break;
		}
		default:
			break;
		}
	}
	return NULL;
}

// Verifies the memory_context_fini parent-lock fix under load: finishing
// an owner-bound child (own allocator) must serialize against siblings
// splicing into the same parent list.
static int
test_concurrent_fini_vs_splice(void) {
	struct fixture fx;
	TEST_ASSERT(fixture_init(&fx) == 0, "fixture init failed");
	g_fx = &fx;
	atomic_store(&g_failures, 0);

	pthread_t threads[THREAD_COUNT];
	unsigned seeds[THREAD_COUNT];
	for (int idx = 0; idx < THREAD_COUNT; ++idx) {
		seeds[idx] = (unsigned)(0xbeefu + (unsigned)idx * 104729u);
		TEST_ASSERT(
			pthread_create(
				&threads[idx], NULL, splice_worker, &seeds[idx]
			) == 0,
			"pthread_create failed for thread %d",
			idx
		);
	}
	for (int idx = 0; idx < THREAD_COUNT; ++idx) {
		pthread_join(threads[idx], NULL);
	}

	TEST_ASSERT(
		atomic_load(&g_failures) == 0,
		"one or more splice workers observed a broken invariant"
	);
	TEST_ASSERT(
		ADDR_OF(&fx.ctx.first_child) == NULL,
		"parent must have no children left after churn"
	);

	// The owner must still be fully serviceable after the storm.
	void *p = memory_balloc(&fx.octx, 128);
	TEST_ASSERT(p != NULL, "owner alloc failed after churn");
	memory_bfree(&fx.octx, p, 128);

	fixture_fini(&fx);
	return 0;
}

struct granule_race_arg {
	unsigned seed;
	void *blocks[GRANULE_RACE_CAP];
	size_t count;
};

// Holds whole-granule blocks until the parent can no longer fund a
// granule. A NULL means the grow failed and the post-failure rescan
// found nothing; a block means the rescan or the first scan won.
static void *
granule_race_worker(void *arg) {
	struct granule_race_arg *ctx = arg;

	size_t count = 0;
	while (count < GRANULE_RACE_CAP) {
		void *p = memory_balloc(&g_fx->octx, GRANULE_REQUEST);
		if (p == NULL) {
			break;
		}
		memset(p, (int)(ctx->seed & 0xff), GRANULE_REQUEST);
		ctx->blocks[count++] = p;
	}

	ctx->count = count;
	return NULL;
}

// Verifies the failed-grow rescan under a real race: two threads race
// whole-granule allocations until the parent cannot fund another
// granule, so one grow of a concurrent pair fails into the rescan.
// Every assertion holds in every interleaving — no hang, exact owner
// accounting for both possible loser outcomes, and a clean return to
// baseline.
static int
test_false_exhaustion_race(void) {
	struct fixture fx;
	TEST_ASSERT(fixture_init(&fx) == 0, "fixture init failed");
	g_fx = &fx;
	atomic_store(&g_failures, 0);

	size_t parent_base = block_allocator_free_size(&fx.ba);

	pthread_t threads[GRANULE_RACE_THREADS];
	struct granule_race_arg args[GRANULE_RACE_THREADS];
	for (int idx = 0; idx < GRANULE_RACE_THREADS; ++idx) {
		args[idx].seed = (unsigned)(0xd1ceu + (unsigned)idx * 6151u);
		args[idx].count = 0;
		TEST_ASSERT(
			pthread_create(
				&threads[idx],
				NULL,
				granule_race_worker,
				&args[idx]
			) == 0,
			"pthread_create failed for thread %d",
			idx
		);
	}
	for (int idx = 0; idx < GRANULE_RACE_THREADS; ++idx) {
		TEST_ASSERT(
			pthread_join(threads[idx], NULL) == 0,
			"pthread_join failed for thread %d",
			idx
		);
	}
	size_t total = args[0].count + args[1].count;

	TEST_ASSERT(total >= 1, "the parent must fund at least one granule");
	TEST_ASSERT(
		fx.owner.arena_count >= 1, "the race must have grown the owner"
	);
	TEST_ASSERT(
		fx.owner.arena_count <=
			PARENT_ARENA_SIZE / MEMORY_OWNER_MIN_GRANULE,
		"arena_count %" PRIu64 " exceeds parent capacity",
		fx.owner.arena_count
	);

	// Exact accounting: each granule is ingested as its pool-rounded
	// true block, each held request takes one request-pool block out,
	// and nothing else consumes the owner's pools.
	size_t req_block = block_allocator_pool_size(
		&fx.owner.allocator,
		block_allocator_pool_index(
			&fx.owner.allocator, GRANULE_REQUEST + 2 * ASAN_RED_ZONE
		)
	);
	size_t ingested = 0;
	struct memory_arena *arenas = ADDR_OF(&fx.owner.arenas);
	for (uint64_t idx = 0; idx < fx.owner.arena_count; ++idx) {
		ingested += block_allocator_pool_size(
			&fx.owner.allocator,
			block_allocator_pool_index(
				&fx.owner.allocator,
				(size_t)arenas[idx].size + 2 * ASAN_RED_ZONE
			)
		);
	}
	TEST_ASSERT(
		block_allocator_free_size(&fx.owner.allocator) +
				total * req_block ==
			ingested,
		"owner free size %zu + %zu held blocks must equal the ingested "
		"total %zu",
		block_allocator_free_size(&fx.owner.allocator),
		total * req_block,
		ingested
	);

	// The workers hold their blocks, so an exhaustive sweep recovers
	// exactly the free remainder — every granule splits into whole
	// request-sized blocks, nothing smaller can be hiding.
	size_t expect_swept = ingested / req_block - total;
	void *swept[GRANULE_RACE_CAP];
	size_t swept_count = 0;
	for (;;) {
		void *p = memory_balloc(&fx.octx, GRANULE_REQUEST);
		if (p == NULL) {
			break;
		}
		TEST_ASSERT(
			swept_count < GRANULE_RACE_CAP,
			"sweep exceeded the parent's block capacity"
		);
		swept[swept_count++] = p;
	}
	TEST_ASSERT(
		swept_count == expect_swept,
		"sweep recovered %zu blocks, expected the free remainder %zu",
		swept_count,
		expect_swept
	);
	qsort(swept, swept_count, sizeof(void *), cmp_ptr);
	for (size_t idx = 1; idx < swept_count; ++idx) {
		TEST_ASSERT(
			swept[idx] != swept[idx - 1],
			"sweep returned the same block twice: %p",
			swept[idx]
		);
	}

	// Round-trip every block, swept and held, back through the owner
	// before the wholesale release.
	for (size_t idx = 0; idx < swept_count; ++idx) {
		memory_bfree(&fx.octx, swept[idx], GRANULE_REQUEST);
	}
	for (int t = 0; t < GRANULE_RACE_THREADS; ++t) {
		for (size_t idx = 0; idx < args[t].count; ++idx) {
			memory_bfree(
				&fx.octx, args[t].blocks[idx], GRANULE_REQUEST
			);
		}
	}
	TEST_ASSERT(
		block_allocator_free_size(&fx.owner.allocator) == ingested,
		"owner free size must round-trip to the ingested total"
	);

	memory_owner_release_all(&fx.owner);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"release_all must return the parent to its exact baseline"
	);

	fixture_fini(&fx);
	return 0;
}

// A request that fits many times into one minimum granule, so a single
// successful growth must satisfy a long run of allocations.
#define SHARED_REQ ((size_t)8 * 1024 - 2 * ASAN_RED_ZONE)
#define SHARED_THREADS 2

struct shared_exhaustion_arg {
	size_t count;
	void *blocks[256];
};

// Allocates until NULL, then proves the NULL was truthful: after a
// correct exhaustion no free block can reappear, because the parent
// funds no further growth and this phase never frees.
static void *
shared_exhaustion_worker(void *arg) {
	struct shared_exhaustion_arg *ctx = arg;

	while (ctx->count < 256) {
		void *p = memory_balloc(&g_fx->octx, SHARED_REQ);
		if (p == NULL) {
			break;
		}
		memset(p, 0x3c, SHARED_REQ);
		ctx->blocks[ctx->count++] = p;
	}

	THREAD_CHECK(
		block_allocator_free_size(&g_fx->owner.allocator) == 0,
		"exhaustion reported while the owner still has free blocks"
	);
	return NULL;
}

// Two threads drain a parent that funds exactly one granule. Growth
// serializes on the owner lock for its whole body, so whichever thread
// loses the granule blocks on that lock for its final rescan and finds
// the winner's installed arena: the combined take must equal the
// granule's full capacity, and neither thread may see a NULL while
// free blocks remain.
static int
test_single_granule_shared_exhaustion(void) {
	size_t granule_block = block_allocator_pool_size(
		NULL,
		block_allocator_pool_index(
			NULL, MEMORY_OWNER_MIN_GRANULE + 2 * ASAN_RED_ZONE
		)
	);
	size_t expected = granule_block / (SHARED_REQ + 2 * ASAN_RED_ZONE);
	size_t arena_sz = granule_block + ((size_t)4 << 10);

	// The arena must start at the allocator's maximum block alignment:
	// put_arena splits from the start alignment, and anything below it
	// fragments the granule into alignment-sized blocks no grow can use.
	size_t arena_align = MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN;

	void *raw = malloc(arena_sz + arena_align);
	TEST_ASSERT(raw != NULL, "failed to allocate raw buffer");
	uintptr_t aligned = ((uintptr_t)raw + arena_align - 1) &
			    ~(uintptr_t)(arena_align - 1);

	struct fixture fx;
	fx.raw = raw;
	TEST_ASSERT(block_allocator_init(&fx.ba) == 0, "parent init failed");
	block_allocator_put_arena(&fx.ba, (void *)aligned, arena_sz);
	TEST_ASSERT(
		memory_context_init(&fx.ctx, "one-granule-parent", &fx.ba) == 0,
		"parent context init failed"
	);
	size_t parent_base = block_allocator_free_size(&fx.ba);

	TEST_ASSERT(
		memory_owner_init(&fx.owner, &fx.ctx, "one-granule") == 0,
		"owner init failed"
	);
	TEST_ASSERT(
		memory_context_init(
			&fx.octx, "one-granule-ctx", &fx.owner.allocator
		) == 0,
		"owner context init failed"
	);
	g_fx = &fx;
	atomic_store(&g_failures, 0);

	pthread_t threads[SHARED_THREADS];
	struct shared_exhaustion_arg args[SHARED_THREADS];
	for (int idx = 0; idx < SHARED_THREADS; ++idx) {
		args[idx].count = 0;
		TEST_ASSERT(
			pthread_create(
				&threads[idx],
				NULL,
				shared_exhaustion_worker,
				&args[idx]
			) == 0,
			"pthread_create failed for thread %d",
			idx
		);
	}
	for (int idx = 0; idx < SHARED_THREADS; ++idx) {
		TEST_ASSERT(
			pthread_join(threads[idx], NULL) == 0,
			"pthread_join failed for thread %d",
			idx
		);
	}
	TEST_ASSERT(atomic_load(&g_failures) == 0, "a worker assertion fired");

	size_t total = args[0].count + args[1].count;
	TEST_ASSERT(
		total == expected,
		"two threads recovered %zu of %zu blocks in the single "
		"funded granule",
		total,
		expected
	);
	TEST_ASSERT(
		fx.owner.arena_count == 1,
		"the parent must fund exactly one granule"
	);

	for (int t = 0; t < SHARED_THREADS; ++t) {
		for (size_t idx = 0; idx < args[t].count; ++idx) {
			memory_bfree(&fx.octx, args[t].blocks[idx], SHARED_REQ);
		}
	}
	memory_owner_release_all(&fx.owner);
	TEST_ASSERT(
		block_allocator_free_size(&fx.ba) == parent_base,
		"release_all must return the parent to its exact baseline"
	);

	memory_context_fini(&fx.octx);
	memory_context_fini(&fx.ctx);
	free(raw);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_concurrent_growth() != 0) {
		LOG(ERROR, "test_concurrent_growth failed");
		return -1;
	}
	if (test_concurrent_fini_vs_splice() != 0) {
		LOG(ERROR, "test_concurrent_fini_vs_splice failed");
		return -1;
	}
	if (test_false_exhaustion_race() != 0) {
		LOG(ERROR, "test_false_exhaustion_race failed");
		return -1;
	}
	if (test_single_granule_shared_exhaustion() != 0) {
		LOG(ERROR, "test_single_granule_shared_exhaustion failed");
		return -1;
	}

	LOG(INFO, "memory_owner thread stress tests: OK");
	return 0;
}
