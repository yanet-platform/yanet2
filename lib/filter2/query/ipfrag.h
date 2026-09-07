#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include "lib/filter2/classifiers/ipfrag.h"

static inline void
filter_query_attr_ip_frag_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)attr_handlers;

	const struct filter_query_attr_ip_frag *ipfrag_attr =
		container_of(attr, struct filter_query_attr_ip_frag, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] =
			value_table_get(&ipfrag_attr->value_table, 0, id);
	}
}

static const struct filter_query_attr_handlers filter_query_ipfrag_handlers = {
	.lookup = filter_query_attr_ip_frag_lookup,
};

// Wrapper so FILTER_ATTR_QUERY can reference filter_query_attr_ip_frag via its
// attr_handlers member, matching the other attributes' instance shape.
struct filter_query_attr_ip_frag_handlers {
	struct filter_query_attr_handlers attr_handlers;
};

static const struct filter_query_attr_ip_frag_handlers filter_query_attr_ip_frag =
	{
		.attr_handlers = filter_query_ipfrag_handlers,
};
