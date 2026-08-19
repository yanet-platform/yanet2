#pragma once

#include "memory.h"
#include "numutils.h"
#include "radix.h"
#include "value.h"

#include "key.h"
#include "lpm.h"

#include <strings.h>

struct range_index {
	struct memory_context *memory_context;
	struct radix radix;
	uint32_t *values;
	uint32_t count;
	uint32_t max_value;
};

static inline int
range_index_init(
	struct range_index *range_index, struct memory_context *memory_context
) {
	SET_OFFSET_OF(&range_index->memory_context, memory_context);

	if (radix_init(&range_index->radix, memory_context)) {
		return -1;
	}

	SET_OFFSET_OF(&range_index->values, NULL);
	range_index->count = 0;
	range_index->max_value = 0;

	return 0;
}

static inline int
range_index_insert(
	struct range_index *range_index,
	uint8_t key_size,
	uint8_t *key,
	uint32_t value
) {
	struct memory_context *memory_context =
		ADDR_OF(&range_index->memory_context);

	uint32_t *old_values = ADDR_OF(&range_index->values);
	uint32_t old_count = range_index->count;

	uint32_t *new_values = old_values;
	uint32_t new_count = old_count;

	if (!((range_index->count - 1) & range_index->count)) {
		old_count = range_index->count;
		new_count = old_count * 2;
		if (!new_count) {
			new_count = 1;
		}
		new_values = (uint32_t *)memory_balloc(
			memory_context, new_count * sizeof(uint32_t)
		);

		if (new_values == NULL) {
			return -1;
		}
		if (old_count > 0) {
			memcpy(new_values,
			       old_values,
			       old_count * sizeof(uint32_t));
		}
	}

	if (radix_insert(
		    &range_index->radix, key_size, key, range_index->count
	    )) {
		if (new_values != old_values) {
			memory_bfree(
				memory_context,
				new_values,
				new_count * sizeof(uint32_t)
			);
		}
		return -1;
	}

	new_values[range_index->count++] = value;
	SET_OFFSET_OF(&range_index->values, new_values);

	if (old_values != new_values) {
		memory_bfree(
			memory_context, old_values, old_count * sizeof(uint32_t)
		);
	}

	if (value > range_index->max_value) {
		range_index->max_value = value;
	}

	return 0;
}

static inline void
range_index_remap(
	struct range_index *range_index, struct value_table *value_table
) {
	uint32_t *values = ADDR_OF(&range_index->values);

	for (uint32_t idx = 0; idx < range_index->count; ++idx) {
		values[idx] = value_table_get(value_table, 0, values[idx]);
	}
}

static inline void
range_index_free(struct range_index *range_index) {
	if (range_index->count == 0) {
		radix_free(&range_index->radix);
		return;
	}
	uint64_t capacity = next_power_of_two(range_index->count);
	memory_bfree(
		ADDR_OF(&range_index->memory_context),
		ADDR_OF(&range_index->values),
		capacity * sizeof(uint32_t)
	);

	SET_OFFSET_OF(&range_index->values, NULL);

	radix_free(&range_index->radix);
}

/*
 * Build an LPM from a range_index.
 *
 * The range_index stores a contiguous, ascending, non-overlapping partition
 * of the full keyspace: radix maps each range-start key -> an index into the
 * values[] array.  This function walks the radix in ascending order, derives
 * each range's upper bound from the next key, and inserts [from..to] -> value
 * into the LPM.  The LPM must already be initialised by the caller.
 */
struct range_index_lpm_ctx {
	struct lpm *lpm;
	const uint32_t *values;
	uint8_t prev_from[LPM_KEY_SIZE_MAX];
	uint32_t prev_value;
};

static inline int
range_index_lpm_cb(
	uint8_t key_size, const uint8_t *from, uint32_t index, void *data
) {
	struct range_index_lpm_ctx *ctx = (struct range_index_lpm_ctx *)data;

	if (ctx->prev_value != LPM_VALUE_INVALID) {
		uint8_t to[key_size];
		memcpy(to, from, key_size);
		filter_key_dec(key_size, to);
		if (lpm_insert(
			    ctx->lpm,
			    key_size,
			    ctx->prev_from,
			    to,
			    ctx->prev_value
		    )) {
			return -1;
		}
	}

	memcpy(ctx->prev_from, from, key_size);
	ctx->prev_value = ctx->values[index];
	return 0;
}

static inline int
range_index_build_lpm(
	const struct range_index *range_index, uint8_t key_size, struct lpm *lpm
) {
	struct range_index_lpm_ctx ctx;
	ctx.lpm = lpm;
	ctx.values = ADDR_OF(&range_index->values);
	ctx.prev_value = LPM_VALUE_INVALID;

	if (radix_walk(
		    &range_index->radix, key_size, range_index_lpm_cb, &ctx
	    )) {
		return -1;
	}

	if (ctx.prev_value != LPM_VALUE_INVALID) {
		uint8_t to[key_size];
		memset(to, 0xff, key_size);
		if (lpm_insert(
			    lpm, key_size, ctx.prev_from, to, ctx.prev_value
		    )) {
			return -1;
		}
	}

	return 0;
}
