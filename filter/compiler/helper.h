#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter/rule.h"

int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
);

static inline int
lpm_collect_value_iterator(uint32_t value, void *data) {
	struct value_table *table = (struct value_table *)data;
	return value_table_touch(table, 0, value);
}

static inline int
lpm_collect_registry_iterator(uint32_t value, void *data) {
	struct value_registry *registry = (struct value_registry *)data;
	return value_registry_collect(registry, value);
}

struct segment_u32 {
	uint32_t from;
	uint32_t to;
};

struct segment_u64 {
	uint64_t from;
	uint64_t to;
};

/**
 * @brief Merge overlapping or adjacent 32-bit segments.
 *
 * Takes a sorted array of segments and merges any that overlap or are adjacent.
 * Segments are considered overlapping if cur.from <= last.to + 1.
 *
 * @param segments Array of segments sorted by 'from' field
 * @param cnt Number of segments in the array
 * @return Number of segments after merging
 */
static inline size_t
merge_segments_u32(struct segment_u32 *segments, size_t cnt) {
	if (cnt == 0) {
		return 0;
	}

	size_t taken = 0;
	for (size_t i = 0; i < cnt; ++i) {
		if (taken == 0) {
			// First segment
			segments[taken++] = segments[i];
		} else {
			// Check if current segment overlaps with the last
			// merged segment
			struct segment_u32 *last = &segments[taken - 1];
			if (segments[i].from <= last->to + 1) {
				// Overlapping or adjacent - merge by extending
				// 'to'
				if (segments[i].to > last->to) {
					last->to = segments[i].to;
				}
			} else {
				// Non-overlapping - add as new segment
				segments[taken++] = segments[i];
			}
		}
	}

	return taken;
}

/**
 * @brief Merge overlapping or adjacent 64-bit segments.
 *
 * Takes a sorted array of segments and merges any that overlap or are adjacent.
 * Segments are considered overlapping if cur.from <= last.to + 1.
 *
 * @param segments Array of segments sorted by 'from' field
 * @param cnt Number of segments in the array
 * @return Number of segments after merging
 */
static inline size_t
merge_segments_u64(struct segment_u64 *segments, size_t cnt) {
	if (cnt == 0) {
		return 0;
	}

	size_t taken = 0;
	for (size_t i = 0; i < cnt; ++i) {
		if (taken == 0) {
			// First segment
			segments[taken++] = segments[i];
		} else {
			// Check if current segment overlaps with the last
			// merged segment
			struct segment_u64 *last = &segments[taken - 1];
			if (segments[i].from <= last->to + 1) {
				// Overlapping or adjacent - merge by extending
				// 'to'
				if (segments[i].to > last->to) {
					last->to = segments[i].to;
				}
			} else {
				// Non-overlapping - add as new segment
				segments[taken++] = segments[i];
			}
		}
	}

	return taken;
}