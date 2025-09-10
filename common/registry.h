#pragma once

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "common/exp_array.h"
#include "common/memory.h"
#include "value.h"

/*
 * Value registry required to map a key into range of unique values.
 * The registry consists of an array of values and key mapping denoting
 * a sub-range of unique values inside the all values array.
 */

#define VALUE_COLLECTOR_CHUNK_SIZE 4096
#define VALUE_COLLECTOR_UNTOUCHED ((uint32_t)-1)

/*
 * Value collector is a simple array where each item contains inforamation
 * if a value was used while the current generation.
 */
struct value_collector {
	struct memory_context *memory_context;
	uint32_t **use_map;
	uint32_t chunk_count;
	uint32_t gen;
};

static inline int
value_collector_init(
	struct value_collector *collector, struct memory_context *memory_context
) {
	collector->memory_context = memory_context;
	// zero-initialized array
	collector->use_map = NULL;
	collector->chunk_count = 0;
	collector->gen = 0;

	return 0;
}

static inline void
value_collector_free(struct value_collector *collector) {
	uint32_t **use_map = ADDR_OF(&collector->use_map);

	for (uint32_t chunk_idx = 0; chunk_idx < collector->chunk_count;
	     ++chunk_idx) {
		uint32_t *chunk = ADDR_OF(&use_map[chunk_idx]);
		if (chunk != NULL)
			memory_bfree(
				collector->memory_context,
				chunk,
				VALUE_COLLECTOR_CHUNK_SIZE * sizeof(uint32_t)
			);
	}
	memory_bfree(
		collector->memory_context,
		use_map,
		collector->chunk_count * sizeof(uint32_t *)
	);
}

static void
value_collector_reset(struct value_collector *collector) {
	collector->gen++;
}

/*
 * Routine returns 1 if value was not seen during current generation,
 * 0 if it was seen, and -1 in case of error.
 */
static inline int
value_collector_check(struct value_collector *collector, uint32_t value) {
	uint32_t chunk_idx = value / VALUE_COLLECTOR_CHUNK_SIZE;
	uint32_t **use_map = ADDR_OF(&collector->use_map);

	if (chunk_idx >= collector->chunk_count) {
		uint32_t new_chunk_count = chunk_idx + 1;

		uint32_t **new_use_map = (uint32_t **)memory_balloc(
			collector->memory_context,
			new_chunk_count * sizeof(uint32_t *)
		);

		if (new_use_map == NULL)
			return -1;

		// Set correct relative addresses
		for (uint32_t idx = 0; idx < collector->chunk_count; ++idx) {
			uint32_t *chunk = ADDR_OF(&use_map[idx]);
			SET_OFFSET_OF(&new_use_map[idx], chunk);
		}

		for (uint32_t idx = collector->chunk_count;
		     idx < new_chunk_count;
		     ++idx)
			new_use_map[idx] = NULL;

		memory_bfree(
			collector->memory_context,
			use_map,
			collector->chunk_count * sizeof(uint32_t *)
		);

		use_map = new_use_map;
		SET_OFFSET_OF(&collector->use_map, use_map);
		collector->chunk_count = new_chunk_count;
	}

	uint32_t *chunk = ADDR_OF(&use_map[chunk_idx]);
	if (chunk == NULL) {
		chunk = (uint32_t *)memory_balloc(
			collector->memory_context,
			VALUE_COLLECTOR_CHUNK_SIZE * sizeof(uint32_t)
		);

		if (chunk == NULL)
			return -1;

		memset(chunk,
		       0xff,
		       VALUE_COLLECTOR_CHUNK_SIZE * sizeof(uint32_t));

		memset(chunk, 0, VALUE_COLLECTOR_CHUNK_SIZE * sizeof(uint32_t));

		SET_OFFSET_OF(&use_map[chunk_idx], chunk);
	}

	uint32_t value_idx = value % VALUE_COLLECTOR_CHUNK_SIZE;
	return chunk[value_idx] != collector->gen;
}

/*
 * The routine touches a value returning 0 if the value was seen before while
 * the current generation, 1 for new values and -1 in case of error
 */
static inline int
value_collector_collect(struct value_collector *collector, uint32_t value) {
	int check = value_collector_check(collector, value);
	if (check != 1)
		return check;

	uint32_t **use_map = ADDR_OF(&collector->use_map);
	uint32_t chunk_idx = value / VALUE_COLLECTOR_CHUNK_SIZE;

	uint32_t *chunk = ADDR_OF(&use_map[chunk_idx]);
	uint32_t value_idx = value % VALUE_COLLECTOR_CHUNK_SIZE;

	chunk[value_idx] = collector->gen;

	return 1;
}

struct value_range {
	uint32_t *values;
	uint64_t count;
};

struct value_registry {
	struct memory_context *memory_context;
	struct value_collector collector;

	struct value_range *ranges;
	uint64_t range_count;

	uint32_t max_value;
};

static inline int
value_registry_init(
	struct value_registry *registry, struct memory_context *memory_context
) {
	if (value_collector_init(&registry->collector, memory_context))
		return -1;

	registry->memory_context = memory_context;

	registry->ranges = NULL;
	registry->range_count = 0;

	registry->max_value = 0;
	return 0;
}

/*
 * the routine start a new registry generation creating new key mapping range.
 */
static inline int
value_registry_start(struct value_registry *registry) {
	value_collector_reset(&registry->collector);

	struct value_range *ranges = ADDR_OF(&registry->ranges);

	if (!((registry->range_count - 1) & registry->range_count)) {
		uint64_t old_capacity = registry->range_count;
		uint64_t new_capacity = old_capacity * 2 + !old_capacity;

		struct value_range *new_ranges =
			(struct value_range *)memory_balloc(
				registry->memory_context,
				new_capacity * sizeof(struct value_range)
			);

		if (new_ranges == NULL)
			return -1;

		for (uint64_t idx = 0; idx < old_capacity; ++idx) {
			SET_OFFSET_OF(
				&new_ranges[idx].values,
				ADDR_OF(&ranges[idx].values)
			);
			new_ranges[idx].count = ranges[idx].count;
		}

		SET_OFFSET_OF(&registry->ranges, new_ranges);

		memory_bfree(
			registry->memory_context,
			ranges,
			sizeof(struct value_range) * old_capacity
		);

		ranges = new_ranges;
	}

	ranges[registry->range_count++] = (struct value_range){NULL, 0};

	SET_OFFSET_OF(&registry->ranges, ranges);

	return 0;
}

static inline int
value_registry_collect(struct value_registry *registry, uint32_t value) {
	if (value_collector_collect(&registry->collector, value) == 1) {
		struct value_range *range =
			ADDR_OF(&registry->ranges) + registry->range_count - 1;
		uint32_t *values = ADDR_OF(&range->values);

		if (mem_array_expand_exp(
			    registry->memory_context,
			    (void **)&values,
			    sizeof(*values),
			    &range->count
		    )) {
			return -1;
		}

		values[range->count - 1] = value;

		if (value > registry->max_value)
			registry->max_value = value;

		SET_OFFSET_OF(&range->values, values);
	}

	return 0;
}

static inline void
value_registry_free(struct value_registry *registry) {
	value_collector_free(&registry->collector);

	for (uint64_t idx = 0; idx < registry->range_count; ++idx) {
		struct value_range *range = ADDR_OF(&registry->ranges) + idx;

		mem_array_free_exp(
			registry->memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		registry->memory_context,
		ADDR_OF(&registry->ranges),
		registry->range_count * sizeof(struct value_range)
	);
}

static inline uint32_t
value_registry_capacity(struct value_registry *registry) {
	return registry->max_value + 1;
}

/*
 * Registry join callback called for each value pair combined from
 * two registry values.
 */
typedef int (*value_registry_join_func)(
	uint32_t first, uint32_t second, uint32_t idx, void *data
);

/*
 * Merges two value registry iteration through registry keys and its values.
 * NOTE: both registry keys should be exact the same.
 */
static inline int
value_registry_join_range(
	struct value_registry *registry1,
	struct value_registry *registry2,
	uint32_t range_idx,
	value_registry_join_func join_func,
	void *join_func_data
) {
	struct value_range *range1 = ADDR_OF(&registry1->ranges) + range_idx;
	uint32_t *values1 = ADDR_OF(&range1->values);
	struct value_range *range2 = ADDR_OF(&registry2->ranges) + range_idx;
	uint32_t *values2 = ADDR_OF(&range2->values);

	for (uint32_t idx1 = 0; idx1 < range1->count; ++idx1) {
		for (uint32_t idx2 = 0; idx2 < range2->count; ++idx2) {

			uint32_t v1 = values1[idx1];
			uint32_t v2 = values2[idx2];

			join_func(v1, v2, range_idx, join_func_data);
		}
	}
	return 0;
}
