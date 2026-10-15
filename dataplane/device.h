#pragma once

#include <stdint.h>

#include "config.h"

struct rte_ether_addr;
struct dataplane;
struct dataplane_worker;

struct dataplane_device {
	uint32_t device_id;

	// The device's RX/TX queue count: one per assignment referencing the
	// device, across all workers. The workers themselves live in the
	// dataplane's flat worker list.
	uint32_t queue_count;

	char port_name[80];

	uint16_t port_id;
	uint32_t xstat_count;
};

int
dataplane_device_start(
	struct dataplane *dataplane, struct dataplane_device *device
);

int
dataplane_device_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	uint32_t device_id,
	struct dataplane_device_config *config
);

int
dataplane_device_get_mac(
	struct dataplane_device *device, struct rte_ether_addr *ether_addr
);
