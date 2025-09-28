#pragma once

#include <stdint.h>

struct counter_storage;

struct cp_module;
struct cp_chain;
struct cp_function;
struct cp_pipeline;
struct cp_device;

struct cp_config_gen;

struct module_ectx {
	struct cp_module *module;
	struct counter_storage *counter_storage;
};

struct chain_ectx {
	struct cp_chain *chain;
	struct counter_storage *counter_storage;
	uint64_t length;
	struct module_ectx *modules[];
};

struct function_ectx {
	struct cp_function *function;
	struct counter_storage *counter_storage;
	uint64_t chain_count;
	struct chain_ectx **chains;
	uint64_t chain_map_size;
	struct chain_ectx *chain_map[];
};

struct pipeline_ectx {
	struct cp_pipeline *pipeline;
	struct counter_storage *counter_storage;
	uint64_t length;
	struct function_ectx *functions[];
};

struct device_ectx {
	struct cp_device *device;
	struct counter_storage *counter_storage;
	uint64_t pipeline_count;
	struct pipeline_ectx **pipelines;
	uint64_t pipeline_map_size;
	struct pipeline_ectx *pipeline_map[];
};

struct phy_device_map {
	struct device_ectx *vlan[4096];
};

struct config_ectx {
	struct phy_device_map *phy_device_maps;

	uint64_t device_count;
	struct device_ectx *devices[];
};

struct config_ectx *
config_ectx_create(
	struct cp_config_gen *config_gen, struct cp_config_gen *old_config_gen
);

void
config_ectx_free(
	struct cp_config_gen *config_gen, struct config_ectx *config_ectx
);
