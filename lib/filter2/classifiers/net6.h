#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/value.h"
#include "lib/filter2/filter.h"

#define FILTER_NET6_ROW_2D 0xffffffffu

struct filter_query_attr_net6 {
	struct filter_query_attr attr;
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
	// Per hi region result: the class for rows uniform across all lo
	// values (the lo lookup is skipped), FILTER_NET6_ROW_2D when the
	// row needs the two dimensional comb lookup.
	uint32_t *row_scalar;
	// Dense row of the comb for the two dimensional regions; the comb
	// itself holds only rows referenced from here.
	uint32_t *row_index;
	// Region count of the hi trie; sizes the row arrays.
	uint32_t row_count;
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
	memory_bfree(
		memory_context,
		ADDR_OF(&net6_attr->row_scalar),
		sizeof(uint32_t) * net6_attr->row_count
	);
	memory_bfree(
		memory_context,
		ADDR_OF(&net6_attr->row_index),
		sizeof(uint32_t) * net6_attr->row_count
	);
	memory_bfree(
		memory_context, net6_attr, sizeof(struct filter_query_attr_net6)
	);
}
