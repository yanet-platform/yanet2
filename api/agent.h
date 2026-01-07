#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "config.h"
#include "counter.h"
#include "info.h"

// Handle to YANET shared memory segment.
struct yanet_shm;
// Handle to dataplane configuration.
struct dp_config;
// TODO: docs.
struct agent;
// TODO: docs.
struct cp_module;

struct cp_device;

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

// Extend agent size.
int
agent_resize(struct agent *agent, size_t new_size);

// Attach a module agent to shared memory,
// use previous agents memory.
struct agent *
agent_reattach(
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

// Returns number of workers in the dataplane instance
//
// @param dp_config Handle to the dataplane instance.
//
// @return Number of workers in the dataplane instance.
uint32_t
dataplane_instance_worker_count(struct dp_config *dp_config);

// Detaches a module agent from shared memory, releasing associated resources.
//
// @param agent Handle to the module agent to detach
//
// @return 0 on success, -1 on failure
int
agent_detach(struct agent *agent);

// Gets the dataplane configuration associated with an agent.
//
// Returns the dataplane configuration handle for the dataplane instance
// that this agent is attached to. This provides access to dataplane-level
// settings and state.
//
// @param agent Handle to the module agent
//
// @return Handle to the dataplane configuration
struct dp_config *
agent_dp_config(struct agent *agent);

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

int
agent_update_functions(
	struct agent *agent,
	uint64_t function_count,
	struct cp_function_config *functions[]
);

int
agent_delete_function(struct agent *agent, const char *function_name);

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct cp_pipeline_config *pipelines[]
);

// Delete pipeline with specified name.
// @return -1 if pipeline not exists or is assigned to some device, 0 on
// success.
int
agent_delete_pipeline(struct agent *agent, const char *pipeline_name);

int
agent_update_devices(
	struct agent *agent, uint64_t device_count, struct cp_device *devices[]
);

// Retrieves and clears the last error from the agent's diagnostic system.
// Transfers ownership of the error message to the caller.
//
// @param agent Handle to the module agent
// @return Heap-allocated error message string that must be freed by the caller,
//         or NULL if no error occurred. Sets errno=ENOMEM if memory allocation
//         failed while capturing the error.
//
// Note: The caller MUST free the returned string when it's not NULL.
//       After this call, the agent's error state is cleared.
const char *
agent_take_error(struct agent *agent);

// Clears any error stored in the agent's diagnostic system without retrieving
// it. This discards the error message and resets the diagnostic state.
//
// @param agent Handle to the module agent
//
// Note: Use this when you want to discard an error without processing it.
//       Use agent_take_error() if you need to retrieve the error message.
void
agent_clean_error(struct agent *agent);

void *
agent_storage_read(struct agent *agent, const char *name);

int
agent_storage_put(
	struct agent *agent, const char *name, void *data, size_t size
);
