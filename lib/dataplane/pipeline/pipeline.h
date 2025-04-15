#pragma once

#include <stdint.h>

#include "dataplane/module/module.h"

#define PIPELINE_NAME_LEN 80

struct dp_config;
struct cp_config_gen;

void
pipeline_process(
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	uint64_t worker_idx,
	uint64_t pipeline_idx,
	struct packet_front *packet_front
);
