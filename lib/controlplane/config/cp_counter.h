#pragma once

#include "api/counter.h"
#include "counters/counters.h"

#include "lib/errors/errors.h"

struct memory_context;

#define KEY_MAX_SIZE 80
#define VALUE_MAX_SIZE 80
#define MAX_TAG_COUNT 8

struct cp_counter_tag {
	char key[KEY_MAX_SIZE];
	char value[VALUE_MAX_SIZE];
};

struct cp_counter_storage {
	struct cp_counter_tag tags[MAX_TAG_COUNT];
	size_t tag_count;
	struct counter_storage *storage;
};

struct cp_config_counter_storage_registry {
	struct memory_context *memory_context;
	struct cp_counter_storage *items;
	size_t count;
	size_t capacity;
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
