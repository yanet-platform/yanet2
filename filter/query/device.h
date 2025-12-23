#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(device)(struct packet *packet, void *data) {
	struct value_table *t = (struct value_table *)data;
	uint64_t device_id = packet->module_device_id;
	return value_table_get(t, 0, device_id);
}