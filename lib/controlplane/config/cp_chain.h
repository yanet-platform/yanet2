#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "lib/errors/errors.h"

struct cp_chain_module {
	char type[80];
	char name[CP_MODULE_NAME_LEN];
	uint64_t tsc_counter_id;
};

struct cp_chain {
	struct memory_context *memory_context; // shm-relative pointer

	char name[CP_CHAIN_NAME_LEN];

	struct counter_registry counter_registry;

	uint64_t length;
	struct cp_chain_module modules[];
};

/*
 * This is not a module config but module identifier config
 */
struct cp_chain_module_config {
	char type[80];
	char name[80];
};

struct cp_chain_config {
	char name[CP_CHAIN_NAME_LEN];
	uint64_t length;
	struct cp_chain_module_config modules[];
};

struct dp_config;
struct cp_config_gen;

// Allocate a new cp_chain with capacity for length modules.
//
// Returns NULL on allocation failure; caller is responsible for reporting the
// error.
struct cp_chain *
cp_chain_new(struct memory_context *memory_context, uint64_t length);

// Free the memory backing self.
//
// NULL-safe no-op.
//
// Does not call cp_chain_fini: caller must do that separately first.
void
cp_chain_free(struct cp_chain *self);

// Initialize chain resources.
//
// On failure, internally calls cp_chain_fini and returns -1.
int
cp_chain_init(
	struct cp_chain *self,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_chain_config *cp_chain_config,
	yanet_error **err
);

// Tear down resources acquired by cp_chain_init.
//
// Idempotent on zero-init.
void
cp_chain_fini(struct cp_chain *self);
