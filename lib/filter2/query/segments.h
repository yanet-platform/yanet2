#pragma once

#include "classifiers/segments.h"
#include "common/btree/u16.h"
#include "common/btree/u32.h"
#include "common/memory_address.h"

enum {
	segment_u16_classifier_max_batch_size = btree_u16_max_batch_size,
	segments_u32_classifier_max_batch_size = btree_u32_max_batch_size,
	segments_u64_classifier_max_batch_size = btree_u64_max_batch_size,
};

static inline size_t
segment_u16_classify(
	struct segment_u16_classifier *classifier,
	size_t values_count,
	uint16_t *values,
	uint32_t *result
) {
	values_count = btree_u16_upper_bounds(
		&classifier->btree, values, values_count, result
	);
	uint32_t *open = ADDR_OF(&classifier->open);
	for (size_t i = 0; i < values_count; ++i) {
		if (unlikely(result[i] == 0 || open[result[i] - 1] == 0)) {
			result[i] = 0;
		}
	}
	return values_count;
}

static inline size_t
segments_u32_classify(
	struct segments_u32_classifier *classifier,
	size_t values_count,
	uint32_t *values,
	uint32_t *result
) {
	values_count = btree_u32_upper_bounds(
		&classifier->btree, values, values_count, result
	);
	uint32_t *open = ADDR_OF(&classifier->open);
	for (size_t i = 0; i < values_count; ++i) {
		if (unlikely(result[i] == 0 || open[result[i] - 1] == 0)) {
			result[i] = 0;
		}
	}
	return values_count;
}

static inline size_t
segments_u64_classify(
	struct segments_u64_classifier *classifier,
	size_t values_count,
	uint64_t *values,
	uint32_t *result
) {
	values_count = btree_u64_upper_bounds(
		&classifier->btree, values, values_count, result
	);
	uint32_t *open = ADDR_OF(&classifier->open);
	for (size_t i = 0; i < values_count; ++i) {
		if (unlikely(result[i] == 0 || open[result[i] - 1] == 0)) {
			result[i] = 0;
		}
	}
	return values_count;
}