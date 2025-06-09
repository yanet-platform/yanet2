#pragma once

#include <stdint.h>

#include "counters.h"

#include "common/numutils.h"

/*
 * The function return bucket index of exponential hystogram counter
 */
static inline uint64_t
counter_hist_bucket_exp2(
	uint64_t value, uint64_t min_bucket, uint64_t max_bucket
) {
	uint64_t bucket = uint64_log(value);
	if (bucket < min_bucket)
		bucket = min_bucket;
	bucket -= min_bucket;
	if (bucket > max_bucket - 1)
		bucket = max_bucket - 1;

	return bucket;
}

static inline void
counter_hist_exp2_inc(
	uint64_t counter_id,
	uint64_t instance_id,
	struct counter_storage *counter_storage,
	uint64_t min_bucket,
	uint64_t max_bucket,
	uint64_t key,
	uint64_t value
) {
	uint64_t bucket = counter_hist_bucket_exp2(key, min_bucket, max_bucket);

	counter_get_address(counter_id, instance_id, counter_storage)[bucket] +=
		value;
}
