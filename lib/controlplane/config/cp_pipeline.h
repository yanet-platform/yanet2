#pragma once

#include <stdio.h>

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "controlplane/config/registry.h"

struct cp_pipeline_function {
	char name[CP_FUNCTION_NAME_LEN];
	uint64_t tsc_counter_id;
};

/*
 * Pipeline descriptor contains length of a pipeline (count in modules)
 * and indexes of modules to be processed inside module registry.
 */
struct cp_pipeline {
	struct registry_item config_item;

	struct counter_registry counter_registry;

	uint64_t counter_packet_in_count;
	uint64_t counter_packet_out_count;
	uint64_t counter_packet_drop_count;
	uint64_t counter_packet_bypass_count;
	uint64_t counter_packet_in_hist;

	char name[CP_PIPELINE_NAME_LEN];

	uint64_t length;
	struct cp_pipeline_function functions[];
};

struct cp_pipeline_config {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t length;
	char functions[][CP_FUNCTION_NAME_LEN];
};

struct dp_config;
struct cp_config_gen;

struct cp_pipeline *
cp_pipeline_create(
	struct memory_context *memory_context,
	struct cp_config_gen *cp_config_gen,
	struct cp_pipeline_config *cp_pipeline_config
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

/*
 * Find index of the pipeline stage associated with provided module.
 * Returns -1 in case module is not referenced.
 */
ssize_t
cp_pipeline_find_module(
	struct cp_config_gen *cp_config_gen,
	struct cp_pipeline *pipeline,
	uint64_t module_type,
	const char *module_name
);
