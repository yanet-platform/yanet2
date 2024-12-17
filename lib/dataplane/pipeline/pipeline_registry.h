#pragma once

#include <stdint.h>
#include <stdlib.h>

struct pipeline;

struct pipeline_registry {
	struct pipeline **pipelines;
	uint32_t pipeline_count;
};

static inline int
pipeline_registry_init(struct pipeline_registry *registry) {
	registry->pipelines = NULL;
	registry->pipeline_count = 0;
	return 0;
}

int
pipeline_registry_register(
	struct pipeline_registry *pipeline_registry, struct pipeline *pipeline
);

struct pipeline *
pipeline_registry_lookup(
	struct pipeline_registry *pipeline_registry, const char *pipeline_name
);
