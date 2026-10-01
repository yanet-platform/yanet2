// Verify per-worker packet-rate accounting without a shared hot counter.

// Exercise disabled limiting, fractional rates, independent workers, burst
// bounds and elapsed-time arithmetic at the edge of the timestamp range.
#include <assert.h>
#include <stdint.h>

#include "modules/pdump/dataplane/limit.h"

static void
test_disabled_limit(void) {
	struct ring_buffer ring = {0};
	struct pdump_rate rate = pdump_rate_init(0, 2);
	for (uint64_t idx = 0; idx < 64; idx++) {
		assert(pdump_rate_allow(&ring, 0, &rate));
	}
	assert(!ring.rate_initialized);
}

static void
test_fractional_rate(void) {
	struct ring_buffer rings[2] = {0};
	struct pdump_rate rate = pdump_rate_init(1, 2);
	for (uint64_t idx = 0; idx < 2; idx++) {
		assert(pdump_rate_allow(&rings[idx], 1, &rate));
		assert(!pdump_rate_allow(&rings[idx], 1, &rate));
		assert(!pdump_rate_allow(&rings[idx], 1000000001ULL, &rate));
		assert(pdump_rate_allow(&rings[idx], 2000000001ULL, &rate));
	}
}

static void
test_burst_and_overflow(void) {
	struct ring_buffer ring = {0};
	struct pdump_rate rate = pdump_rate_init(UINT64_MAX, 2);
	assert(pdump_rate_allow(&ring, 1, &rate));
	for (uint64_t idx = 0; idx < 32; idx++) {
		assert(pdump_rate_allow(&ring, UINT64_MAX, &rate));
	}
	assert(!pdump_rate_allow(&ring, UINT64_MAX, &rate));
	assert(!pdump_rate_allow(&ring, 0, &rate));
}

static void
test_uneven_worker_load(void) {
	struct ring_buffer ring = {0};
	struct pdump_rate rate = pdump_rate_init(1000, 2);
	uint64_t captured = 0;
	for (uint64_t millisecond = 0; millisecond <= 1000; millisecond++) {
		captured += pdump_rate_allow(
			&ring, millisecond * 1000000ULL, &rate
		);
	}
	assert(captured == 501);
}

int
main(void) {
	test_disabled_limit();
	test_fractional_rate();
	test_burst_and_overflow();
	test_uneven_worker_load();
	return 0;
}
