#pragma once

#include "counters/counters.h"

#include "controlplane/config/defines.h"
#include "controlplane/config/registry.h"

struct cp_pipeline_module_counter_storage_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_pipeline_module_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_module_counter_storage_registry *registry
);

void
cp_pipeline_module_counter_storage_registry_destroy(
	struct cp_pipeline_module_counter_storage_registry *registry
);

struct counter_storage *
cp_pipeline_module_counter_storage_registry_lookup(
	struct cp_pipeline_module_counter_storage_registry *registry,
	const char *pipeline_name,
	uint64_t module_type,
	const char *module_name
);

int
cp_pipeline_module_counter_storage_registry_insert(
	struct cp_pipeline_module_counter_storage_registry *registry,
	char *pipeline_name,
	uint64_t module_type,
	char *module_name,
	struct counter_storage *counter_storage
);

struct cp_pipeline_counter_storage_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_pipeline_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_counter_storage_registry *registry
);

void
cp_pipeline_counter_storage_registry_destroy(
	struct cp_pipeline_counter_storage_registry *registry
);

struct counter_storage *
cp_pipeline_counter_storage_registry_lookup(
	struct cp_pipeline_counter_storage_registry *registry,
	const char *pipeline_name
);

int
cp_pipeline_counter_storage_registry_insert(
	struct cp_pipeline_counter_storage_registry *registry,
	char *pipeline_name,
	struct counter_storage *counter_storage
);
