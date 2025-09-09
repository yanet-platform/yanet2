#pragma once

#include <stdint.h>

#include "dataplane/module/module.h"

struct dp_config;
struct dp_worker;
struct packet_front;
struct cp_config_gen;
struct pipeline_ectx;

void
pipeline_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	uint64_t pipeline_idx,
	struct packet_front *packet_front
);

void
pipeline_ectx_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
);
