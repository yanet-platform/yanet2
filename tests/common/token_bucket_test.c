// Shared token-bucket arithmetic and bounded concurrent reservations.
//
// Frozen clocks make the budget deterministic. Stale snapshots distinguish
// idle collision losses from active tickets, retained debt and later recovery.
#include <assert.h>
#include <pthread.h>
#include <sched.h>
#include <stdint.h>

#include "common/token_bucket.h"

_Static_assert(
	_Alignof(struct token_bucket) == YANET_CACHE_LINE_SIZE,
	"bucket alignment must follow the platform cache line"
);
_Static_assert(
	sizeof(struct token_bucket) % YANET_CACHE_LINE_SIZE == 0,
	"adjacent buckets must not share a cache line"
);

static void
run_burst_refill_and_idle_cap_test(void) {
	struct token_bucket bucket;
	token_bucket_init(&bucket, 1000000, 32, 0);
	for (uint64_t idx = 0; idx < 32; ++idx) {
		assert(token_bucket_take(&bucket, 0));
	}
	uint64_t edt = atomic_load(&bucket.edt);
	for (uint64_t idx = 0; idx < 100; ++idx) {
		assert(token_bucket_full(&bucket, 0));
		assert(!token_bucket_take(&bucket, 999999));
	}
	assert(edt == atomic_load(&bucket.edt));
	assert(token_bucket_take(&bucket, 1000000));
	assert(token_bucket_full(&bucket, 1000000));
	for (uint64_t idx = 0; idx < 32; ++idx) {
		assert(token_bucket_take(&bucket, 10000000000ULL));
	}
	assert(token_bucket_full(&bucket, 10000000000ULL));
}

static void
run_continuous_windows_without_free_burst_test(void) {
	struct token_bucket bucket;
	token_bucket_init(&bucket, 1000000, 32, 0);
	for (uint64_t second = 0; second < 5; ++second) {
		uint64_t accepted = 0;
		for (uint64_t tick = 0; tick < 10000; ++tick) {
			accepted += token_bucket_take(
				&bucket, second * 1000000000ULL + tick * 100000
			);
		}
		assert(accepted == (second == 0 ? 1031 : 1000));
	}
}

static void
run_failed_restart_and_late_reservation_test(void) {
	struct token_bucket bucket;
	token_bucket_init(&bucket, 100, 32, 0);
	uint64_t stale = atomic_load(&bucket.edt);
	assert(token_bucket_reserve(&bucket, 10000, stale));
	assert(!token_bucket_reserve(&bucket, 10000, stale));
	assert(!token_bucket_full(&bucket, 10000));
	assert(10100 == atomic_load(&bucket.edt));

	stale = atomic_load(&bucket.edt);
	assert(token_bucket_reserve(&bucket, 10000, stale));
	assert(token_bucket_reserve(&bucket, 10000, stale));
	assert(10300 == atomic_load(&bucket.edt));
	assert(!token_bucket_full(&bucket, 10000));
	for (uint64_t idx = 3; idx < 32; ++idx) {
		assert(token_bucket_take(&bucket, 10000));
	}
	assert(13200 == atomic_load(&bucket.edt));
	assert(!token_bucket_reserve(&bucket, 10000, stale));
	assert(13300 == atomic_load(&bucket.edt));
	assert(token_bucket_full(&bucket, 10000));
	assert(!token_bucket_take(&bucket, 10000));
	assert(!token_bucket_take(&bucket, 10199));
	assert(13300 == atomic_load(&bucket.edt));
	assert(token_bucket_take(&bucket, 10200));
	assert(13400 == atomic_load(&bucket.edt));
	assert(token_bucket_full(&bucket, 10200));
}

static void
run_clock_skew_and_wrap_test(void) {
	struct token_bucket bucket;
	token_bucket_init(&bucket, 100, 32, 0);
	assert(token_bucket_take(&bucket, 10000));
	assert(token_bucket_full(&bucket, 0));
	assert(!token_bucket_take(&bucket, 0));
	assert(token_bucket_take(&bucket, 10100));
}

// Verifies that fresh credit and replenishment survive unsigned clock wrap.
static void
run_fresh_start_before_and_through_wrap_test(void) {
	const uint64_t starts[] = {
		0, UINT64_MAX - 10000, UINT64_MAX - 1000, UINT64_MAX
	};
	for (unsigned idx = 0; idx < sizeof(starts) / sizeof(starts[0]);
	     ++idx) {
		struct token_bucket bucket;
		uint64_t now = starts[idx];
		token_bucket_init(&bucket, 100, 32, now);
		assert(!token_bucket_full(&bucket, now));
		assert(atomic_load(&bucket.edt) == now);
		for (unsigned packet = 0; packet < 32; ++packet) {
			assert(token_bucket_take(&bucket, now));
		}
		uint64_t finish = now + 3200;
		assert(atomic_load(&bucket.edt) == finish);
		assert(token_bucket_full(&bucket, now));
		assert(!token_bucket_take(&bucket, now + 99));
		assert(atomic_load(&bucket.edt) == finish);
		assert(token_bucket_take(&bucket, now + 100));
		assert(token_bucket_full(&bucket, now + 100));
		for (unsigned packet = 0; packet < 32; ++packet) {
			assert(token_bucket_take(&bucket, now + 10000));
		}
		assert(token_bucket_full(&bucket, now + 10000));
	}
}

#define THREAD_COUNT 16

struct concurrent_round {
	struct token_bucket *bucket;
	_Atomic unsigned ready;
	_Atomic bool start;
	_Atomic unsigned accepted;
	uint64_t now;
	bool stale_restart;
};

// All threads reserve against the same frozen clock and start gate.
static void *
reserve_concurrently(void *argument) {
	struct concurrent_round *round = argument;
	uint64_t edt =
		atomic_load_explicit(&round->bucket->edt, memory_order_relaxed);
	atomic_fetch_add_explicit(&round->ready, 1, memory_order_release);
	while (!atomic_load_explicit(&round->start, memory_order_acquire)) {
		sched_yield();
	}
	unsigned accepted = 0;
	if (round->stale_restart) {
		accepted = token_bucket_reserve(round->bucket, round->now, edt);
	} else {
		for (unsigned idx = 0; idx < 128; ++idx) {
			accepted +=
				token_bucket_take(round->bucket, round->now);
		}
	}
	atomic_fetch_add_explicit(
		&round->accepted, accepted, memory_order_relaxed
	);
	return NULL;
}

static void
run_concurrent_startup_test(void) {
	for (unsigned repeat = 0; repeat < 20; ++repeat) {
		struct token_bucket bucket;
		token_bucket_init(&bucket, 100, 32, 0);
		struct concurrent_round round = {
			.bucket = &bucket,
			.now = repeat % 2 == 0 ? 0 : 10000,
			.stale_restart = repeat % 2 != 0,
		};
		pthread_t threads[THREAD_COUNT];
		for (unsigned idx = 0; idx < THREAD_COUNT; ++idx) {
			int result = pthread_create(
				&threads[idx],
				NULL,
				reserve_concurrently,
				&round
			);
			assert(result == 0);
		}
		while (atomic_load_explicit(
			       &round.ready, memory_order_acquire
		       ) != THREAD_COUNT) {
			sched_yield();
		}
		atomic_store_explicit(&round.start, true, memory_order_release);
		for (unsigned idx = 0; idx < THREAD_COUNT; ++idx) {
			assert(pthread_join(threads[idx], NULL) == 0);
		}
		unsigned accepted = atomic_load(&round.accepted);
		if (round.stale_restart) {
			assert(accepted == 1);
			assert(atomic_load(&bucket.edt) ==
			       round.now + bucket.cost);
			assert(!token_bucket_full(&bucket, round.now));
		} else {
			assert(accepted == 32);
			assert(token_bucket_full(&bucket, round.now));
		}
	}
}

int
main(void) {
	run_burst_refill_and_idle_cap_test();
	run_continuous_windows_without_free_burst_test();
	run_failed_restart_and_late_reservation_test();
	run_clock_skew_and_wrap_test();
	run_fresh_start_before_and_through_wrap_test();
	run_concurrent_startup_test();
	return 0;
}
