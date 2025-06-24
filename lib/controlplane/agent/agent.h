#pragma once

#include <stddef.h>

#include <sys/types.h>

#include "common/memory.h"

struct dp_config;
struct cp_config;

struct cp_module;

struct agent;

struct agent {
	struct block_allocator block_allocator;
	struct memory_context memory_context;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	pid_t pid;
	uint64_t memory_limit;
	uint64_t gen;
	uint64_t loaded_module_count;
	uint64_t active_module_count;
	struct agent *prev;
	char name[80];

	uint64_t arena_count;
	void **arenas;

	struct cp_module *unused_module;
};

void
agent_cleanup(struct agent *agent);

struct cp_module;

int
agent_update_modules(
	struct agent *agent, size_t module_count, struct cp_module **cp_modules
);

/*
 * Delete module with specified type and name.
 * Returns error if module is still referenced by some pipeline
 * or module does not exist.
 */
int
agent_delete_module(
	struct agent *agent, const char *module_type, const char *module_name
);

struct pipeline_config;

struct pipeline_config *
pipeline_config_create(const char *name, uint64_t length);

void
pipeline_config_free(struct pipeline_config *config);

void
pipeline_config_set_module(
	struct pipeline_config *config,
	uint64_t index,
	const char *type,
	const char *name
);

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct pipeline_config *pipelines[]
);

int
agent_delete_pipeline(struct agent *agent, const char *pipeline_name);

struct cp_device_config;

struct cp_device_config *
cp_device_config_create(const char *name, uint64_t pipeline_count);

void
cp_device_config_free(struct cp_device_config *config);

int
cp_device_config_add_pipeline(
	struct cp_device_config *config, const char *name, uint64_t weight
);

void
agent_free_unused_modules(struct agent *agent);

// Allows to clean up previous agents which have no loaded modules.
void
agent_free_unused_agents(struct agent *agent);
