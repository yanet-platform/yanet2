#pragma once

#include <stdint.h>

#include <sys/types.h>

// FIXME: double declare
#define CP_DEVICE_NAME_LEN 80

struct dp_config;

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
	uint64_t gen;
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
	char name[80];
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

struct cp_device_pipeline_info {
	uint64_t pipeline_idx;
	uint64_t weight;
};

struct cp_device_info {
	uint64_t pipeline_count;
	char name[CP_DEVICE_NAME_LEN];
	struct cp_device_pipeline_info pipelines[];
};

struct cp_device_list_info {
	uint64_t gen;
	uint64_t device_count;
	struct cp_device_info *devices[];
};

void
cp_device_list_info_free(struct cp_device_list_info *device_list_info);

struct cp_device_list_info *
yanet_get_cp_device_list_info(struct dp_config *dp_config);

struct cp_device_info *
yanet_get_cp_device_info(
	struct cp_device_list_info *device_list_info, uint64_t idx
);

struct cp_device_pipeline_info *
yanet_get_cp_device_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
);

struct cp_agent_instance_info {
	pid_t pid;
	uint64_t memory_limit;
	uint64_t allocated;
	uint64_t freed;
	uint64_t gen;
};

struct cp_agent_info {
	char name[80];
	uint64_t instance_count;
	struct cp_agent_instance_info instances[];
};

struct cp_agent_list_info {
	uint64_t count;
	struct cp_agent_info *agents[];
};

int
yanet_get_cp_agent_instance_info(
	struct cp_agent_info *agent_info,
	uint64_t index,
	struct cp_agent_instance_info **instance_info
);

int
yanet_get_cp_agent_info(
	struct cp_agent_list_info *agent_list_info,
	uint64_t index,
	struct cp_agent_info **agent_info
);

void
cp_agent_list_info_free(struct cp_agent_list_info *agent_list_info);

struct cp_agent_list_info *
yanet_get_cp_agent_list_info(struct dp_config *dp_config);
