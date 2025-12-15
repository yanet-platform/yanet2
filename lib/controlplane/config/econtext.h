#pragma once

#include <stdint.h>

#include "common/memory_address.h"
#include "dataplane/module/module.h"

struct counter_storage;

struct cp_module;
struct cp_chain;
struct cp_function;
struct cp_pipeline;
struct cp_device;

struct cp_config_gen;

struct module_ectx {
	module_handler handler;
	struct cp_module *cp_module;
	struct counter_storage *counter_storage;
	struct config_gen_ectx *config_gen_ectx;

	uint64_t mc_index_size;
	uint64_t *mc_index;

	uint64_t cm_index_size;
	uint64_t *cm_index;
};

static inline uint64_t
module_ectx_encode_device(struct module_ectx *module_ectx, uint64_t index) {
	uint64_t *mc_index = ADDR_OF(&module_ectx->mc_index);
	return mc_index[index];
}

static inline uint64_t
module_ectx_decode_device(struct module_ectx *module_ectx, uint64_t index) {
	uint64_t *cm_index = ADDR_OF(&module_ectx->cm_index);
	return cm_index[index];
}

struct chain_module_ectx {
	struct module_ectx *module_ectx;
	uint64_t tsc_counter_id;
};

struct chain_ectx {
	struct cp_chain *cp_chain;
	struct counter_storage *counter_storage;
	uint64_t length;
	struct chain_module_ectx modules[];
};

struct function_ectx {
	struct cp_function *cp_function;
	struct counter_storage *counter_storage;
	uint64_t chain_count;
	struct chain_ectx **chains;
	uint64_t chain_map_size;
	struct chain_ectx *chain_map[];
};

struct pipeline_ectx {
	struct cp_pipeline *cp_pipeline;
	struct counter_storage *counter_storage;
	uint64_t length;
	struct function_ectx *functions[];
};

struct device_entry_ectx {
	device_handler handler;
	uint64_t pipeline_count;
	struct pipeline_ectx **pipelines;
	uint64_t pipeline_map_size;
	struct pipeline_ectx *pipeline_map[];
};

struct device_ectx {
	struct cp_device *cp_device;
	struct counter_storage *counter_storage;
	struct device_entry_ectx *input_pipelines;
	struct device_entry_ectx *output_pipelines;
};

struct config_gen_ectx {
	struct cp_config_gen *cp_config_gen;
	struct phy_device_map *phy_device_maps;

	uint64_t device_count;
	struct device_ectx *devices[];
};

static inline struct device_ectx *
config_gen_ectx_get_device(
	struct config_gen_ectx *config_gen_ectx, uint64_t index
) {
	if (index >= config_gen_ectx->device_count)
		return NULL;
	return ADDR_OF(config_gen_ectx->devices + index);
}

struct config_gen_ectx *
config_gen_ectx_create(
	struct cp_config_gen *config_gen, struct cp_config_gen *old_config_gen
);

void
config_gen_ectx_free(
	struct cp_config_gen *config_gen,
	struct config_gen_ectx *config_gen_ectx
);
