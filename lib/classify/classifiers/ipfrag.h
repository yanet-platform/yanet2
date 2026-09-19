#pragma once

#include "common/container_of.h"

#include "lib/classify/classify.h"

// Query-side attribute for the IP-fragment condition.
//
// Holds the compacted value table mapping the two fragment regions
// (non-fragment / fragment) to their rule-set identifiers.
struct classify_query_attr_ipfrag {
	struct classify_query_attr attr;
	struct value_table value_table;
};

static inline void
classify_query_attr_ipfrag_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_query_attr_ipfrag, attr);

	value_table_free(&ipfrag_attr->value_table);
	memory_bfree(
		memory_context,
		ipfrag_attr,
		sizeof(struct classify_query_attr_ipfrag)
	);
}
