#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(device)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct value_table *t = (struct value_table *)data;
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint64_t device_id = packets[idx]->module_device_id;
		result[idx] = value_table_get(t, 0, device_id);
	}
}
