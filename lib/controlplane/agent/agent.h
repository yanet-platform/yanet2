#pragma once

#include <stdbool.h>
#include <stddef.h>

#include <sys/types.h>

#include "common/memory.h"

#include "lib/errors/errors.h"

struct dp_config;
struct cp_config;

struct cp_module;
struct cp_device;
struct cp_object;

struct registry;
struct registry_item;

struct agent;

// Longest agent name the registry can hold, terminator included.
#define AGENT_NAME_LEN 80

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
	// Count of live generation references to this agent's modules,
	// devices and objects.
	//
	// Those entities carry no creator reference: their reference
	// counts, and this mirror of them, count only registry references
	// from live configuration generations, which can outlive the
	// creating process. An item whose count reaches zero is dangling —
	// known only to its owner — and its memory returns either through
	// the owner's typed destroy or, if the owner is gone, with this
	// agent's wholesale reclaim.
	//
	// The count moves with relaxed atomics, so generation teardown may
	// drop references without the config lock. A racing drop unseen by
	// a reclaim decision can only postpone that agent's reclamation,
	// and gains never race reclamation the other way: updates through
	// a superseded agent are refused, a copy gain mirrors a reference
	// the source generation already holds, and the one zero-to-one gain
	// on a predecessor — a destroy gate's destruction hold — takes the
	// agent lock, which the sweep's zero check therefore cannot miss,
	// so the sweep may run under the agent lock alone.
	// Reclaiming a superseded agent waits for it to reach zero, because
	// freeing the arena is one wholesale operation with no way to tear
	// the loaded entities down individually first.
	uint64_t loaded_entity_count;
	struct agent *prev;
	// Set once a newer generation of this agent's name replaces it
	// in the registry.
	//
	// From that moment the agent exists for nothing but reclamation,
	// and its arenas are freed wholesale once the loaded count above
	// drains to zero. The store runs at the superseding attach under
	// the configuration and agent locks, before any sweep those locks
	// admit; readers pair it with an acquire load, so a refusal is
	// ordered after the splice that ended the agent's writer life.
	bool superseded;
	char name[AGENT_NAME_LEN];

	uint64_t arena_count;
	struct agent_arena *arenas;

	struct agent_storage *storage;
};

struct dp_config *
agent_dp_config(struct agent *agent);

// Refuse a storage write through a superseded agent generation.
//
// A superseded generation's only purpose is to be reclaimed once its
// loaded count reaches zero: a value a caller would still store
// through it would race the sweep's wholesale free of the arenas or
// outlive every handle the caller holds, so the write fails fast
// instead.
//
// An agent that never enters the registry, like the dataplane's own,
// is never replaced and always passes.
//
// @return 0 while no newer generation has replaced the agent, -1 with
// a failed-precondition error once one has.
static inline int
agent_reject_superseded(struct agent *agent, yanet_error **err) {
	if (!__atomic_load_n(&agent->superseded, __ATOMIC_ACQUIRE)) {
		return 0;
	}

	yanet_error_add_kind(
		err,
		YANET_ERROR_FAILED_PRECONDITION,
		"agent \"%s\" has been superseded by a newer generation",
		agent->name
	);
	return -1;
}

void
agent_cleanup(struct agent *agent);

struct cp_module;

// Drop the registry references named by the array, one subtraction of
// the total per distinct agent.
//
// Every pointer in the array is read — never dereferenced — after the
// first subtraction runs, so an agent whose count reaches zero may be
// reclaimed by a concurrent sweep midway through the batch. Callers
// must therefore finish every dereference of agent-owned storage before
// calling this.
void
agent_unload_references(struct agent **owners, uint64_t count);

// Return one destruction hold, as taken by a destroy gate that
// observed the item unreferenced.
//
// The hold keeps the agent reclaimable-safe while a caller runs a typed
// destructor unlocked; capture the agent pointer before the destructor
// frees the item it came from.
void
agent_destroy_released(struct agent *agent);

// Release every item of a retiring registry and mirror the drops into
// the owners' loaded count, in bounded allocation-free batches.
//
// The fallback for a registry teardown whose one-shot owners snapshot
// could not be allocated; item_owner maps one registry item to the
// agent that owns it. Each batch finishes every dereference of
// agent-owned storage before its loaded count drops, so an owner whose
// count reaches zero has no reference left anywhere and nothing later
// in the release can touch reclaimed storage.
void
agent_release_registry_items(
	struct registry *registry,
	struct agent *(*item_owner)(struct registry_item *item)
);

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

// Allows to clean up previous agents which have no loaded modules,
// devices or objects.
void
agent_free_unused_agents(struct agent *agent);
