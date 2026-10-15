#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/token_bucket.h"

#define PDUMP_RATE_PPS_MAX 1000000ULL

static inline size_t
pdump_rate_bucket_raw_size(void) {
	return sizeof(struct token_bucket) + _Alignof(struct token_bucket) - 1;
}

static inline struct token_bucket *
pdump_rate_bucket_align(void *raw) {
	size_t alignment = _Alignof(struct token_bucket);
	return (void *)(((uintptr_t)raw + alignment - 1) &
			~(uintptr_t)(alignment - 1));
}

// Round packet cost upward; bounded rates lose less than 0.1% to rounding.
static inline void
pdump_rate_init(struct token_bucket *bucket, uint64_t rate_pps, uint64_t now) {
	assert(rate_pps != 0 && rate_pps <= PDUMP_RATE_PPS_MAX);
	uint64_t burst = (rate_pps + 999) / 1000;
	if (burst < 32) {
		burst = 32;
	}
	uint64_t cost = (1000000000ULL + rate_pps - 1) / rate_pps;
	token_bucket_init(bucket, cost, burst, now);
}
