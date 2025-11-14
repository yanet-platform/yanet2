#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"
#include "controlplane/config/registry.h"

struct cp_chain;

struct cp_function_chain {
	struct cp_chain *cp_chain;
	uint64_t weight;
};

/*
 * Pipeline descriptor contains length of a function (count in modules)
 * and indexes of modules to be processed inside module registry.
 */
struct cp_function {
	struct registry_item config_item;

	struct counter_registry counter_registry;

	uint64_t counter_packet_in_count;
	uint64_t counter_packet_out_count;
	uint64_t counter_packet_drop_count;
	uint64_t counter_packet_in_hist;

	char name[CP_PIPELINE_NAME_LEN];

	uint64_t chain_count;
	struct cp_function_chain chains[];
};

struct cp_chain_config;

struct cp_function_chain_config {
	struct cp_chain_config *chain;
	uint64_t weight;
};

struct cp_function_config {
	char name[CP_FUNCTION_NAME_LEN];
	uint64_t chain_count;
	struct cp_function_chain_config chains[];
};

struct dp_config;
struct cp_config_gen;

struct cp_function *
cp_function_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_function_config *cp_function_config
);

void
cp_function_free(
	struct memory_context *memory_context, struct cp_function *cp_function
);

/*
 * Pipeline registry contains all existing functions.
 * After reading a packet a dataplane worker evaluates index of a
 * function assigned to process the packet and fetchs function descriptor
 * from the function registry insdide active configuration generation.
 */

struct cp_function_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_function_registry_init(
	struct memory_context *memory_context,
	struct cp_function_registry *registry
);

int
cp_function_registry_copy(
	struct memory_context *memory_context,
	struct cp_function_registry *new_function_registry,
	struct cp_function_registry *old_function_registry
);

void
cp_function_registry_destroy(struct cp_function_registry *function_registry);

struct cp_function *
cp_function_registry_get(
	struct cp_function_registry *function_registry, uint64_t idx
);

int
cp_function_registry_lookup_index(
	struct cp_function_registry *function_registry,
	const char *name,
	uint64_t *index
);

struct cp_function *
cp_function_registry_lookup(
	struct cp_function_registry *function_registry, const char *name
);

int
cp_function_registry_upsert(
	struct cp_function_registry *function_registry,
	const char *name,
	struct cp_function *function
);

int
cp_function_registry_delete(
	struct cp_function_registry *function_registry, const char *name
);

static inline uint64_t
cp_function_registry_capacity(struct cp_function_registry *function_registry) {
	return function_registry->registry.capacity;
}
