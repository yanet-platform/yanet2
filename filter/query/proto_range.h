#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(proto_range)(struct packet *packet, void *data) {
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	uint16_t proto = packet->transport_header.type * 256;
	return value_table_get(&c->table, 0, proto);
}