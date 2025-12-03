#pragma once

#include <stddef.h>

#include <sys/types.h>

#include "common/memory.h"

#include "lib/controlplane/diag/diag.h"

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

	struct diag diag;
};

struct dp_config *
agent_dp_config(struct agent *agent);

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

struct cp_chain_config *
cp_chain_config_create(
	const char *name,
	uint64_t length,
	const char *const *types,
	const char *const *names
);

void
cp_chain_config_free(struct cp_chain_config *cp_chain_config);

struct cp_function_config;

struct cp_function_config *
cp_function_config_create(const char *name, uint64_t chain_count);

void
cp_function_config_free(struct cp_function_config *config);

int
cp_function_config_set_chain(
	struct cp_function_config *cp_function_config,
	uint64_t index,
	struct cp_chain_config *cp_chain_config,
	uint64_t weight
);

int
agent_update_functions(
	struct agent *agent,
	uint64_t function_count,
	struct cp_function_config *functions[]
);

struct cp_pipeline_config;

struct cp_pipeline_config *
cp_pipeline_config_create(const char *name, uint64_t length);

void
cp_pipeline_config_free(struct cp_pipeline_config *config);

int
cp_pipeline_config_set_function(
	struct cp_pipeline_config *config, uint64_t index, const char *name
);

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct cp_pipeline_config *pipelines[]
);

int
agent_delete_pipeline(struct agent *agent, const char *pipeline_name);

struct cp_device_config;

struct cp_device_config *
cp_device_config_create(
	const char *name,
	uint64_t input_pipeline_count,
	uint64_t output_pipeline_count
);

void
cp_device_config_free(struct cp_device_config *config);

int
cp_device_config_set_input_pipeline(
	struct cp_device_config *config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

int
cp_device_config_set_output_pipeline(
	struct cp_device_config *config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

// Allows to clean up previous agents which have no loaded modules.
void
agent_free_unused_agents(struct agent *agent);
