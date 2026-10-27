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
	// How many of those queues are in the RSS table: all of them unless
	// some are steering queues.
	uint32_t rss_queue_count;

	char port_name[80];

	uint16_t port_id;
	uint32_t xstat_count;

	uint64_t rss_hash;
	// Per queue, the traffic the NIC delivers to it. NULL when the port
	// keeps the default: every queue in the RSS table, nothing steered.
	struct dataplane_queue_role *queue_roles;
};

// Whether a device queue is a steering queue: one left out of the RSS
// table, receiving only the classes steered to it.
static inline int
dataplane_device_queue_is_steering(
	const struct dataplane_device *device, uint32_t queue_id
) {
	return device->queue_roles != NULL &&
	       !device->queue_roles[queue_id].rss;
}

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
