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
struct module_data;

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

// Gets NUMA node mapping for dataplane.
//
// Returns a bitmap representing available NUMA nodes. Each bit in the returned
// value corresponds to a NUMA node index. For example:
// - 0x1 (bit 0 set) means NUMA node 0 is available
// - 0x3 (bits 0,1 set) means NUMA nodes 0 and 1 are available
//
// @param shm Handle to shared memory segment
//
// @return Bitmap of available NUMA nodes
uint32_t
yanet_shm_numa_map(struct yanet_shm *shm);

// Gets dataplane configuration from shared memory.
//
// Provides access to the dataplane configuration stored in shared memory.
//
// @param shm Handle to shared memory segment
//
// @return Handle to dataplane configuration.
struct dp_config *
yanet_shm_dp_config(struct yanet_shm *shm, uint32_t numa_idx);

// Attaches a module agent to shared memory.
//
// Creates a new agent for a specific module in the given NUMA node.
// The agent provides module-specific operations and memory management.
//
// @param shm Handle to shared memory segment
// @param numa_idx NUMA node index where the agent should operate
// @param agent_name Name of the module agent (e.g. "route", "balancer")
// @param memory_limit Maximum memory limit for this agent
//
// @return Handle to the module agent, NULL on failure
struct agent *
agent_attach(
	struct yanet_shm *shm,
	uint32_t numa_idx,
	const char *agent_name,
	size_t memory_limit
);

// Detaches a module agent from shared memory, releasing associated  resources.
//
// @param agent Handle to the module agent to detach
//
// @return 0 on success, -1 on failure
int
agent_detach(struct agent *agent);

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
