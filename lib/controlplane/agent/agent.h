#pragma once

#include <stddef.h>

#include <sys/types.h>

#include "common/memory.h"

#include "lib/errors/errors.h"

struct dp_config;
struct cp_config;

struct cp_module;
struct cp_device;
struct cp_object;

struct agent;

// Process-local handle to a mapped shared-memory segment.
//
// Carries the mapping length next to its base address so that releasing
// the segment never depends on what the segment header says: the dataplane
// sizes the storage file long before it fills the header in, and a detach
// in that window must still unmap exactly what was mapped. A harness that
// backs the segment with its own memory embeds this struct and hands out a
// pointer to it; such a handle must never be detached.
struct yanet_shm {
	void *base;
	size_t size;
};

struct agent_arena {
	void *data;
	uint64_t size;
};

struct agent_storage {
	char name[80];
	struct agent_storage *next;
	size_t size;
	uint8_t data[];
};

struct agent {
	struct block_allocator block_allocator;
	struct memory_context memory_context;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	pid_t pid;
	uint64_t memory_limit;
	uint64_t gen;
	// Count of live generation references to this agent's modules.
	//
	// Excludes the initial reference a module construction call holds for
	// its own creator, since a creator that dies never releases it and
	// counting it would block reclaim forever. Every place that adds or
	// removes such a reference keeps this count in step, so it reflects
	// only references that can outlive the creator.
	uint64_t loaded_module_count;
	uint64_t loaded_device_count;
	// Count of this agent's shared objects that still hold a reference
	// from a live generation.
	//
	// Follows the same accounting as the device count above: incremented
	// when a reference is added and decremented when it is dropped and
	// the object retires. Reclaiming a superseded agent waits for this
	// and the sibling counts to reach zero, because freeing the arena is
	// one wholesale operation with no way to tear objects down
	// individually first.
	uint64_t loaded_object_count;
	// Count of this agent's parked-module teardowns currently running
	// outside the configuration lock.
	//
	// Set before the lock is released to run a batch of parked
	// destructors and cleared once they return. The reclaim guard treats
	// a nonzero count as a live reference, because those destructors
	// still touch this agent's arena even though the generation counts
	// above already read zero for them. A process that dies mid-teardown
	// leaves this above zero forever, leaking that one superseded arena
	// rather than risking a destructor running against memory that has
	// already been freed.
	uint64_t parked_teardown_count;
	struct agent *prev;
	char name[80];

	uint64_t arena_count;
	struct agent_arena *arenas;

	struct agent_storage *storage;

	// Head of this agent's list of modules parked after their reference
	// count reached zero.
	//
	// Parked entries await destruction until the next construction call
	// for their module type reclaims them. A control plane can reuse one
	// agent across more than one service, so this list may mix module
	// types. Each type's construction reclaims only matching entries,
	// leaving the rest parked for their own type's turn.
	struct cp_module *parked_modules;

	// Head of this agent's list of devices parked after their reference
	// count reached zero.
	//
	// Follows the same discipline as the module list above: a device type's
	// construction call reclaims only matching entries, so a list shared by
	// plain and vlan devices keeps each type's entries for that type's own
	// next construction.
	struct cp_device *parked_devices;
};

struct dp_config *
agent_dp_config(struct agent *agent);

void
agent_cleanup(struct agent *agent);

struct cp_module;

int
agent_update_modules(
	struct agent *agent,
	size_t module_count,
	struct cp_module **cp_modules,
	yanet_error **err
);

/*
 * Delete module with specified type and name.
 * Returns error if module is still referenced by some pipeline
 * or module does not exist.
 */
int
agent_delete_module(
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
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
	struct cp_function_config *functions[],
	yanet_error **err
);

int
agent_delete_function(
	struct agent *agent, const char *function_name, yanet_error **err
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
	struct cp_pipeline_config *pipelines[],
	yanet_error **err
);

int
agent_delete_pipeline(
	struct agent *agent, const char *pipeline_name, yanet_error **err
);

struct cp_device;

int
agent_update_devices(
	struct agent *agent,
	uint64_t device_count,
	struct cp_device *devices[],
	yanet_error **err
);

// Delete device with specified name.
//
// @return -1 if device does not exist or is a predefined topology
// device, 0 on success.
int
agent_delete_device(
	struct agent *agent, const char *device_name, yanet_error **err
);

int
agent_update_objects(
	struct agent *agent,
	uint64_t object_count,
	struct cp_object *objects[],
	yanet_error **err
);

int
agent_delete_object(
	struct agent *agent,
	const char *object_type,
	const char *object_name,
	yanet_error **err
);

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

// Allows to clean up previous agents which have no loaded modules or devices.
void
agent_free_unused_agents(struct agent *agent);
