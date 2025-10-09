#pragma once

#include <stdint.h>

struct cp_chain_config;

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
cp_function_config_free(struct cp_function_config *function_config);

int
cp_function_config_set_chain(
	struct cp_function_config *function_config,
	uint64_t index,
	struct cp_chain_config *chain_config,
	uint64_t weight
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
