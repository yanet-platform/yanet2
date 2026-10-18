// Pdump rate rounding and sequential aggregate burst bounds.
#include <assert.h>
#include <stdint.h>

#include "modules/pdump/dataplane/limit.h"

static void
run_rate_cost_and_burst_test(void) {
	const uint64_t rates[] = {1, 1000, 32000, 32001, PDUMP_RATE_PPS_MAX};
	for (unsigned idx = 0; idx < sizeof(rates) / sizeof(rates[0]); ++idx) {
		struct token_bucket bucket;
		pdump_rate_init(&bucket, rates[idx], 0);
		uint64_t burst = (rates[idx] + 999) / 1000;
		if (burst < 32) {
			burst = 32;
		}
		assert(bucket.cost * rates[idx] >= 1000000000ULL);
		assert((bucket.cost - 1) * rates[idx] < 1000000000ULL);
		for (uint64_t packet = 0; packet < burst; ++packet) {
			assert(token_bucket_take(&bucket, 0));
		}
		assert(token_bucket_full(&bucket, 0));
		assert(!token_bucket_take(&bucket, bucket.cost - 1));
		assert(token_bucket_take(&bucket, bucket.cost));
	}
}

int
main(void) {
	run_rate_cost_and_burst_test();
	return 0;
}
