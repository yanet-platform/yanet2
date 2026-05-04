#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/value.h"
#include "filter/filter.h"

struct filter_query_attr_net6 {
	struct filter_query_attr attr;
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
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
		memory_context, net6_attr, sizeof(struct filter_query_attr_net6)
	);
}
