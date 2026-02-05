#pragma once

#include <stddef.h>
#include <stdint.h>

struct dp_config;

struct counter_value_handle;

struct counter_handle {
	char name[60];
	uint64_t size;
	uint64_t gen;
	struct counter_value_handle *value_handle;
};

struct counter_handle_list {
	uint64_t instance_count;
	uint64_t count;
	struct counter_handle counters[];
};

struct counter_handle_list *
yanet_get_device_counters(struct dp_config *dp_config, const char *device_name);

struct counter_handle_list *
yanet_get_pipeline_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name
);

struct counter_handle_list *
yanet_get_function_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
);

struct counter_handle_list *
yanet_get_chain_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
);

// Get module counters, optionally filtered by name.
struct counter_handle_list *
yanet_get_module_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *const *query,
	size_t query_count
);

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config);

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx);

uint64_t
yanet_get_counter_value(
	struct counter_value_handle *value_handle,
	uint64_t value_idx,
	uint64_t worker_idx
);

void
yanet_counter_handle_list_free(struct counter_handle_list *counters);
