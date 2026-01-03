#pragma once

#include "common/memory_address.h"
#include "counters/counters.h"
#include <stdint.h>

#include "handler/real.h"

static inline struct real_stats *
real_counter(
	struct real *real, size_t worker, struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(real->counter_id, worker, storage);
	return (struct real_stats *)counter;
}

static inline struct real_state *
real_state(struct real *real) {
	return ADDR_OF(&real->state);
}