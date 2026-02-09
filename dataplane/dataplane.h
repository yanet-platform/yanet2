#pragma once

#include <stddef.h>
#include <stdint.h>

struct dataplane_config;
struct dataplane_device;

struct dataplane_instance {
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

#define DATAPLANE_MAX_INSTANCES 8

struct dataplane {
	struct dataplane_instance instances[DATAPLANE_MAX_INSTANCES];
	uint32_t instance_count;

	struct dataplane_device *devices;
	uint32_t device_count;
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
struct cp_config_gen;
struct packet_list;

void
dataplane_route_pipeline(
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct packet_list *packets
);

void
dataplane_drop_packets(
	struct dataplane *dataplane, struct packet_list *packets
);
