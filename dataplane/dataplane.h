#pragma once

#include <pthread.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/dataplane/config/plugin_loader.h"

struct dataplane_config;
struct dataplane_device;

struct dataplane_instance {
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	uint32_t **device_xstat_map;

	// Assigner of published generations to this instance's workers.
	pthread_t config_assigner_thread;
	// Gates the join: false unless a thread was created and not yet
	// reaped.
	bool config_assigner_started;
};

int
dataplane_instance_config_assigner_start(struct dataplane_instance *instance);

void
dataplane_instance_config_assigner_stop(struct dataplane_instance *instance);

#define DATAPLANE_MAX_INSTANCES 8

struct dataplane {
	struct dataplane_instance instances[DATAPLANE_MAX_INSTANCES];
	uint32_t instance_count;

	struct dataplane_device *devices;
	uint32_t device_count;

	struct plugin_registry plugins;
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
