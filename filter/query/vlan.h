#pragma once

#include "common/value.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include "filter/classifiers/vlan.h"

typedef uint32_t (*packet_get_vlan_func)(const struct packet *packet);

struct filter_query_attr_vlan_handlers {
	struct filter_query_attr_handlers attr_handlers;
	packet_get_vlan_func get_vlan;
};

static inline void
filter_query_attr_vlan_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	const struct filter_query_attr_vlan_handlers *vlan_handlers =
		container_of(
			attr_handlers,
			struct filter_query_attr_vlan_handlers,
			attr_handlers
		);

	const struct filter_query_attr_vlan *vlan_attr =
		container_of(attr, struct filter_query_attr_vlan, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t vlan_id = vlan_handlers->get_vlan(packets[idx]);
		results[idx] =
			value_table_get(&vlan_attr->value_table, 0, vlan_id);
	}
}

static inline uint32_t
filter_packet_get_vlan(const struct packet *packet) {
	return packet->vlan;
}

static const struct filter_query_attr_handlers filter_query_vlan_handlers = {
	.lookup = filter_query_attr_vlan_lookup,
};

static const struct filter_query_attr_vlan_handlers filter_query_attr_vlan = {
	.attr_handlers = filter_query_vlan_handlers,
	.get_vlan = filter_packet_get_vlan,
};
