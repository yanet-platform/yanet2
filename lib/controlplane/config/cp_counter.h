#pragma once

#include "common/hash_index.h"

#include "api/counter.h"
#include "lib/counters/counters.h"

#include "lib/errors/errors.h"

struct memory_context;

#define MAX_TAG_COUNT 16

// Tag sets are stored by value: the fixed-size counter_tag keeps them
// copyable between the arena and plain heap memory without per-string
// allocation.
struct cp_counter_storage {
	struct counter_tag tags[MAX_TAG_COUNT];
	size_t tag_count;
	struct counter_storage *storage;
};

// Items are addressed by their position in the items array; the index
// stores those positions, so growing the array never invalidates it.
struct cp_config_counter_storage_registry {
	struct memory_context *memory_context;
	struct cp_counter_storage *items;
	size_t count;
	size_t capacity;

	// Hash index over each item's exact normalized tag set, serving
	// duplicate rejection on insert and exact lookups in constant time.
	//
	// Entries hold item positions, so growing the items array never
	// invalidates them. The index stays in lockstep with the array —
	// capacity and count equal the array's, and both grow together —
	// while pattern queries with predicate values still walk the array.
	struct hash_index tag_index;
};

int
cp_config_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_config_counter_storage_registry *registry,
	yanet_error **err
);

int
cp_config_counter_storage_registry_insert(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *tags,
	size_t tag_count,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct cp_counter_storage **
cp_config_counter_storage_registry_find(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *tags,
	size_t tag_count,
	yanet_error **err
);

// Resolve the storage registered under exactly the passed tag list, or
// NULL when no item carries precisely that tag set.
//
// The tags are normalized the same way insert normalizes them, so any
// permutation of a stored set resolves to its storage; a set that is a
// subset or superset of a stored one does not match. Predicate values
// cannot match: stored tag sets never carry them.
struct counter_storage *
cp_config_counter_storage_registry_lookup_exact(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *tags,
	size_t tag_count
);

void
cp_config_counter_storage_registry_fini(
	struct cp_config_counter_storage_registry *registry
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name
);

int
cp_config_counter_storage_registry_insert_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name
);

int
cp_config_counter_storage_registry_insert_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
);

int
cp_config_counter_storage_registry_insert_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
);

int
cp_config_counter_storage_registry_insert_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name
);

int
cp_config_counter_storage_registry_insert_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_module_tagged(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *registry_tag
);

int
cp_config_counter_storage_registry_insert_module_tagged(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *registry_tag,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_object(
	struct cp_config_counter_storage_registry *registry,
	const char *object_type,
	const char *object_name
);

int
cp_config_counter_storage_registry_insert_object(
	struct cp_config_counter_storage_registry *registry,
	const char *object_type,
	const char *object_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);

struct counter_storage *
cp_config_counter_storage_registry_lookup_module_object_link(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *object_type,
	const char *object_name
);

int
cp_config_counter_storage_registry_insert_module_object_link(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *object_type,
	const char *object_name,
	struct counter_storage *counter_storage,
	yanet_error **err
);
