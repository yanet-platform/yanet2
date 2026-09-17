#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/value.h"
#include "lib/filter2/filter.h"

// Marks a high half region whose row needs the two dimensional join
// table lookup; every other entry of the uniform line below carries the
// row result class directly.
#define FILTER_NET6_ROW_2D 0xffffffffu

struct filter_query_attr_net6 {
	struct filter_query_attr attr;
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
	// Value line over the high half regions: a row of the join table
	// without any low half distinction carries its result class here
	// and the lookup finishes with the single line read; the row of a
	// low half distinction carries the two dimensional mark.
	struct vline uniform;
};

static inline void
filter_query_attr_net6_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_net6 *net6_attr =
		container_of(attr, struct filter_query_attr_net6, attr);

	lpm_free(&net6_attr->hi);
	lpm_free(&net6_attr->lo);
	value_table_free(&net6_attr->comb);
	vline_free(&net6_attr->uniform);

	memory_bfree(
		memory_context, net6_attr, sizeof(struct filter_query_attr_net6)
	);
}
