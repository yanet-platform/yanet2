#pragma once

#include "common/container_of.h"
#include "common/value.h"

#include "declare.h"
#include "filter/classifiers/device.h"

#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

typedef uint32_t (*packet_get_device_func)(const struct packet *packet);

struct filter_query_attr_device_handlers {
	struct filter_query_attr_handlers attr_handlers;
	packet_get_device_func get_device;
};

static inline void
filter_query_attr_device_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	const struct filter_query_attr_device_handlers *device_handlers =
		container_of(
			attr_handlers,
			struct filter_query_attr_device_handlers,
			attr_handlers
		);

	const struct filter_query_attr_device *device_attr =
		container_of(attr, struct filter_query_attr_device, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		uint32_t device_id = device_handlers->get_device(packets[idx]);
		if (device_id >= device_attr->value_table.h_dim)
			device_id = 0;
		results[idx] = value_table_get(
			&device_attr->value_table, 0, device_id
		);
	}
}

static inline uint32_t
filter_packet_get_device(const struct packet *packet) {
	return packet->module_device_id;
}

static const struct filter_query_attr_handlers filter_query_device_handlers = {
	.lookup = filter_query_attr_device_lookup,
};

static const struct filter_query_attr_device_handlers filter_query_attr_device =
	{
		.attr_handlers = filter_query_device_handlers,
		.get_device = filter_packet_get_device,
};
