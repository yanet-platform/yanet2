#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/value.h"
#include "lib/classify/classify.h"

// Marks a high half region whose row needs the two dimensional lookup:
// the high half trie value carries the final result class directly for
// every other region, a marked value carries the dense row of the join
// table behind the mark. The trie stores its values shifted left by
// one, so the mark lives in the top value bit the trie preserves and
// the classes and dense rows stay below it.
#define FILTER_NET6_ROW_MARK 0x40000000u

struct classify_query_attr_net6 {
	struct classify_query_attr attr;
	// The high half trie value is the final result class, or the dense
	// join table row behind FILTER_NET6_ROW_MARK; the table itself
	// holds only the rows referenced from the marked values.
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
};

static inline void
classify_query_attr_net6_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_net6 *net6_attr =
		container_of(attr, struct classify_query_attr_net6, attr);

	lpm_free(&net6_attr->hi);
	lpm_free(&net6_attr->lo);
	value_table_free(&net6_attr->comb);

	memory_bfree(
		memory_context,
		net6_attr,
		sizeof(struct classify_query_attr_net6)
	);
}
