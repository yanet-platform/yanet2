#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(vlan)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct vline *l = (struct vline *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		uint16_t vlan = packets[idx]->vlan;
		result[idx] = vline_get(l, vlan);
	}
}
