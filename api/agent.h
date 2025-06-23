#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Handle to YANET shared memory segment.
struct yanet_shm;
// Handle to dataplane configuration.
struct dp_config;
// TODO: docs.
struct agent;
// TODO: docs.
struct cp_module;

// Attaches to YANET shared memory segment.
//
// This is the primary entry point for accessing YANET's shared memory. The
// shared memory segment contains both dataplane configuration and
// module-specific data.
//
// Once attached, the returned handle can be used to:
// - Access dataplane configuration.
// - Get module configurations.
// - Allocate memory for new modules.
//
// @param path Path to the shared memory file (e.g. "/dev/hugepages/yanet").
//
// @return Handle to the shared memory segment on success.
//         On failure, the function return NULL and set errno to indicate the
//         error.
//         The caller is responsible for detaching the handle using
//         yanet_shm_detach().
struct yanet_shm *
yanet_shm_attach(const char *path);

// Detaches from YANET shared memory segment.
//
// Releases all resources associated with the shared memory handle.
// After this call, the handle becomes invalid and must not be used.
//
// @param shm Handle to shared memory segment obtained from yanet_shm_attach()
int
yanet_shm_detach(struct yanet_shm *shm);

// Gets configuration of dataplane instance from shared memory.
//
// Provides access to the dataplane instance configuration stored in shared
// memory.
//
// @param shm Handle to shared memory segment
// @param instance_idx Index of the dataplane instance
//
// @return Handle to dataplane configuration.
struct dp_config *
yanet_shm_dp_config(struct yanet_shm *shm, uint32_t instance_idx);

// Attaches a module agent to shared memory.
//
// Creates a new agent for a specific module in the given dataplane instance.
// The agent provides module-specific operations and memory management.
//
// @param shm Handle to shared memory segment
// @param instance_idx Index of the dataplane instance where the agent should
// operate
// @param agent_name Name of the module agent (e.g. "route", "balancer")
// @param memory_limit Maximum memory limit for this agent
//
// @return Handle to the module agent, NULL on failure
struct agent *
agent_attach(
	struct yanet_shm *shm,
	uint32_t instance_idx,
	const char *agent_name,
	size_t memory_limit
);

// Returns number of dataplane instances in the specified shared memory segment.
//
// @param shm Handle to the shared memory segment.
//
// @return Number of dataplane instances in the specified shared memory segment.
uint32_t
yanet_shm_instance_count(struct yanet_shm *shm);

// Returns index of numa node dataplane instance attached to
//
// @param dp_config Handle to the dataplane instance.
//
// @return Index of numa node dataplane instance attached to.
uint32_t
dataplane_instance_numa_idx(struct dp_config *dp_config);

// Detaches a module agent from shared memory, releasing associated resources.
//
// @param agent Handle to the module agent to detach
//
// @return 0 on success, -1 on failure
int
agent_detach(struct agent *agent);

int
agent_update_modules(
	struct agent *agent, size_t module_count, struct cp_module **cp_modules
);

// Delete module with specified type and name.
//
// @return -1 if module is still referenced by some pipeline or module does not
// exist, 0 on success.
int
agent_delete_module(
	struct agent *agent, const char *module_type, const char *module_name
);

struct pipeline_config;

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct pipeline_config *pipelines[]
);

// Delete pipeline with specified name.
// @return -1 if pipeline not exists or is assigned to some device, 0 on
// success.
int
agent_delete_pipeline(struct agent *agent, const char *pipeline_name);

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

struct cp_device_config;

int
agent_update_devices(
	struct agent *agent,
	uint64_t device_count,
	struct cp_device_config *devices[]
);

struct cp_device_config *
cp_device_config_create(const char *name, uint64_t pipeline_count);

void
cp_device_config_free(struct cp_device_config *config);

int
cp_device_config_add_pipeline(
	struct cp_device_config *config, const char *name, uint64_t weight
);

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

struct counter_value_handle;

struct counter_handle {
	char name[60];
	uint64_t size;
	uint64_t gen;
	struct counter_value_handle *value_handle;
};

struct counter_handle_list {
	uint64_t instance_count;
	uint64_t count;
	struct counter_handle counters[];
};

struct counter_handle_list *
yanet_get_pm_counters(
	struct dp_config *dp_config,
	const char *module_type,
	const char *module_name,
	const char *pipeline_name
);

struct counter_handle_list *
yanet_get_pipeline_counters(
	struct dp_config *dp_config, const char *pipeline_name
);

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config);

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx);

uint64_t
yanet_get_counter_value(
	struct counter_value_handle *value_handle,
	uint64_t value_idx,
	uint64_t worker_idx
);

void
yanet_counter_handle_list_free(struct counter_handle_list *counters);
