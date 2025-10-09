#pragma once

#include <stdint.h>

#include "dataplane/module/module.h"

struct dp_config;
struct dp_worker;
struct packet_front;
struct cp_config_gen;

struct pipeline_ectx;
struct device_ectx;

void
pipeline_ectx_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
);

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front,
	struct packet *packet
);

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front,
	struct packet *packet
);
