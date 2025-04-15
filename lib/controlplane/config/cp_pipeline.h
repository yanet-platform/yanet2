#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/registry.h"

#define CP_PIPELINE_NAME_LEN 80

struct counter_storage;

struct cp_pipeline_module {
	uint64_t index;
	struct counter_storage *counter_storage;
	uint64_t counter_id;
};

/*
 * Pipeline descriptor contains length of a pipeline (count in modules)
 * and indexes of modules to be processed inside module registry.
 */
struct cp_pipeline {
	struct registry_item config_item;
	struct counter_registry counter_registry;
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t length;
	struct cp_pipeline_module modules[];
};

struct module_config {
	char type[80];
	char name[80];
};

struct pipeline_config {
	char name[80];
	uint64_t length;
	struct module_config modules[0];
};

struct dp_config;
struct cp_config_gen;

struct cp_pipeline *
cp_pipeline_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct pipeline_config *pipeline_config
);

void
cp_pipeline_free(
	struct memory_context *memory_context, struct cp_pipeline *pipeline
);

/*
 * Pipeline registry contains all existing pipelines.
 * After reading a packet a dataplane worker evaluates index of a
 * pipeline assigned to process the packet and fetchs pipeline descriptor
 * from the pipeline registry insdide active configuration generation.
 */

struct cp_pipeline_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_pipeline_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_registry *registry
);

int
cp_pipeline_registry_copy(
	struct memory_context *memory_context,
	struct cp_pipeline_registry *new_pipeline_registry,
	struct cp_pipeline_registry *old_pipeline_registry
);

void
cp_pipeline_registry_destroy(struct cp_pipeline_registry *pipeline_registry);

struct cp_pipeline *
cp_pipeline_registry_get(
	struct cp_pipeline_registry *pipeline_registry, uint64_t idx
);

int
cp_pipeline_registry_lookup_index(
	struct cp_pipeline_registry *pipeline_registry,
	const char *name,
	uint64_t *index
);

struct cp_pipeline *
cp_pipeline_registry_lookup(
	struct cp_pipeline_registry *pipeline_registry, const char *name
);

int
cp_pipeline_registry_upsert(
	struct cp_pipeline_registry *pipeline_registry,
	const char *name,
	struct cp_pipeline *pipeline
);

int
cp_pipeline_registry_delete(
	struct cp_pipeline_registry *pipeline_registry, const char *name
);

static inline uint64_t
cp_pipeline_registry_capacity(struct cp_pipeline_registry *pipeline_registry) {
	return pipeline_registry->registry.capacity;
}
