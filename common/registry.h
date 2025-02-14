#pragma once

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "value.h"

/*
 * Value registry required to map a key into range of unique values.
 * The registry consists of an array of values and key mapping denoting
 * a sub-range of unique values inside the all values array.
 */

#define VALUE_COLLECTOR_CHUNK_SIZE 4096

/*
 * Value collector is a simple array where each item contains inforamation
 * if a value was used while the current generation.
 */
struct value_collector {
	uint32_t **use_map;
	uint32_t chunk_count;
	uint32_t gen;
};

static inline int
value_collector_init(struct value_collector *collector) {
	// zero-initialized array
	collector->use_map = (uint32_t **)NULL;
	collector->chunk_count = 0;
	collector->gen = 0;

	return 0;
}

static inline void
value_collector_free(struct value_collector *collector) {
	for (uint32_t chunk_idx = 0; chunk_idx < collector->chunk_count;
	     ++chunk_idx)
		free(collector->use_map[chunk_idx]);
	free(collector->use_map);
}

static void
value_collector_reset(struct value_collector *collector) {
	collector->gen++;
}

/*
 * The routine touches a value returnin if the value was seen before while
 * the current generation.
 */
static inline int
value_collector_collect(struct value_collector *collector, uint32_t value) {
	uint32_t chunk_idx = value / VALUE_COLLECTOR_CHUNK_SIZE;
	if (chunk_idx >= collector->chunk_count) {
		uint32_t **use_map = (uint32_t **)realloc(
			collector->use_map,
			sizeof(uint32_t **) * (chunk_idx + 1)
		);
		if (use_map == NULL)
			return -1;
		memset(use_map + collector->chunk_count,
		       0,
		       sizeof(uint32_t *) *
			       (chunk_idx + 1 - collector->chunk_count));
		collector->use_map = use_map;
		collector->chunk_count = chunk_idx + 1;
	}

	if (collector->use_map[chunk_idx] == NULL) {
		collector->use_map[chunk_idx] = (uint32_t *)calloc(
			VALUE_COLLECTOR_CHUNK_SIZE, sizeof(uint32_t)
		);
		if (collector->use_map[chunk_idx] == NULL)
			return -1;
	}

	uint32_t value_idx = value % VALUE_COLLECTOR_CHUNK_SIZE;

	if (collector->use_map[chunk_idx][value_idx] == collector->gen)
		return 0;
	collector->use_map[chunk_idx][value_idx] = collector->gen;
	return 1;
}

struct value_range {
	uint32_t from;
	uint32_t count;
};

struct value_registry {
	struct value_collector collector;

	uint32_t *values;
	struct value_range *ranges;
	uint32_t value_count;
	uint32_t range_count;

	uint32_t max_value;
};

static inline int
value_registry_init(struct value_registry *registry) {
	if (value_collector_init(&registry->collector))
		return -1;

	registry->values = NULL;
	registry->value_count = 0;
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

	if (!(registry->range_count & (registry->range_count + 1))) {
		struct value_range *new_ranges = (struct value_range *)realloc(
			registry->ranges,
			sizeof(struct value_range) *
				((registry->range_count + 1) * 2)
		);
		if (new_ranges == NULL)
			return -1;
		registry->ranges = new_ranges;
	}

	registry->ranges[registry->range_count++] =
		(struct value_range){registry->value_count, 0};

	return 0;
}

static inline int
value_registry_collect(struct value_registry *registry, uint32_t value) {
	if (!(registry->value_count & (registry->value_count + 1))) {
		uint32_t *new_values = (uint32_t *)realloc(
			registry->values,
			sizeof(uint32_t) * (registry->value_count + 1) * 2
		);
		if (new_values == NULL)
			return -1;
		registry->values = new_values;
	}

	if (value_collector_collect(&registry->collector, value)) {
		registry->values[registry->value_count++] = value;
		registry->ranges[registry->range_count - 1].count++;
		if (value >= registry->max_value)
			registry->max_value = value;
	}

	return 0;
}

static inline void
value_registry_free(struct value_registry *registry) {
	value_collector_free(&registry->collector);
	free(registry->ranges);
	free(registry->values);
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
	struct value_range *range1 = registry1->ranges + range_idx;
	struct value_range *range2 = registry2->ranges + range_idx;
	for (uint32_t idx1 = range1->from; idx1 < range1->from + range1->count;
	     ++idx1) {
		for (uint32_t idx2 = range2->from;
		     idx2 < range2->from + range2->count;
		     ++idx2) {

			uint32_t v1 = registry1->values[idx1];
			uint32_t v2 = registry2->values[idx2];

			join_func(v1, v2, range_idx, join_func_data);
		}
	}
	return 0;
}

/*
 * FIXME: the function is tricky and should be refactored.
 */
static inline int
value_registry_compact(
	struct value_registry *src_registry,
	struct value_table *values,
	struct value_registry *dst_registry
) {
	if (value_registry_init(dst_registry)) {
		return -1;
	}

	for (uint32_t r_idx = 0; r_idx < values->remap_table.count; ++r_idx) {
		struct remap_item *item =
			remap_table_item(&values->remap_table, r_idx);
		if (!item->count) {
			continue;
		}

		if (value_registry_start(dst_registry))
			goto error;

		struct value_range *range = src_registry->ranges + r_idx;
		for (uint32_t v_idx = range->from;
		     v_idx < range->from + range->count;
		     ++v_idx) {
			value_registry_collect(
				dst_registry, src_registry->values[v_idx]
			);
		}
	}

	value_table_compact(values);

	return 0;

error:
	value_registry_free(dst_registry);
	return -1;
}
