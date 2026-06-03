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

#include <stdint.h>

#include "filter.h"
#include "query/attribute.h"
#include "rule.h"

////////////////////////////////////////////////////////////////////////////////

typedef void (*filter_lookup_query_func)(
	void *data,
	struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
);

struct filter_query {
	uint64_t lookup_count;
	filter_lookup_query_func *lookups;
};

static inline void
filter_query(
	struct filter *filter,
	const struct filter_query *filter_query,
	struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	/* Local slots storage */
	uint32_t __slots[2 * MAX_ATTRIBUTES * packet_count + 1];
	/* compute classifiers for leaf attributes into parent slots
	 */
	for (size_t __ai = 0; __ai < filter_query->lookup_count; ++__ai) {
		size_t __vtx = filter_query->lookup_count + __ai;
		const struct filter_vertex *__v = &(filter)->v[__vtx];
		filter_query->lookups[__ai](
			ADDR_OF(&__v->data),
			packets,
			__slots + __vtx * packet_count,
			packet_count
		);
	}
	/* compute inner vertices except root, pushing up to parent */
	for (size_t __vtx = filter_query->lookup_count - 1; __vtx >= 2;
	     --__vtx) {
		struct filter_vertex *__v = &(filter)->v[__vtx];
		for (uint32_t idx = 0; idx < packet_count; ++idx) {
			uint32_t __c = value_table_get(
				&__v->table,
				__slots[(__vtx << 1) * packet_count + idx],
				__slots[(__vtx << 1 | 1) * packet_count + idx]
			);
			__slots[__vtx * packet_count + idx] = __c;
		}
	}
	/* root (1 when n>1, else 0) */
	const size_t __root = filter_query->lookup_count > 1;
	struct filter_vertex *__r = &(filter)->v[__root];
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		uint32_t __res = value_table_get(
			&__r->table,
			__root == 0
				? 0
				: __slots[(__root << 1) * packet_count + idx],
			__slots[(__root << 1 | 1) * packet_count + idx]
		);
		(results)[idx] = __res;
	}
}
