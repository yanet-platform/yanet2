#pragma once

#include <stddef.h>
#include <stdint.h>

struct dataplane_config;
struct dataplane_device;

struct dataplane_numa_node {
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

struct dataplane {
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
	struct dataplane_config *config
);

int
dataplane_start(struct dataplane *dataplane);

int
dataplane_stop(struct dataplane *dataplane);

struct dp_config;
struct cp_config;
struct packet_list;

void
dataplane_route_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct packet_list *packets
);

void
dataplane_drop_packets(
	struct dataplane *dataplane, struct packet_list *packets
);

void
dataplane_log_enable(char *name);
