#pragma once

#include "common/memory.h"
#include "common/numutils.h"
#include "common/radix.h"
#include "common/value.h"

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
		if (!new_count)
			new_count = 1;
		new_values = (uint32_t *)memory_balloc(
			memory_context, new_count * sizeof(uint32_t)
		);

		if (new_values == NULL)
			return -1;
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

	if (value > range_index->max_value)
		range_index->max_value = value;

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
	uint64_t capacity = 1 << uint64_log(range_index->count);
	memory_bfree(
		ADDR_OF(&range_index->memory_context),
		ADDR_OF(&range_index->values),
		capacity * sizeof(uint32_t)
	);

	radix_free(&range_index->radix);
}
