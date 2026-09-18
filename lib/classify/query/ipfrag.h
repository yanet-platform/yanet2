#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include "lib/classify/classifiers/ipfrag.h"

static inline void
classify_query_attr_ipfrag_lookup(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)attr_handlers;

	const struct classify_query_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_query_attr_ipfrag, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] =
			value_table_get(&ipfrag_attr->value_table, 0, id);
	}
}

static const struct classify_query_attr_handlers filter_query_ipfrag_handlers =
	{
		.lookup = classify_query_attr_ipfrag_lookup,
};

// Wrapper so CLASSIFY_ATTR_QUERY can reference classify_query_attr_ipfrag via
// its attr_handlers member, matching the other attributes' instance shape.
struct classify_query_attr_ipfrag_handlers {
	struct classify_query_attr_handlers attr_handlers;
};

static const struct classify_query_attr_ipfrag_handlers
	classify_query_attr_ipfrag = {
		.attr_handlers = filter_query_ipfrag_handlers,
};
