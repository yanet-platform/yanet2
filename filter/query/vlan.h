#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(vlan)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct filter_query_attr_proto_range *c =
		(struct filter_query_attr_proto_range *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		uint16_t vlan = packets[idx]->vlan;
		result[idx] = value_table_get(&c->value_table, 0, vlan);
	}
}
