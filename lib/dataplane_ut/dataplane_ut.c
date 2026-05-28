#include "dataplane_ut.h"

// dp_worker->gen is initialized to a value far above any plausible
// production cp_config_gen->gen so that harness workers never
// observe a "wait for newer generation" state.
#define DATAPLANE_UT_HIGH_GEN 1000000000000000ULL

#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/counter_storage.h"
#include "lib/dataplane/config/module_loader.h"
#include "lib/dataplane/config/topology.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/pipeline_round.h"
#include "lib/dataplane_ut/mempool.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

struct dataplane_ut {
	void *arena;
	size_t arena_size;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct rte_mempool *mempool;
	uint64_t mock_time_ns;
};

// Counter indices used by wire_worker_counters to address the
// standard worker counter slots by their registration order.
enum {
	WORKER_CTR_ITERATIONS = 0,
	WORKER_CTR_RX = 1,
	WORKER_CTR_TX = 2,
	WORKER_CTR_REMOTE_RX = 3,
	WORKER_CTR_REMOTE_TX = 4,
	WORKER_CTR_RX_BURSTS = 5,
};

// Wire counter address pointers for a single dp_worker.
//
// The worker_counter_storage must already be spawned and wired into
// dp_config before calling this.
static void
wire_worker_counters(struct dp_worker *dp_worker, struct dp_config *dp_config) {
	struct counter_storage *storage =
		ADDR_OF(&dp_config->worker_counter_storage);
	uint64_t idx = dp_worker->idx;

	dp_worker->iterations =
		counter_get_address(WORKER_CTR_ITERATIONS, idx, storage);

	dp_worker->rx_count =
		counter_get_address(WORKER_CTR_RX, idx, storage) + 0;
	dp_worker->rx_size =
		counter_get_address(WORKER_CTR_RX, idx, storage) + 1;

	dp_worker->tx_count =
		counter_get_address(WORKER_CTR_TX, idx, storage) + 0;
	dp_worker->tx_size =
		counter_get_address(WORKER_CTR_TX, idx, storage) + 1;

	dp_worker->remote_rx_count =
		counter_get_address(WORKER_CTR_REMOTE_RX, idx, storage) + 0;

	dp_worker->remote_tx_count =
		counter_get_address(WORKER_CTR_REMOTE_TX, idx, storage) + 0;
	dp_worker->rx_bursts =
		counter_get_address(WORKER_CTR_RX_BURSTS, idx, storage);
}

struct dataplane_ut *
dataplane_ut_new(const struct dataplane_ut_config *cfg) {
	if (cfg == NULL) {
		LOG(ERROR, "dataplane_ut_new: cfg is NULL");
		return NULL;
	}
	if (cfg->cp_memory + cfg->dp_memory == 0) {
		LOG(ERROR, "dataplane_ut_new: total memory is zero");
		return NULL;
	}
	if (cfg->worker_count < 1) {
		LOG(ERROR, "dataplane_ut_new: worker_count must be at least 1");
		return NULL;
	}

	struct dataplane_ut *ut = calloc(1, sizeof(*ut));
	if (ut == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: failed to allocate harness struct");
		return NULL;
	}

	// Allocate the shared arena. calloc zeros the struct so every pointer
	// field is NULL until explicitly set — partial-init free is safe.
	ut->arena_size = cfg->cp_memory + cfg->dp_memory;
	ut->arena = aligned_alloc(64, ut->arena_size);
	if (ut->arena == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: failed to allocate %zu-byte arena",
		    ut->arena_size);
		dataplane_ut_free(ut);
		return NULL;
	}
	memset(ut->arena, 0, ut->arena_size);

	// Lay out dp_config and cp_config inside the arena.
	if (dp_storage_init(
		    0,
		    0,
		    ut->arena,
		    cfg->dp_memory,
		    cfg->cp_memory,
		    &ut->dp_config,
		    &ut->cp_config
	    ) == -1) {
		LOG(ERROR, "dataplane_ut_new: dp_storage_init failed");
		dataplane_ut_free(ut);
		return NULL;
	}

	// Set instance_count — dp_storage_init leaves it at zero.
	ut->dp_config->instance_count = 1;

	// Open the current binary so dlsym can resolve module/device symbols.
	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);
	if (bin_hndl == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: dlopen(NULL) failed: %s",
		    dlerror());
		dataplane_ut_free(ut);
		return NULL;
	}

	// Load requested packet-processing modules.
	for (size_t idx = 0; idx < cfg->module_count; ++idx) {
		if (dp_load_module(
			    ut->dp_config, bin_hndl, cfg->modules[idx]
		    ) == -1) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to load module %s",
			    cfg->modules[idx]);
			dataplane_ut_free(ut);
			return NULL;
		}
	}

	// Load requested device adapters.
	for (size_t idx = 0; idx < cfg->devices_to_load_count; ++idx) {
		if (dp_load_device(
			    ut->dp_config, bin_hndl, cfg->devices_to_load[idx]
		    ) == -1) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to load device %s",
			    cfg->devices_to_load[idx]);
			dataplane_ut_free(ut);
			return NULL;
		}
	}

	// Allocate the system agent inside cp_config memory so that its
	// offset pointers remain valid across all processes that map the
	// same arena. A stack agent would mix shm-relative offsets with
	// process-local addresses and produce SIGBUS on cross-process
	// dereference.
	struct agent *agent = dp_system_agent_new(
		ut->cp_config, ut->dp_config, "dataplane_ut"
	);
	if (agent == NULL) {
		LOG(ERROR, "dataplane_ut_new: failed to allocate system agent");
		dataplane_ut_free(ut);
		return NULL;
	}

	// Populate dp_topology with the logical port names from cfg.
	if (cfg->device_count > 0) {
		struct dp_port *ports = dp_topology_alloc_devices(
			ut->dp_config, cfg->device_count
		);
		if (ports == NULL) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to allocate dp_topology");
			dataplane_ut_free(ut);
			return NULL;
		}
		for (size_t idx = 0; idx < cfg->device_count; ++idx) {
			strtcpy(ports[idx].port_name,
				cfg->devices[idx],
				sizeof(ports[idx].port_name));
		}
	}

	// Create the initial cp_config_gen so agents can register modules
	// and pipelines.
	yanet_error *err = NULL;
	struct cp_config_gen *cp_config_gen = cp_config_gen_create(agent, &err);
	if (cp_config_gen == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: cp_config_gen_create failed: %s",
		    yanet_error_message(err));
		yanet_error_free(err);
		dataplane_ut_free(ut);
		return NULL;
	}
	SET_OFFSET_OF(&ut->cp_config->cp_config_gen, cp_config_gen);

	// Register the standard worker counters used by the pipeline.
	// Sizes and names mirror production worker.c::worker_register_counter.
	counter_registry_init(
		&ut->dp_config->worker_counters,
		&ut->dp_config->memory_context,
		0
	);
	if (worker_counters_register(ut->dp_config) == -1) {
		LOG(ERROR,
		    "dataplane_ut_new: failed to register worker counters");
		dataplane_ut_free(ut);
		return NULL;
	}

	// Initialise counter storage allocators for both zones, link the
	// worker registry, and spawn the backing storage so address
	// pointers resolve. worker_count drives the per-instance layout.
	if (dp_counter_storage_init(
		    ut->dp_config, ut->cp_config, cfg->worker_count
	    ) != 0) {
		LOG(ERROR, "dataplane_ut_new: dp_counter_storage_init failed");
		dataplane_ut_free(ut);
		return NULL;
	}

	// Create the mock mempool now so step 2 can allocate mbufs.
	ut->mempool = test_mempool_create();
	if (ut->mempool == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: test_mempool_create returned NULL");
		dataplane_ut_free(ut);
		return NULL;
	}

	// Allocate dp_workers array inside dp_config memory.
	struct dp_worker **workers_array = (struct dp_worker **)memory_balloc(
		&ut->dp_config->memory_context,
		cfg->worker_count * sizeof(struct dp_worker *)
	);
	if (workers_array == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: failed to allocate workers pointer array"
		);
		dataplane_ut_free(ut);
		return NULL;
	}

	ut->dp_config->worker_count = cfg->worker_count;
	SET_OFFSET_OF(&ut->dp_config->workers, workers_array);

	struct dp_worker **dp_workers = ADDR_OF(&ut->dp_config->workers);
	for (size_t idx = 0; idx < cfg->worker_count; ++idx) {
		struct dp_worker *dp_worker = (struct dp_worker *)memory_balloc(
			&ut->dp_config->memory_context, sizeof(struct dp_worker)
		);
		if (dp_worker == NULL) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to allocate dp_worker "
			    "%zu",
			    idx);
			dataplane_ut_free(ut);
			return NULL;
		}
		memset(dp_worker, 0, sizeof(struct dp_worker));
		dp_worker->idx = idx;
		// A high generation value ensures the pipeline never waits for
		// a newer config snapshot — same trick used by mock.
		dp_worker->gen = DATAPLANE_UT_HIGH_GEN;
		dp_worker->rx_mempool = ut->mempool;
		dp_worker->core_id = (uint32_t)idx;
		dp_worker->device_id = 0;
		dp_worker->queue_id = (uint32_t)idx;
		dp_worker->rx_burst_size = WORKER_RX_BURST_SIZE;

		wire_worker_counters(dp_worker, ut->dp_config);

		SET_OFFSET_OF(dp_workers + idx, dp_worker);
	}

	return ut;
}

void
dataplane_ut_free(struct dataplane_ut *ut) {
	if (ut == NULL) {
		return;
	}

	if (ut->mempool != NULL) {
		// Do NOT call rte_mempool_free — the test pool is not a real
		// DPDK pool and has no backing memory to tear down that way.
		free(ut->mempool);
		ut->mempool = NULL;
	}

	if (ut->cp_config != NULL) {
		counter_storage_allocator_fini(
			&ut->cp_config->counter_storage_allocator
		);
		memory_context_fini(&ut->cp_config->memory_context);
	}

	if (ut->dp_config != NULL) {
		counter_storage_allocator_fini(
			&ut->dp_config->counter_storage_allocator
		);
		memory_context_fini(&ut->dp_config->memory_context);
	}

	if (ut->arena != NULL) {
		free(ut->arena);
		ut->arena = NULL;
	}

	free(ut);
}

struct yanet_shm *
dataplane_ut_shm(struct dataplane_ut *ut) {
	// The arena base address serves as the opaque shm handle.
	return (struct yanet_shm *)ut->arena;
}

void
dataplane_ut_set_time_ns(struct dataplane_ut *ut, uint64_t ns) {
	ut->mock_time_ns = ns;
}

uint64_t
dataplane_ut_get_time_ns(struct dataplane_ut *ut) {
	return ut->mock_time_ns;
}

struct rte_mbuf *
dataplane_ut_alloc_mbuf(struct dataplane_ut *ut) {
	return rte_pktmbuf_alloc(ut->mempool);
}

void
dataplane_ut_run(
	struct dataplane_ut *ut,
	size_t worker_idx,
	struct packet_list *input,
	struct dataplane_ut_round_result *result
) {
	// The harness only manages one instance, so dp_workers lives in
	// dp_config->workers as a registry of size worker_count.
	struct dp_worker **workers = ADDR_OF(&ut->dp_config->workers);
	struct dp_worker *dp_worker = ADDR_OF(&workers[worker_idx]);

	// Mirror worker_loop_round housekeeping: time, gen, iterations.
	dp_worker->current_time = ut->mock_time_ns;

	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&ut->cp_config->cp_config_gen);
	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&cp_config_gen->config_gen_ectx);

	__atomic_store_n(&dp_worker->gen, cp_config_gen->gen, __ATOMIC_RELEASE);
	*dp_worker->iterations += 1;

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	packet_list_concat(&packet_front.pending_input, input);
	packet_list_init(input);

	packet_list_init(&result->output);
	packet_list_init(&result->drop);

	if (config_gen_ectx == NULL) {
		// No active pipeline: drop everything.
		packet_list_concat(&result->drop, &packet_front.pending_input);
		return;
	}

	worker_pipeline_round(
		dp_worker, cp_config_gen, config_gen_ectx, &packet_front
	);

	packet_list_concat(&result->output, &packet_front.output);
	packet_list_concat(&result->drop, &packet_front.drop);
}
