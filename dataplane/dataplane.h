#pragma once

#include <stdint.h>

#include <pthread.h>

#include "pipeline.h"
#include "worker.h"

#include "module.h"

#include "device.h"

struct dataplane_config {
	struct module_registry module_registry;

	struct pipeline *pipelines;
	uint32_t pipeline_count;
};

struct dataplane {
	struct dataplane_config config;

	struct pipeline phy_pipeline;
	struct pipeline kernel_pipeline;

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
