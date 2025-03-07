#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

struct agent;
struct module_data;

struct agent *
agent_connect(
	const char *storage_name, const char *agent_name, size_t memory_limit
);

int
agent_update_modules(
	struct agent *agent,
	size_t module_count,
	struct module_data **module_datas
);

struct pipeline_config;

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct pipeline_config *pipelines[]
);

struct pipeline_config *
pipeline_config_create(uint64_t length);

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
agent_update_devices(
	struct agent *agent, size_t device_count, uint64_t *pipelines
);

// FIXME: proper name for shared memory handle
struct dp_config;

struct dp_config *
yanet_attach(const char *storage_name);

struct dp_module_info {
	char name[80];
};

struct dp_module_list_info {
	uint64_t module_count;
	struct dp_module_info modules[];
};

void
dp_module_list_info_free(struct dp_module_list_info *module_list_info);

struct dp_module_list_info *
yanet_get_dp_module_list_info(struct dp_config *dp_config);

int
yanet_get_dp_module_info(
	struct dp_module_list_info *module_list,
	uint64_t index,
	struct dp_module_info *module_info
);

struct cp_module_info {
	uint64_t index;
	char config_name[80];
};

struct cp_module_list_info {
	uint64_t gen;
	uint64_t module_count;
	struct cp_module_info modules[];
};

void
cp_module_list_info_free(struct cp_module_list_info *module_list_info);

struct cp_module_list_info *
yanet_get_cp_module_list_info(struct dp_config *dp_config);

int
yanet_get_cp_module_info(
	struct cp_module_list_info *module_list,
	uint64_t index,
	struct cp_module_info *module_info
);

struct cp_pipeline_info {
	uint64_t length;
	uint64_t modules[];
};

struct cp_pipeline_list_info {
	uint64_t count;
	struct cp_pipeline_info *pipelines[];
};

void
cp_pipeline_list_info_free(struct cp_pipeline_list_info *pipeline_list_info);

struct cp_pipeline_list_info *
yanet_get_cp_pipeline_list_info(struct dp_config *dp_config);

int
yanet_get_cp_pipeline_info(
	struct cp_pipeline_list_info *pipeline_list_info,
	uint64_t index,
	struct cp_pipeline_info **pipeline_info
);

int
yanet_get_cp_pipeline_module_info(
	struct cp_pipeline_info *pipeline_info,
	uint64_t index,
	uint64_t *config_index
);
