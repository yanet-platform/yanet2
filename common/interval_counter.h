#pragma once

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

#include "common/memory.h"
#include "memory_address.h"

////////////////////////////////////////////////////////////////////////////////

struct interval_counter {
	struct memory_context *mctx;

	// power of 2 (range_size=2^k)
	uint32_t range_size;

	uint32_t range_size_bits;

	struct {
		int64_t value;
		uint32_t gen;
	} *values;

#ifndef NDEBUG
	uint32_t max_timeout;
#endif

	uint32_t now;
};

////////////////////////////////////////////////////////////////////////////////

static inline int
interval_counter_init(
	struct interval_counter *counter,
	uint32_t now,
	uint32_t max_timeout,
	struct memory_context *mctx
);

static inline void
interval_counter_free(struct interval_counter *counter);

static inline void
interval_counter_advance_time(struct interval_counter *counter, uint32_t to);

static inline uint64_t
interval_counter_current_count(struct interval_counter *counter);

static inline void
interval_counter_put(
	struct interval_counter *counter,
	uint32_t from,
	uint32_t timeout,
	int32_t cnt
);

////////////////////////////////////////////////////////////////////////////////

static inline int
interval_counter_init(
	struct interval_counter *counter,
	uint32_t now,
	uint32_t max_timeout,
	struct memory_context *mctx
) {
	uint32_t len = 2 * max_timeout;
	uint32_t log_len = 31 - __builtin_clz(len);
	counter->range_size_bits = log_len + 1;
	counter->range_size = 1ll << counter->range_size_bits;

	SET_OFFSET_OF(&counter->mctx, mctx);
	counter->values = memory_balloc(
		mctx, (size_t)counter->range_size * sizeof(*counter->values)
	);
	if (counter->values == NULL) {
		return -1;
	}
	memset(counter->values,
	       0,
	       (size_t)counter->range_size * sizeof(*counter->values));
	SET_OFFSET_OF(&counter->values, counter->values);

#ifndef NDEBUG
	counter->max_timeout = max_timeout;
#endif

	counter->now = now;

	return 0;
}

static inline void
interval_counter_free(struct interval_counter *counter) {
	memory_bfree(
		ADDR_OF(&counter->mctx),
		ADDR_OF(&counter->values),
		counter->range_size * sizeof(*counter->values)
	);
}

static inline int64_t *
interval_counter_get(struct interval_counter *counter, uint32_t point) {
	typedef typeof(*counter->values) value_t;
	value_t *value =
		ADDR_OF(&counter->values) + (point & (counter->range_size - 1));
	uint32_t gen = point >> counter->range_size_bits;
	value->value = value->value * (int64_t)(gen == value->gen);
	value->gen = gen;
	return &value->value;
}

static inline void
interval_counter_advance_time(struct interval_counter *counter, uint32_t to) {
	assert(counter->now <= to);
	typeof(*counter->values) *values = ADDR_OF(&counter->values);
	while (counter->now < to) {
		uint64_t prev = values[counter->now & (counter->range_size - 1)]
					.value; // because power of 2
		++counter->now;
		*interval_counter_get(counter, counter->now) += prev;
	}
}

static inline uint64_t
interval_counter_current_count(struct interval_counter *counter) {
	int64_t value = (ADDR_OF(&counter->values) +
			 (counter->now & (counter->range_size - 1)))
				->value;
	assert(value >= 0);
	return (uint64_t)value;
}

static inline void
interval_counter_put(
	struct interval_counter *counter,
	uint32_t from,
	uint32_t timeout,
	int32_t cnt
) {
	*interval_counter_get(counter, from) += cnt;
	*interval_counter_get(counter, from + timeout) -= cnt;
}