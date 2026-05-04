#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "filter/filter.h"

struct filter_query_attr_net4 {
	struct filter_query_attr attr;
	struct lpm lpm;
};

static inline void
filter_query_attr_net4_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_net4 *net4_attr =
		container_of(attr, struct filter_query_attr_net4, attr);

	lpm_free(&net4_attr->lpm);
	memory_bfree(
		memory_context, net4_attr, sizeof(struct filter_query_attr_net4)
	);
}
