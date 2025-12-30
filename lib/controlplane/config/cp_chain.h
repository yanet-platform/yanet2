#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

struct cp_chain_module {
	char type[80];
	char name[CP_MODULE_NAME_LEN];
	uint64_t tsc_counter_id;
};

struct cp_chain {
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

struct cp_chain *
cp_chain_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_chain_config *cp_chain_config
);

void
cp_chain_free(struct memory_context *memory_context, struct cp_chain *cp_chain);
