#pragma once

#include <stdint.h>

#include <pthread.h>

#include "worker.h"

#include "dataplane/config/dataplane_registry.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

#include "device.h"

#include "memory.h"

struct dataplane_numa_node {
	struct block_allocator block_allocator;

	struct dataplane_registry dataplane_registry;

	struct pipeline *phy_pipeline;
	struct pipeline *kernel_pipeline;
};

struct dataplane_config {};

struct dataplane {
	struct dataplane_config config;

	struct dataplane_numa_node nodes[8];
	uint32_t node_count;

	struct dataplane_device *devices;
	uint32_t device_count;
};

int
dataplane_init(
	struct dataplane *dataplane,
	const char *binary,
	size_t device_count,
	const char *const *devices
);

int
dataplane_start(struct dataplane *dataplane);

int
dataplane_stop(struct dataplane *dataplane);

void
dataplane_route_pipeline(
	struct dataplane *dataplane, struct packet_list *packets
);

void
dataplane_drop_packets(
	struct dataplane *dataplane, struct packet_list *packets
);

int
dataplane_register_module(struct dataplane *dataplane, struct module *module);

int
dataplane_configure_module(struct dataplane *dataplane);
