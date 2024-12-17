#pragma once

#include <stdint.h>

#include "rte_ether.h"

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
dataplane_dpdk_port_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	uint32_t device_id,
	const char *name,
	uint16_t worker_count,
	uint16_t numa_id
);

int
dataplane_dpdk_port_get_mac(
	struct dataplane_device *device, struct rte_ether_addr *ether_addr
);
