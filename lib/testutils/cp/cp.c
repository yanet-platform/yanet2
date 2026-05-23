#include "cp.h"

#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/exp_array.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/strutils.h"

#include "api/agent.h"
#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

#include "counters/counters.h"
#include "dataplane/module/module.h"

#include "lib/errors/errors.h"
#include "lib/logging/log.h"

struct test_shm {
	// Raw arena: [dp_config | dp arena ...][cp_config | cp arena ...]
	void *arena;
	struct cp_config *cp_config;
	// Free size of cp_config->block_allocator captured after create
	// finishes its own bookkeeping allocations. Used by test_shm_destroy
	// to detect leaks.
	size_t cp_baseline_free;
};

// Round size up to a multiple of align. align must be a power of two.
static inline size_t
round_up(size_t size, size_t align) {
	return (size + align - 1) & ~(align - 1);
}

struct test_shm *
test_shm_create(
	size_t cp_size,
	size_t dp_size,
	const char *const *module_names,
	size_t module_name_count,
	yanet_error **err
) {
	cp_size = round_up(cp_size, 64);
	dp_size = round_up(dp_size, 64);

	struct test_shm *shm =
		(struct test_shm *)calloc(1, sizeof(struct test_shm));
	if (shm == NULL) {
		yanet_error_add(err, "failed to allocate test_shm handle");
		return NULL;
	}

	// Single contiguous allocation for both dp and cp zones.
	void *arena = aligned_alloc(64, dp_size + cp_size);
	if (arena == NULL) {
		yanet_error_add(err, "failed to allocate arena");
		free(shm);
		return NULL;
	}
	memset(arena, 0, dp_size + cp_size);

	shm->arena = arena;

	// Lay out dp_config at the front of the arena.
	struct dp_config *dp_config = (struct dp_config *)arena;
	dp_config->instance_count = 1;
	dp_config->instance_idx = 0;
	dp_config->numa_idx = 0;
	dp_config->worker_count = 1;
	dp_config->storage_size = dp_size + cp_size;

	// Give the dp block allocator the region after the dp_config header.
	block_allocator_init(&dp_config->block_allocator);
	block_allocator_put_arena(
		&dp_config->block_allocator,
		(uint8_t *)arena + sizeof(struct dp_config),
		dp_size - sizeof(struct dp_config)
	);
	memory_context_init(
		&dp_config->memory_context, "dp", &dp_config->block_allocator
	);

	// Lay out cp_config immediately after the dp zone.
	struct cp_config *cp_config =
		(struct cp_config *)((uint8_t *)arena + dp_size);

	// Give the cp block allocator the region after the cp_config header.
	block_allocator_init(&cp_config->block_allocator);
	block_allocator_put_arena(
		&cp_config->block_allocator,
		(uint8_t *)arena + dp_size + sizeof(struct cp_config),
		cp_size - sizeof(struct cp_config)
	);
	memory_context_init(
		&cp_config->memory_context, "cp", &cp_config->block_allocator
	);

	// Cross-link dp_config <-> cp_config via offset pointers.
	SET_OFFSET_OF(&dp_config->cp_config, cp_config);
	SET_OFFSET_OF(&cp_config->dp_config, dp_config);

	shm->cp_config = cp_config;

	// Allocate a zero-element cp_agent_registry from cp memory.
	struct cp_agent_registry *reg =
		(struct cp_agent_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_agent_registry)
		);
	if (reg == NULL) {
		yanet_error_add(err, "failed to allocate cp_agent_registry");
		goto fail_arena;
	}
	reg->count = 0;
	SET_OFFSET_OF(&cp_config->agent_registry, reg);

	// Initialize the counter storage allocator for the cp zone.
	// instance_count=1 matches worker_count=1.
	counter_storage_allocator_init(
		&cp_config->counter_storage_allocator,
		&cp_config->memory_context,
		1
	);

	// Initialize cp_config_gen using the canonical stub-agent pattern.
	// A transient stack-allocated agent is constructed here solely to
	// satisfy the cp_config_gen_create signature, which requires an agent
	// to resolve dp_config and cp_config pointers.
	//
	// Note: the SET_OFFSET_OF calls below store stack-relative offsets
	// inside the stub agent's own fields. This is safe because the stub
	// agent never escapes this scope — it is used only within
	// cp_config_gen_create and is destroyed before the function returns.
	// The returned cp_config_gen lives in shm and holds no back-references
	// to the stub agent (confirmed by inspection: dp_topology.device_count
	// is zero so cp_device_init, which stores agent in cp_device, is never
	// called).
	{
		struct agent stub_agent;
		memset(&stub_agent, 0, sizeof(struct agent));
		memory_context_init_from(
			&stub_agent.memory_context,
			&cp_config->memory_context,
			"test_shm cp_config_gen init"
		);
		SET_OFFSET_OF(&stub_agent.dp_config, dp_config);
		SET_OFFSET_OF(&stub_agent.cp_config, cp_config);

		yanet_error *cgen_err = NULL;
		struct cp_config_gen *config_gen =
			cp_config_gen_create(&stub_agent, &cgen_err);
		if (config_gen == NULL) {
			yanet_error_add(
				err,
				"failed to init cp_config_gen: %s",
				yanet_error_message(cgen_err)
			);
			yanet_error_free(cgen_err);
			memory_context_fini(&stub_agent.memory_context);
			goto fail_arena;
		}
		memory_context_fini(&stub_agent.memory_context);
		SET_OFFSET_OF(&cp_config->cp_config_gen, config_gen);
	}

	// Allocate a single dp_worker. gen=UINT64_MAX ensures
	// dp_config_wait_for_gen never spins on any generation value.
	struct dp_worker **workers_array = (struct dp_worker **)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_worker *)
	);
	struct dp_worker *worker = (struct dp_worker *)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_worker)
	);
	if (workers_array == NULL || worker == NULL) {
		yanet_error_add(err, "failed to allocate dp_worker");
		goto fail_arena;
	}
	worker->gen = UINT64_MAX;
	SET_OFFSET_OF(workers_array, worker);
	SET_OFFSET_OF(&dp_config->workers, workers_array);

	// Resolve each module from the process image and register it. The
	// loader returns a heap-allocated module; we copy name and handler
	// into dp_modules, then free it.
	for (size_t idx = 0; idx < module_name_count; ++idx) {
		const char *name = module_names[idx];
		char sym[128];
		snprintf(sym, sizeof(sym), "new_module_%s", name);
		module_load_handler loader =
			(module_load_handler)dlsym(RTLD_DEFAULT, sym);
		if (loader == NULL) {
			yanet_error_add(
				err,
				"symbol '%s' not found in process image",
				sym
			);
			goto fail_arena;
		}
		struct module *m = loader();
		if (m == NULL) {
			yanet_error_add(
				err,
				"loader for module '%s' returned NULL",
				name
			);
			goto fail_arena;
		}
		struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
		if (mem_array_expand_exp(
			    &dp_config->memory_context,
			    (void **)&dp_modules,
			    sizeof(struct dp_module),
			    &dp_config->module_count
		    )) {
			free(m);
			yanet_error_add(
				err,
				"failed to expand dp_modules for '%s'",
				name
			);
			goto fail_arena;
		}
		struct dp_module *slot =
			dp_modules + dp_config->module_count - 1;
		strtcpy(slot->name, m->name, sizeof(slot->name));
		slot->handler = m->handler;
		SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);
		// Convention: new_module_<name> returns a pointer to the start
		// of a single malloc'd block. Free it here, after copying the
		// handler into dp_modules. A module that violates this turns
		// the free into a bug.
		free(m);
	}

	// Capture the cp baseline free size after all harness bookkeeping is
	// done. test_shm_destroy compares against this to detect leaks.
	shm->cp_baseline_free =
		block_allocator_free_size(&cp_config->block_allocator);

	return shm;

fail_arena:
	free(arena);
	free(shm);
	return NULL;
}

void
test_shm_destroy(struct test_shm *shm) {
	if (shm == NULL) {
		return;
	}

	struct cp_config *cp_config = shm->cp_config;

	size_t cp_free_now =
		block_allocator_free_size(&cp_config->block_allocator);
	if (cp_free_now != shm->cp_baseline_free) {
		LOG(WARN,
		    "cp allocator leak detected: baseline=%zu current=%zu "
		    "delta=%zd bytes",
		    shm->cp_baseline_free,
		    cp_free_now,
		    (ssize_t)shm->cp_baseline_free - (ssize_t)cp_free_now);
	}

	counter_storage_allocator_fini(&cp_config->counter_storage_allocator);
	memory_context_fini(&((struct dp_config *)shm->arena)->memory_context);
	memory_context_fini(&cp_config->memory_context);

	free(shm->arena);
	free(shm);
}

void *
test_shm_dp_config(struct test_shm *shm) {
	return shm->arena;
}
