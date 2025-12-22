#include "common/memory.h"
#include "common/memory_block.h"
#include "common/numutils.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <assert.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MAX_GUAR_ALIGN MEMORY_BLOCK_MAX_ALIGN
#define BIG_ALIGN (1u << 21)		  // 2 MiB
#define RAW_ALLOC_SZ ((size_t)(1u << 22)) // 4 MiB

static inline uintptr_t
align_up_uint(uintptr_t p, size_t align) {
	return next_divisible_pow2(p, align);
}

// Dump allocator pools and mask for diagnostics
static void
dump_allocator_state(const char *tag, struct block_allocator *ba) {
	size_t free_total = block_allocator_free_size(ba);
	LOG(INFO,
	    "%s: mask=0x%08x free_total=%zu",
	    tag,
	    ba->not_empty_mask,
	    free_total);
	for (size_t i = 0; i < MEMORY_BLOCK_ALLOCATOR_EXP; ++i) {
		const struct block_allocator_pool *p = &ba->pools[i];
		if (p->free || p->allocate || p->borrow) {
			LOG(INFO,
			    "  pool[%zu]: size=%zu alloc=%" PRIu64
			    " free=%" PRIu64 " borrow=%" PRIu64,
			    i,
			    block_allocator_pool_size(ba, i),
			    p->allocate,
			    p->free,
			    p->borrow);
		}
	}
}

static inline int
ptr_has_alignment(void *ptr, size_t align) {
	return ((uintptr_t)ptr % align) == 0;
}
static inline size_t
compute_target_pool(struct block_allocator *ba, size_t req) {
	size_t internal = req + 2 * ASAN_RED_ZONE;
	return block_allocator_pool_index(ba, internal);
}
/* duplicate compute_target_pool removed */

static int
test_init_and_empty_alloc(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	TEST_ASSERT(
		ba.not_empty_mask == 0, "not_empty_mask should be 0 after init"
	);
	for (size_t i = 0; i < MEMORY_BLOCK_ALLOCATOR_EXP; ++i) {
		TEST_ASSERT(
			ba.pools[i].free_list == NULL,
			"pool[%zu].free_list != NULL",
			i
		);
		TEST_ASSERT(
			ba.pools[i].allocate == 0, "pool[%zu].allocate != 0", i
		);
		TEST_ASSERT(ba.pools[i].free == 0, "pool[%zu].free != 0", i);
		TEST_ASSERT(
			ba.pools[i].borrow == 0, "pool[%zu].borrow != 0", i
		);
	}

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "balloc.init", &ba) == 0,
		"mctx init failed"
	);

	void *p = memory_balloc(&mctx, 16);
	TEST_ASSERT(
		p == NULL, "allocation on empty allocator must return NULL"
	);
	TEST_ASSERT(
		mctx.balloc_count == 0,
		"balloc_count should not increment on failed alloc"
	);
	TEST_ASSERT(
		mctx.balloc_size == 0,
		"balloc_size should not increment on failed alloc"
	);
	return 0;
}

// Prepare a 2MiB-aligned arena of exactly 2MiB length inside a raw 4MiB buffer
static int
make_2mb_aligned_arena(
	void **raw_out, void **arena_out, size_t *arena_size_out
) {
	void *raw =
		malloc(RAW_ALLOC_SZ + BIG_ALIGN); // ensure we can align inside
	TEST_ASSERT(raw != NULL, "failed to allocate raw buffer");
	uintptr_t p = (uintptr_t)raw;
	uintptr_t aligned = align_up_uint(p, BIG_ALIGN);
	// Ensure we still have 2MiB available
	if (aligned + BIG_ALIGN > (uintptr_t)raw + RAW_ALLOC_SZ + BIG_ALIGN) {
		free(raw);
		TEST_ASSERT(0, "alignment math failed");
	}
	*raw_out = raw;
	*arena_out = (void *)aligned;
	*arena_size_out = BIG_ALIGN;
	return 0;
}

static int
test_put_arena_single_block_and_exact_alloc(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	void *raw = NULL;
	void *arena = NULL;
	size_t arena_sz = 0;
	TEST_ASSERT(
		make_2mb_aligned_arena(&raw, &arena, &arena_sz) == 0,
		"aligned arena prep failed"
	);

	// Ingest arena
	block_allocator_put_arena(&ba, arena, arena_sz);
	dump_allocator_state("after put_arena(2MiB)", &ba);

	// Expect exactly one free block at pool index 18 (2MiB == 1 << (3 +
	// 18))
	size_t pi_2mb = 18;
	TEST_ASSERT(
		block_allocator_pool_size(&ba, pi_2mb) == BIG_ALIGN,
		"pool size mismatch for 2MiB"
	);
	TEST_ASSERT(ba.pools[pi_2mb].free == 1, "pool[18].free must be 1");
	TEST_ASSERT(
		ba.not_empty_mask == (1u << pi_2mb),
		"not_empty_mask must be 1<<18, got 0x%08x",
		ba.not_empty_mask
	);
	TEST_ASSERT(
		block_allocator_free_size(&ba) == BIG_ALIGN,
		"free_size must be 2MiB"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "balloc.exact", &ba) == 0,
		"mctx init failed"
	);

	// Exact allocation hits the 2MiB block without borrowing.
	size_t req = BIG_ALIGN - 2 * ASAN_RED_ZONE;
	void *ptr = memory_balloc(&mctx, req);
	dump_allocator_state("after exact alloc", &ba);
	TEST_ASSERT(ptr != NULL, "exact 2MiB allocation returned NULL");

	// Alignment rule: pointer must be aligned to min(block_size,
	// MAX_GUAR_ALIGN).
	size_t k_b = BIG_ALIGN;
	size_t guar = (k_b < MAX_GUAR_ALIGN) ? k_b : MAX_GUAR_ALIGN;
	TEST_ASSERT(
		ptr_has_alignment(ptr, guar),
		"returned ptr not aligned to guaranteed boundary: guar=%zu "
		"ptr=%p",
		guar,
		ptr
	);

	// After taking the only 2MiB free block, its pool should become empty.
	// Mask must clear the corresponding bit.
	TEST_ASSERT(
		ba.pools[pi_2mb].free == 0,
		"pool[18].free must become 0 after get"
	);
	TEST_ASSERT(
		(ba.not_empty_mask & (1u << pi_2mb)) == 0,
		"not_empty_mask bit 18 should be cleared after last block is "
		"taken, mask=0x%08x",
		ba.not_empty_mask
	);
	TEST_ASSERT(
		block_allocator_free_size(&ba) == 0,
		"free_size must be 0 after exact alloc"
	);

	// Free back and re-TEST_ASSERT totals and mask
	memory_bfree(&mctx, ptr, req);
	dump_allocator_state("after exact free", &ba);
	TEST_ASSERT(
		ba.pools[pi_2mb].free == 1,
		"pool[18].free must be restored to 1 after free"
	);
	TEST_ASSERT(
		ba.not_empty_mask == (1u << pi_2mb),
		"not_empty_mask must restore bit 18 only, got 0x%08x",
		ba.not_empty_mask
	);
	TEST_ASSERT(
		block_allocator_free_size(&ba) == BIG_ALIGN,
		"free_size must restore to 2MiB"
	);

	// No leaks at context level so far
	TEST_ASSERT(mctx.balloc_count == 1, "balloc_count mismatch");
	TEST_ASSERT(mctx.bfree_count == 1, "bfree_count mismatch");
	TEST_ASSERT(mctx.balloc_size == req, "balloc_size mismatch");
	TEST_ASSERT(mctx.bfree_size == req, "bfree_size mismatch");

	free(raw);
	return 0;
}

static int
test_small_alloc_borrow_chain_and_mask_logic(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	void *raw = NULL;
	void *arena = NULL;
	size_t arena_sz = 0;
	TEST_ASSERT(
		make_2mb_aligned_arena(&raw, &arena, &arena_sz) == 0,
		"aligned arena prep failed"
	);

	// Ingest a single 2MiB block
	block_allocator_put_arena(&ba, arena, arena_sz);
	dump_allocator_state("before small alloc", &ba);
	TEST_ASSERT(
		ba.pools[18].free == 1, "pool[18].free must be 1 initially"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "balloc.small", &ba) == 0,
		"mctx init failed"
	);

	// Request size=1; compute actual target pool considering ASAN red zones
	const size_t req = 1;
	const size_t target_pi = compute_target_pool(&ba, req);
	const size_t target_block = block_allocator_pool_size(&ba, target_pi);
	void *ptr = memory_balloc(&mctx, req);
	dump_allocator_state("after small alloc", &ba);
	TEST_ASSERT(ptr != NULL, "small allocation returned NULL");

	// Guaranteed alignment: min(block_size, 64) == 8 for pool 0
	size_t guar =
		(target_block < MAX_GUAR_ALIGN) ? target_block : MAX_GUAR_ALIGN;
	TEST_ASSERT(
		ptr_has_alignment(ptr, guar),
		"small alloc: ptr not aligned to guar=%zu, ptr=%p",
		guar,
		ptr
	);

	// Free size must reduce by exactly target_block (8) bytes (splits do
	// not change total free).
	size_t free_total_after_alloc = block_allocator_free_size(&ba);
	TEST_ASSERT(
		free_total_after_alloc == BIG_ALIGN - target_block,
		"free_total must be 2MiB - %zu after small alloc, got %zu",
		target_block,
		free_total_after_alloc
	);

	// Mask invariants around borrow:
	// - The original parent pool (18) should become empty after borrowing
	// at least once.
	// - Lower pools should contain free blocks; in particular, pool 0
	// should remain non-empty
	//   after taking one block (since borrow puts two blocks, then get
	//   consumes one).
	TEST_ASSERT(
		ba.pools[18].free == 0,
		"pool[18] must be empty after borrow chain"
	);
	TEST_ASSERT(
		(ba.not_empty_mask & (1u << 18)) == 0,
		"bit 18 must be cleared after parent became empty; mask=0x%08x",
		ba.not_empty_mask
	);
	TEST_ASSERT(
		ba.pools[target_pi].free > 0,
		"pool[%zu] should have remaining free blocks after one get; "
		"free=%" PRIu64,
		target_pi,
		ba.pools[target_pi].free
	);

	// Free back and TEST_ASSERT totals and mask are consistent
	memory_bfree(&mctx, ptr, req);
	dump_allocator_state("after small free", &ba);
	TEST_ASSERT(
		block_allocator_free_size(&ba) == BIG_ALIGN,
		"free_total must restore to 2MiB after free, got %zu",
		block_allocator_free_size(&ba)
	);

	TEST_ASSERT(
		mctx.balloc_count == 1 && mctx.bfree_count == 1,
		"ctx counters mismatch (1/1)"
	);
	TEST_ASSERT(
		mctx.balloc_size == req && mctx.bfree_size == req,
		"ctx sizes mismatch"
	);

	free(raw);
	return 0;
}

static int
test_alignment_matrix(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	// Attach a big arena (we only need many blocks, 2MiB is plenty)
	void *raw = NULL;
	void *arena = NULL;
	size_t arena_sz = 0;
	TEST_ASSERT(
		make_2mb_aligned_arena(&raw, &arena, &arena_sz) == 0,
		"aligned arena prep failed"
	);
	block_allocator_put_arena(&ba, arena, arena_sz);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "balloc.align", &ba) == 0,
		"mctx init failed"
	);

	// Try a set of target pools: 0..10 (block sizes 8..8 << 10 = 8192)
	const size_t max_pool = 10;
	void *ptrs[max_pool + 1];
	size_t reqs[max_pool + 1];
	memset(ptrs, 0, sizeof(ptrs));

	for (size_t i = 0; i <= max_pool; ++i) {
		size_t k_b = block_allocator_pool_size(&ba, i);
		size_t req = (k_b > 2 * ASAN_RED_ZONE)
				     ? (k_b - 2 * ASAN_RED_ZONE)
				     : 1;
		void *p = memory_balloc(&mctx, req);
		TEST_ASSERT(
			p != NULL,
			"align matrix: alloc failed for pool %zu (B=%zu, "
			"req=%zu)",
			i,
			k_b,
			req
		);

		size_t guar = (k_b < MAX_GUAR_ALIGN) ? k_b : MAX_GUAR_ALIGN;
		TEST_ASSERT(
			ptr_has_alignment(p, guar),
			"align matrix: ptr not aligned to guar=%zu for pool "
			"%zu (B=%zu) ptr=%p",
			guar,
			i,
			k_b,
			p
		);

		ptrs[i] = p;
		reqs[i] = req;
	}

	// Free all and ensure no leaks and totals restored
	for (size_t i = 0; i <= max_pool; ++i) {
		memory_bfree(&mctx, ptrs[i], reqs[i]);
	}

	TEST_ASSERT(
		block_allocator_free_size(&ba) == BIG_ALIGN,
		"alignment matrix: free_total must restore to 2MiB, got %zu",
		block_allocator_free_size(&ba)
	);

	free(raw);
	return 0;
}

static int
test_reduction_loop_small_region(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	// Craft a region whose start is highly aligned but length too small,
	// forcing the while (pos + block_size > end) reduction path.
	void *raw_big = malloc(RAW_ALLOC_SZ + BIG_ALIGN);
	TEST_ASSERT(raw_big != NULL, "failed to allocate raw_big");
	uintptr_t base = (uintptr_t)raw_big;
	uintptr_t highly_aligned =
		align_up_uint(base, 1u << 20); // 1MiB alignment
	size_t small_len = (1u << 20) - 8;     // just below alignment
	void *arena = (void *)highly_aligned;

	block_allocator_put_arena(&ba, arena, small_len);
	dump_allocator_state("after small_len put_arena", &ba);

	// We expect some memory to be ingested (free_size > 0) and mask
	// non-zero.
	size_t free_total = block_allocator_free_size(&ba);
	TEST_ASSERT(
		free_total > 0,
		"reduction loop: expected some free bytes, got 0"
	);
	TEST_ASSERT(
		ba.not_empty_mask != 0,
		"reduction loop: not_empty_mask must be non-zero"
	);

	// Allocate one smallest block and free back to ensure lists are sane.
	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "balloc.reduce", &ba) == 0,
		"mctx init failed"
	);
	void *p = memory_balloc(&mctx, 1);
	TEST_ASSERT(p != NULL, "reduction loop: small alloc failed");
	memory_bfree(&mctx, p, 1);

	free(raw_big);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_init_and_empty_alloc() != 0) {
		LOG(ERROR, "test_init_and_empty_alloc failed");
		return -1;
	}
	if (test_put_arena_single_block_and_exact_alloc() != 0) {
		LOG(ERROR,
		    "test_put_arena_single_block_and_exact_alloc failed");
		return -1;
	}
	if (test_small_alloc_borrow_chain_and_mask_logic() != 0) {
		LOG(ERROR,
		    "test_small_alloc_borrow_chain_and_mask_logic failed");
		return -1;
	}
	if (test_alignment_matrix() != 0) {
		LOG(ERROR, "test_alignment_matrix failed");
		return -1;
	}
	if (test_reduction_loop_small_region() != 0) {
		LOG(ERROR, "test_reduction_loop_small_region failed");
		return -1;
	}

	LOG(INFO, "balloc tests: OK");
	return 0;
}