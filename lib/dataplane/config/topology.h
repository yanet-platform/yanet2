#pragma once

#include <stdint.h>

struct dp_port {
	uint16_t port_id;
	char port_name[80];
};

struct dp_topology {
	uint64_t device_count;
	struct dp_port *devices;
};
