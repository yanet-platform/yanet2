/**
 * @file query.h
 * @brief Query helpers and macro interface for classifying packets.
 *
 * Provides:
 *  - filter_actions_with_category(): post-process action list by category
 *  - FILTER_QUERY: run classification for a declared attribute signature
 *
 * Notes:
 *  - FILTER_QUERY returns a pointer to an actions array stored inside filter
 *    memory; it must not be freed by the caller.
 *  - Action iteration preserves order and stops at the first terminal action
 *    (i.e. without ACTION_NON_TERMINATE).
 */
#pragma once

#include "filter.h"
#include "query/attribute.h"
#include "rule.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * @brief Filter actions in-place by category, preserving order until terminal.
 * @param actions Action list (modified in-place).
 * @param count Number of actions in the list.
 * @param category 0-based category index to keep (others are removed).
 * @return Number of actions remaining after filtering.
 */
static inline uint32_t
filter_actions_with_category(
	uint32_t *actions, uint32_t count, uint16_t category
) {
	uint32_t count_category = 0;

	for (uint32_t i = 0; i < count; ++i) {
		uint32_t action = actions[i];
		uint16_t cat = FILTER_ACTION_CATEGORY_MASK(action);

		if (cat == 0 || (cat & (1 << category))) {
			actions[count_category++] = action;
		} else {
			continue;
		}

		if (!(action & ACTION_NON_TERMINATE)) {
			break;
		}
	}

	return count_category;
}

////////////////////////////////////////////////////////////////////////////////

/* Query uses local pair array instead of filter_slots to avoid introducing
 * extra public helper structures in this header. */

/**
 * @def FILTER_QUERY(filter_ptr, tag, packet_ptr, actions_out_ptr,
 * count_out_ptr)
 * @brief Classify a packet using a filter built for signature tag.
 * @param filter_ptr struct filter* built with FILTER_INIT for the same tag.
 * @param tag Name used in FILTER_QUERY_DECLARE(...).
 * @param packet_ptr struct packet* input packet to classify.
 * @param actions_out_ptr uint32_t** receives pointer to action array (owned by
 * filter).
 * @param count_out_ptr uint32_t* receives number of actions.
 */
#define FILTER_QUERY(filter_ptr, tag, packet_ptrs, result, count)              \
	__extension__({                                                        \
		struct filter *__flt = (filter_ptr);                           \
		struct packet **__pkts = (packet_ptrs);                        \
		const size_t __n = sizeof(__filter_attrs_query_##tag) /        \
				   sizeof(__filter_attrs_query_##tag[0]);      \
		/* Local slots storage */                                      \
		uint32_t __slots[2 * MAX_ATTRIBUTES * count];                  \
		/* compute classifiers for leaf attributes into parent slots   \
		 */                                                            \
		for (size_t __ai = 0; __ai < __n; ++__ai) {                    \
			size_t __vtx = __n + __ai;                             \
			struct filter_vertex *__v = &(__flt)->v[__vtx];        \
                                                                               \
			__filter_attrs_query_##tag[__ai].query(                \
				ADDR_OF(&__v->data),                           \
				__pkts,                                        \
				__slots + __vtx * count,                       \
				count                                          \
			);                                                     \
		}                                                              \
		/* compute inner vertices except root, pushing up to parent */ \
		for (size_t __vtx = __n - 1; __vtx >= 2; --__vtx) {            \
			struct filter_vertex *__v = &(__flt)->v[__vtx];        \
			for (uint32_t idx = 0; idx < count; ++idx) {           \
				uint32_t __c = value_table_get(                \
					&__v->table,                           \
					__slots[(__vtx << 1) * count + idx],   \
					__slots[(__vtx << 1 | 1) * count +     \
						idx]                           \
				);                                             \
				__slots[__vtx * count + idx] = __c;            \
			}                                                      \
		}                                                              \
		/* root (1 when n>1, else 0) */                                \
		const size_t __root = __n > 1;                                 \
		struct filter_vertex *__r = &(__flt)->v[__root];               \
		for (uint32_t idx = 0; idx < count; ++idx) {                   \
			uint32_t __res = value_table_get(                      \
				&__r->table,                                   \
				__root == 0 ? 0                                \
					    : __slots[(__root << 1) * count +  \
						      idx],                    \
				__slots[(__root << 1 | 1) * count + idx]       \
			);                                                     \
			struct value_range *__range =                          \
				ADDR_OF(&__r->registry.ranges) + __res;        \
			(result)[idx] = __range;                               \
		}                                                              \
	})
