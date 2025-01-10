#pragma once

#include <stdint.h>

#include <pthread.h>

#include "worker.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

#include "device.h"

#include "memory.h"

struct dataplane_numa_node {
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

struct dataplane_config {};

struct dataplane {
	struct dataplane_config config;

	struct dataplane_numa_node nodes[8];
	uint32_t node_count;

	struct dataplane_device *devices;
	uint32_t device_count;

	uint64_t read;
	uint64_t write;
	uint64_t drop;
};

int
dataplane_init(
	struct dataplane *dataplane,
	const char *binary,

	const char *storage,

	size_t numa_count,
	size_t dp_memory,
	size_t cp_memory,

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
