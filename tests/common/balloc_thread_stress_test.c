// Threaded stress test for the block_allocator spinlock and the
// memory_context tree splice locking added alongside it.
//
// Several pthreads hammer one shared block_allocator with balloc/bfree churn
// spanning multiple pool sizes (forcing the borrow/split path), while also
// churning memory_context_init_from/memory_context_fini of child contexts
// against one shared parent context, and periodically calling
// block_allocator_free_size as a locked reader racing that churn. This is
// only safe concurrently because block_allocator_balloc/bfree/free_size and
// the context tree splices all take the allocator's own spinlock.
// block_allocator_put_arena is exercised only single-threaded, before the
// threads start.

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_ALIGN                                                            \
	(1u << 21) // 2 MiB, a MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN chunk.
#define RAW_ALLOC_SZ ((size_t)(1u << 22))

#define THREAD_COUNT 8
#define ITERATIONS_PER_THREAD 20000
#define SLOTS_PER_THREAD 16
#define CHILD_CHURN_PERIOD 23
#define FREE_SIZE_CHURN_PERIOD 17

// Request sizes spanning several pools, so a borrow/split chain is exercised
// on nearly every allocation.
static const size_t kSizes[] = {8, 24, 40, 96, 200, 480, 1000, 2040};
#define NUM_SIZES (sizeof(kSizes) / sizeof(kSizes[0]))

static struct memory_context g_root;
static atomic_int g_failures = 0;

// Records a worker-thread invariant violation without unwinding the thread:
// TEST_ASSERT returning TEST_FAILED does not fit a pthread start routine.
#define THREAD_CHECK(cond, msg, ...)                                           \
	do {                                                                   \
		if (!(cond)) {                                                 \
			LOG(ERROR, "THREAD ASSERT FAILED: " msg, ##__VA_ARGS__ \
			);                                                     \
			atomic_fetch_add(&g_failures, 1);                      \
		}                                                              \
	} while (0)

struct thread_args {
	unsigned seed;
	struct block_allocator *ba;
};

// Drives balloc/bfree churn against the shared root context through a small
// fixed set of slots, interleaved with memory_context_init_from/fini churn
// of transient child contexts on the same root.
static void *
stress_worker(void *arg) {
	struct thread_args *args = (struct thread_args *)arg;
	unsigned seed = args->seed;
	struct block_allocator *ba = args->ba;

	void *slots[SLOTS_PER_THREAD];
	size_t slot_sizes[SLOTS_PER_THREAD];
	memset(slots, 0, sizeof(slots));
	memset(slot_sizes, 0, sizeof(slot_sizes));

	for (int iter = 0; iter < ITERATIONS_PER_THREAD; ++iter) {
		int idx = (int)(rand_r(&seed) % SLOTS_PER_THREAD);
		if (slots[idx] != NULL) {
			memory_bfree(&g_root, slots[idx], slot_sizes[idx]);
			slots[idx] = NULL;
		} else {
			size_t size = kSizes[rand_r(&seed) % NUM_SIZES];
			void *ptr = memory_balloc(&g_root, size);
			if (ptr != NULL) {
				slots[idx] = ptr;
				slot_sizes[idx] = size;
			}
		}

		if (iter % CHILD_CHURN_PERIOD == 0) {
			struct memory_context child;
			THREAD_CHECK(
				memory_context_init_from(
					&child, &g_root, "stress-child"
				) == 0,
				"memory_context_init_from failed"
			);

			void *cp = memory_balloc(&child, 32);
			if (cp != NULL) {
				memory_bfree(&child, cp, 32);
			}

			THREAD_CHECK(
				child.balloc_count == child.bfree_count,
				"child balloc_count/bfree_count mismatch: "
				"%zu != %zu",
				child.balloc_count,
				child.bfree_count
			);
			THREAD_CHECK(
				child.balloc_size == child.bfree_size,
				"child balloc_size/bfree_size mismatch: %zu "
				"!= %zu",
				child.balloc_size,
				child.bfree_size
			);

			memory_context_fini(&child);
		}

		if (iter % FREE_SIZE_CHURN_PERIOD == 0) {
			// A locked reader racing balloc/bfree on the same
			// allocator. Only the lock itself is under test, since
			// the exact value is transient under concurrent churn.
			size_t free_size = block_allocator_free_size(ba);
			THREAD_CHECK(
				free_size <= ARENA_ALIGN,
				"free_size %zu exceeds arena size %u",
				free_size,
				ARENA_ALIGN
			);
		}
	}

	for (int idx = 0; idx < SLOTS_PER_THREAD; ++idx) {
		if (slots[idx] != NULL) {
			memory_bfree(&g_root, slots[idx], slot_sizes[idx]);
			slots[idx] = NULL;
		}
	}

	return NULL;
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

// Verifies that concurrent balloc/bfree churn and memory_context
// init_from/fini churn against one shared block_allocator and parent context
// never corrupt the free lists or the context tree: the allocator returns to
// its baseline free size, the parent ends up with no children, per-context
// counters balance, and an exhaustive alloc-until-NULL sweep recovers every
// byte exactly once.
static int
test_concurrent_balloc_and_context_churn(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	void *raw = malloc(RAW_ALLOC_SZ + ARENA_ALIGN);
	TEST_ASSERT(raw != NULL, "failed to allocate raw arena buffer");
	uintptr_t aligned = ((uintptr_t)raw + ARENA_ALIGN - 1) &
			    ~(uintptr_t)(ARENA_ALIGN - 1);
	void *arena = (void *)aligned;
	size_t arena_size = ARENA_ALIGN;

	block_allocator_put_arena(&ba, arena, arena_size);

	TEST_ASSERT(
		memory_context_init(&g_root, "stress-root", &ba) == 0,
		"root context init failed"
	);

	size_t baseline_free = block_allocator_free_size(&ba);
	TEST_ASSERT(
		baseline_free > 0, "arena ingestion produced no free bytes"
	);

	pthread_t threads[THREAD_COUNT];
	struct thread_args args[THREAD_COUNT];
	for (int idx = 0; idx < THREAD_COUNT; ++idx) {
		args[idx].seed = (unsigned)(0xc0ffeeu + (unsigned)idx * 7919u);
		args[idx].ba = &ba;
		TEST_ASSERT(
			pthread_create(
				&threads[idx], NULL, stress_worker, &args[idx]
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
		"one or more worker threads observed a broken invariant"
	);

	TEST_ASSERT(
		g_root.balloc_count == g_root.bfree_count,
		"root balloc_count/bfree_count mismatch after churn: %zu != "
		"%zu",
		g_root.balloc_count,
		g_root.bfree_count
	);
	TEST_ASSERT(
		g_root.balloc_size == g_root.bfree_size,
		"root balloc_size/bfree_size mismatch after churn: %zu != %zu",
		g_root.balloc_size,
		g_root.bfree_size
	);
	TEST_ASSERT(
		ADDR_OF(&g_root.first_child) == NULL,
		"root must have no children left after churn"
	);

	size_t drained_free = block_allocator_free_size(&ba);
	TEST_ASSERT(
		drained_free == baseline_free,
		"allocator free size did not return to baseline: got %zu, "
		"want %zu",
		drained_free,
		baseline_free
	);

	// Exhaustive sweep: repeatedly take the smallest block until the
	// allocator is empty. This walks the real free lists (unlike
	// block_allocator_free_size, which only sums pool counters), so a
	// free list corrupted by an unlocked race shows up here as a
	// duplicate pointer, a wrong leftover free size, or a runaway
	// iteration count.
	size_t sweep_cap = baseline_free / MEMORY_BLOCK_ALLOCATOR_MIN_SIZE + 16;
	void **swept = malloc(sweep_cap * sizeof(void *));
	TEST_ASSERT(
		swept != NULL, "failed to allocate sweep bookkeeping array"
	);

	size_t swept_count = 0;
	for (;;) {
		void *p =
			memory_balloc(&g_root, MEMORY_BLOCK_ALLOCATOR_MIN_SIZE);
		if (p == NULL) {
			break;
		}
		TEST_ASSERT(
			swept_count < sweep_cap,
			"sweep exceeded expected iteration cap %zu; free list "
			"is likely corrupted",
			sweep_cap
		);
		swept[swept_count++] = p;
	}

	TEST_ASSERT(
		block_allocator_free_size(&ba) == 0,
		"allocator still reports free bytes after an exhaustive sweep"
	);

	qsort(swept, swept_count, sizeof(void *), cmp_ptr);
	for (size_t idx = 1; idx < swept_count; ++idx) {
		TEST_ASSERT(
			swept[idx] != swept[idx - 1],
			"sweep returned the same block twice: %p",
			swept[idx]
		);
	}

	free(swept);
	memory_context_fini(&g_root);
	free(raw);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_concurrent_balloc_and_context_churn() != 0) {
		LOG(ERROR, "test_concurrent_balloc_and_context_churn failed");
		return -1;
	}

	LOG(INFO, "balloc thread stress tests: OK");
	return 0;
}
