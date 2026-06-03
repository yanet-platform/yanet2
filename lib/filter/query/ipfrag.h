#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(ip_frag)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct value_table *t = (struct value_table *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		result[idx] = value_table_get(t, 0, id);
	}
}
