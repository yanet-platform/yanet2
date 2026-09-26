#pragma once

#include "common/value.h"

#include "lib/classify/classify.h"

#include <string.h>

// Query-side attribute for the IP-fragment condition.
//
// Holds the compacted value table mapping the two fragment regions
// (non-fragment / fragment) to their rule-set identifiers.
//
// The struct is embedded by value in a consumer classifier; the table
// dies with the consumer through the free below.
struct classify_attr_ipfrag {
	struct value_table value_table;
};

// Releases the internals of an embedded IP-fragment classifier and
// zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_ipfrag_free(
	struct memory_context *memory_context, struct classify_attr_ipfrag *attr
) {
	(void)memory_context;

	value_table_free(&attr->value_table);
	memset(attr, 0, sizeof(*attr));
}
