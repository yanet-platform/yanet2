#include <stdlib.h>

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/reclaim.h"
#include "lib/controlplane/config/zone.h"
#include "modules/pdump/dataplane/config.h"

#include "snapshot.h"

struct module *
new_module_pdump(void);
struct object *
new_object_ring(void);

int
pdump_test_prepare_worker(struct yanet_shm *shm) {
	// Retain the real constructors for the fixture's dynamic type loader.
	free(new_module_pdump());
	free(new_object_ring());
	struct dp_config *config = shm->base;
	struct dp_worker *worker =
		memory_balloc(&config->memory_context, sizeof(*worker));
	if (worker == NULL) {
		return -1;
	}
	struct dp_worker **workers =
		memory_balloc(&config->memory_context, sizeof(*workers));
	if (workers == NULL) {
		memory_bfree(&config->memory_context, worker, sizeof(*worker));
		return -1;
	}
	if (pthread_mutex_init(dp_config_external_round_lock(config), NULL) !=
	    0) {
		memory_bfree(
			&config->memory_context, workers, sizeof(*workers)
		);
		memory_bfree(&config->memory_context, worker, sizeof(*worker));
		return -1;
	}
	memset(worker, 0, sizeof(*worker));
	SET_OFFSET_OF(workers, worker);
	SET_OFFSET_OF(&config->workers, workers);
	config->worker_count = 1;
	config->external_worker_rounds = true;
	return 0;
}

void
pdump_test_free_worker(struct yanet_shm *shm) {
	struct dp_config *config = shm->base;
	struct dp_worker **workers = ADDR_OF(&config->workers);
	struct dp_worker *worker = ADDR_OF(workers);
	pthread_mutex_destroy(dp_config_external_round_lock(config));
	memory_bfree(&config->memory_context, worker, sizeof(*worker));
	memory_bfree(&config->memory_context, workers, sizeof(*workers));
	config->workers = NULL;
	config->worker_count = 0;
}

void
pdump_test_set_worker_time(struct agent *agent, uint64_t timestamp) {
	struct dp_config *config = ADDR_OF(&agent->dp_config);
	struct dp_worker **workers = ADDR_OF(&config->workers);
	struct dp_worker *worker = ADDR_OF(workers);
	__atomic_store_n(&worker->current_time, timestamp, __ATOMIC_RELAXED);
}

// Match the native BPF allocator's ABI without requiring an EAL heap.
//
// The production Go-host shim takes one argument, but the converter calls the
// three-argument DPDK ABI. Its result is released by the production libc free.
void *
__wrap_rte_zmalloc(const char *type, size_t size, unsigned alignment) {
	(void)type;
	(void)alignment;
	return calloc(1, size);
}

int
pdump_test_read_rate(
	struct agent *agent,
	const char *name,
	struct pdump_test_rate_snapshot *snapshot
) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *generation = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_module *module =
		cp_config_gen_lookup_module(generation, "pdump", name);
	if (module == NULL) {
		cp_config_reclaim_unused_agents_locked(cp_config);
		cp_config_unlock(cp_config);
		return -1;
	}
	struct pdump_module_config *config =
		container_of(module, struct pdump_module_config, cp_module);
	struct token_bucket *bucket = ADDR_OF(&config->rate_bucket);
	*snapshot = (struct pdump_test_rate_snapshot){
		.has_bucket = bucket != NULL,
		.cost = bucket == NULL ? 0 : bucket->cost,
		.cap = bucket == NULL ? 0 : bucket->cap,
		.edt = bucket == NULL
			       ? 0
			       : atomic_load_explicit(
					 &bucket->edt, memory_order_relaxed
				 ),
	};
	cp_config_reclaim_unused_agents_locked(cp_config);
	cp_config_unlock(cp_config);
	return 0;
}
