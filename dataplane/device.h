#pragma once

#include <stdint.h>

#include "config.h"

#include "worker.h"

struct rte_ether_addr;
struct dataplane;

struct dataplane_device {
	uint32_t device_id;

	uint32_t worker_count;
	struct dataplane_worker *workers;

	uint16_t port_id;
};

int
dataplane_device_start(
	struct dataplane *dataplane, struct dataplane_device *device
);

void
dataplane_device_stop(struct dataplane_device *device);

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
