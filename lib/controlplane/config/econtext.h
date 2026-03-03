#pragma once

#include "lib/dataplane/pipeline/econtext.h"

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
