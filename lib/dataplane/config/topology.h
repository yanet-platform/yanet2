#pragma once

#include <stddef.h>
#include <stdint.h>

struct dp_port {
	uint16_t port_id;
	char port_name[80];
};

struct dp_topology {
	uint64_t device_count;
	struct dp_port *devices;
};

struct dp_config;

// Allocate the dp_topology.devices array of count slots and wire it into
// dp_config. The slots are left uninitialised — the caller fills each
// entry's fields. Returns NULL on out-of-memory.
struct dp_port *
dp_topology_alloc_devices(struct dp_config *dp_config, size_t count);
