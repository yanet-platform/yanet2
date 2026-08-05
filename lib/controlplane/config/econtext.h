#pragma once

#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

static inline struct device_ectx *
config_gen_ectx_get_device(
	struct config_gen_ectx *config_gen_ectx, uint64_t index
) {
	if (index >= config_gen_ectx->device_count) {
		return NULL;
	}
	return ADDR_OF(config_gen_ectx->devices + index);
}

// Build one execution context per worker.
//
// Returns an array of worker_count offset pointers, each a config_gen_ectx
// carrying that worker's own single-instance counter storages. Each entry is
// released with config_gen_ectx_free; the array itself is freed by the caller.
struct config_gen_ectx **
config_gen_ectxs_create(
	struct cp_config_gen *config_gen,
	struct cp_config_gen *old_config_gen,
	uint64_t worker_count,
	yanet_error **err
);

void
config_gen_ectx_free(
	struct cp_config_gen *config_gen,
	struct config_gen_ectx *config_gen_ectx
);
