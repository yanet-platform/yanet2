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
	uint32_t slots[2 * MAX_ATTRIBUTES * packet_count + 1];
	/* compute classifiers for leaf attributes into parent slots
	 */
	for (size_t leaf = 0; leaf < filter_query->lookup_count; ++leaf) {
		size_t vertex = filter_query->lookup_count + leaf;
		const struct filter_vertex *vertex_ptr = &(filter)->v[vertex];
		filter_query->lookups[leaf](
			ADDR_OF(&vertex_ptr->data),
			packets,
			slots + vertex * packet_count,
			packet_count
		);
	}
	/* compute inner vertices except root, pushing up to parent */
	for (size_t vertex = filter_query->lookup_count - 1; vertex >= 2;
	     --vertex) {
		struct filter_vertex *vertex_ptr = &(filter)->v[vertex];
		for (uint32_t idx = 0; idx < packet_count; ++idx) {
			uint32_t classified = value_table_get(
				&vertex_ptr->table,
				slots[(vertex << 1) * packet_count + idx],
				slots[(vertex << 1 | 1) * packet_count + idx]
			);
			slots[vertex * packet_count + idx] = classified;
		}
	}
	/* root (1 when n>1, else 0) */
	const size_t root = filter_query->lookup_count > 1;
	struct filter_vertex *root_ptr = &(filter)->v[root];
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		uint32_t result = value_table_get(
			&root_ptr->table,
			root == 0 ? 0 : slots[(root << 1) * packet_count + idx],
			slots[(root << 1 | 1) * packet_count + idx]
		);
		(results)[idx] = result;
	}
}
