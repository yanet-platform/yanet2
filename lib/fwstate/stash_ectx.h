#pragma once

#include <stdint.h>

#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"

// Worker index of an execution context whose worker is not known.
#define FWSTATE_WORKER_IDX_NONE UINT64_MAX

// Find the index of the worker whose execution context holds module_ectx.
//
// Used by an execution-context commit handler, which is not told its
// worker: the generation keeps one context per worker at the worker's
// index, so the index is where the module's own context appears. Returns
// FWSTATE_WORKER_IDX_NONE when the context is not found, in which case no
// per-worker state can be linked.
static inline uint64_t
fwstate_stash_worker_idx(struct module_ectx *module_ectx) {
	struct config_gen_ectx *own = module_ectx->abs_config_gen_ectx;
	if (own == NULL) {
		return FWSTATE_WORKER_IDX_NONE;
	}
	struct cp_config_gen *config_gen = ADDR_OF(&own->cp_config_gen);
	if (config_gen == NULL) {
		return FWSTATE_WORKER_IDX_NONE;
	}
	for (uint64_t idx = 0; idx < config_gen->config_gen_ectx_count; ++idx) {
		if (cp_config_gen_worker_ectx(config_gen, idx) == own) {
			return idx;
		}
	}
	return FWSTATE_WORKER_IDX_NONE;
}
