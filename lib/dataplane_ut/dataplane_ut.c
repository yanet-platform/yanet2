#include "dataplane_ut.h"

// dp_worker->gen is initialized to a value far above any plausible
// production cp_config_gen->gen so that harness workers never
// observe a "wait for newer generation" state.
#define DATAPLANE_UT_HIGH_GEN 1000000000000000ULL

#include <dlfcn.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "common/buildinfo.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/counter_storage.h"
#include "lib/dataplane/config/module_loader.h"
#include "lib/dataplane/config/plugin_loader.h"
#include "lib/dataplane/config/topology.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/pipeline_round.h"
#include "lib/dataplane/worker/round.h"
#include "lib/dataplane_ut/mempool.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

struct dataplane_ut {
	void *arena;
	size_t arena_size;
	// Shared-memory handle over the arena, handed out to agents.
	//
	// Owned by the harness: it is never detached, because the arena is
	// heap memory that the harness frees itself.
	struct yanet_shm shm;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct agent *agent;
	struct rte_mempool *mempool;
	uint64_t mock_time_ns;
	struct plugin_registry plugins;
};

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
	if (cfg->packet_recirc_limit != 0 &&
	    (cfg->packet_recirc_limit < PACKET_RECIRC_LIMIT_MIN ||
	     cfg->packet_recirc_limit > PACKET_RECIRC_LIMIT_MAX)) {
		LOG(ERROR,
		    "dataplane_ut_new: packet_recirc_limit must be zero or in "
		    "range %u..%u",
		    (unsigned)PACKET_RECIRC_LIMIT_MIN,
		    (unsigned)PACKET_RECIRC_LIMIT_MAX);
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
	ut->shm.base = ut->arena;
	ut->shm.size = ut->arena_size;

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
	ut->dp_config->packet_recirc_limit =
		cfg->packet_recirc_limit == 0 ? PACKET_RECIRC_LIMIT_DEFAULT
					      : cfg->packet_recirc_limit;

	// Open the current binary so dlsym can resolve module/device symbols.
	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);
	if (bin_hndl == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: dlopen(NULL) failed: %s",
		    dlerror());
		dataplane_ut_free(ut);
		return NULL;
	}

	// Load module .so plugins so their symbols take priority over the
	// statically-linked built-ins when resolving new_module_<name>.
	if (dp_load_plugins(cfg->plugin_dir, &ut->plugins) == -1) {
		LOG(ERROR,
		    "dataplane_ut_new: failed to load plugins from %s",
		    cfg->plugin_dir);
		dataplane_ut_free(ut);
		return NULL;
	}

	// Load requested packet-processing modules.
	for (size_t idx = 0; idx < cfg->module_count; ++idx) {
		if (dp_load_module(
			    ut->dp_config,
			    bin_hndl,
			    &ut->plugins,
			    cfg->modules[idx]
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

	// Register requested object types directly. The harness owns no
	// dlsym constructor for them, so fill the dp_objects array in place
	// rather than going through dp_load_object; cp_object_init only needs
	// the type name resolvable via dp_config_lookup_object.
	if (cfg->objects_to_load_count > 0) {
		struct dp_object *dp_objects =
			(struct dp_object *)memory_balloc(
				&ut->dp_config->memory_context,
				cfg->objects_to_load_count *
					sizeof(struct dp_object)
			);
		if (dp_objects == NULL) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to allocate dp_objects");
			dataplane_ut_free(ut);
			return NULL;
		}
		for (size_t idx = 0; idx < cfg->objects_to_load_count; ++idx) {
			strtcpy(dp_objects[idx].name,
				cfg->objects_to_load[idx],
				sizeof(dp_objects[idx].name));
		}
		SET_OFFSET_OF(&ut->dp_config->dp_objects, dp_objects);
		ut->dp_config->object_count = cfg->objects_to_load_count;
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
			strtcpy(ports[idx].device_name,
				cfg->devices[idx],
				sizeof(ports[idx].device_name));
		}
	}

	// Allocate the system agent inside cp_config memory so that its
	// offset pointers remain valid across all processes that map the
	// same arena. A stack agent would mix shm-relative offsets with
	// process-local addresses and produce SIGBUS on cross-process
	// dereference.
	cp_config_lock(ut->cp_config);

	struct agent *agent = dp_system_agent_new(
		ut->cp_config, ut->dp_config, "dataplane_ut"
	);
	if (agent == NULL) {
		LOG(ERROR, "dataplane_ut_new: failed to allocate system agent");
		cp_config_unlock(ut->cp_config);
		dataplane_ut_free(ut);
		return NULL;
	}
	ut->agent = agent;

	// Create the initial cp_config_gen so agents can register modules
	// and pipelines.
	yanet_error *err = NULL;
	struct cp_config_gen *cp_config_gen = cp_config_gen_new(agent, &err);
	if (cp_config_gen == NULL) {
		LOG(ERROR,
		    "dataplane_ut_new: cp_config_gen_new failed: %s",
		    yanet_error_message(err));
		yanet_error_free(err);
		cp_config_unlock(ut->cp_config);
		dataplane_ut_free(ut);
		return NULL;
	}
	SET_OFFSET_OF(&ut->cp_config->cp_config_gen, cp_config_gen);
	cp_config_unlock(ut->cp_config);

	counter_registry_init(
		&ut->dp_config->worker_counters,
		&ut->dp_config->memory_context,
		0
	);
	struct worker_counter_ids counter_ids;
	if (worker_counters_register(
		    &ut->dp_config->worker_counters, &counter_ids
	    ) == -1) {
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

	dp_config_mark_ready(ut->dp_config);

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
		    "dataplane_ut_new: failed to allocate workers pointer "
		    "array");
		dataplane_ut_free(ut);
		return NULL;
	}

	ut->dp_config->worker_count = cfg->worker_count;
	SET_OFFSET_OF(&ut->dp_config->workers, workers_array);

	// Devices are optional: a harness with no explicit device_count still
	// exposes the implicit device 0, mirroring the Go side's semantics.
	size_t effective_device_count =
		cfg->device_count == 0 ? 1 : cfg->device_count;

	struct dp_worker **dp_workers = ADDR_OF(&ut->dp_config->workers);
	for (size_t idx = 0; idx < cfg->worker_count; ++idx) {
		if (cfg->workers != NULL &&
		    cfg->workers[idx].device_id >= effective_device_count) {
			LOG(ERROR,
			    "dataplane_ut_new: worker %zu binds out-of-range "
			    "device_id %u (device_count %zu)",
			    idx,
			    cfg->workers[idx].device_id,
			    effective_device_count);
			dataplane_ut_free(ut);
			return NULL;
		}

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
		if (cfg->workers != NULL) {
			dp_worker->device_id = cfg->workers[idx].device_id;
			dp_worker->queue_id = cfg->workers[idx].queue_id;
		} else {
			dp_worker->device_id = 0;
			dp_worker->queue_id = (uint32_t)idx;
		}
		dp_worker->rx_burst_size = WORKER_RX_BURST_SIZE;

		SET_OFFSET_OF(dp_workers + idx, dp_worker);
	}

	// The harness instance starts with its clock already running.
	//
	// A test that never drives a packet still exercises control-plane
	// code that has to judge the deadlines in this instance, and an
	// instance whose workers have never published cannot be judged at
	// all. Real time is the same reading a started worker would take.
	struct timespec startup_ts;
	if (clock_gettime(CLOCK_REALTIME, &startup_ts) == 0) {
		dataplane_ut_set_time_ns(
			ut,
			(uint64_t)startup_ts.tv_sec * 1000000000ULL +
				(uint64_t)startup_ts.tv_nsec
		);
	}

	worker_counters_bind(ut->dp_config, &counter_ids);

	// Skipped when device_count == 0: the implicit device 0 (see above)
	// has no dp_topology slot, so per-device worker counts stay
	// unavailable in that mode -- a known, deferred gap.
	if (cfg->device_count > 0) {
		uint64_t *device_worker_counts = (uint64_t *)calloc(
			effective_device_count, sizeof(uint64_t)
		);
		if (device_worker_counts == NULL) {
			LOG(ERROR,
			    "dataplane_ut_new: failed to allocate device "
			    "worker counts");
			dataplane_ut_free(ut);
			return NULL;
		}
		for (size_t idx = 0; idx < cfg->worker_count; ++idx) {
			struct dp_worker *dp_worker = ADDR_OF(dp_workers + idx);
			++device_worker_counts[dp_worker->device_id];
		}
		for (size_t device_idx = 0; device_idx < effective_device_count;
		     ++device_idx) {
			if (dp_topology_set_device_worker_count(
				    ut->dp_config,
				    device_idx,
				    device_worker_counts[device_idx]
			    ) != 0) {
				LOG(ERROR,
				    "dataplane_ut_new: failed to set "
				    "dp_topology worker count for device "
				    "%zu",
				    device_idx);
				free(device_worker_counts);
				dataplane_ut_free(ut);
				return NULL;
			}
		}
		free(device_worker_counts);
	}

	return ut;
}

void
dataplane_ut_free(struct dataplane_ut *ut) {
	if (ut == NULL) {
		return;
	}

	if (ut->mempool != NULL) {
		test_mempool_free(ut->mempool);
		ut->mempool = NULL;
	}

	if (ut->cp_config != NULL) {
		memory_context_fini(&ut->cp_config->memory_context);
	}

	if (ut->dp_config != NULL) {
		memory_context_fini(&ut->dp_config->memory_context);
	}

	if (ut->arena != NULL) {
		free(ut->arena);
		ut->arena = NULL;
	}

	dp_unload_plugins(&ut->plugins);

	free(ut);
}

struct yanet_shm *
dataplane_ut_shm(struct dataplane_ut *ut) {
	return &ut->shm;
}

// Publish a time on every worker of the harness instance.
//
// In a live instance each worker publishes the time at the head of its
// round, which is what tells an attached process what time the instance
// thinks it is. A harness runs a round only when a test drives packets
// through it, so without this an instance with idle workers would answer
// that it has no time at all.
static void
dataplane_ut_publish_time(struct dataplane_ut *ut, uint64_t ns) {
	struct dp_worker **workers = ADDR_OF(&ut->dp_config->workers);
	if (workers == NULL) {
		return;
	}

	for (uint64_t idx = 0; idx < ut->dp_config->worker_count; ++idx) {
		struct dp_worker *worker = ADDR_OF(workers + idx);
		if (worker == NULL) {
			continue;
		}

		__atomic_store_n(&worker->current_time, ns, __ATOMIC_RELAXED);
	}
}

void
dataplane_ut_set_time_ns(struct dataplane_ut *ut, uint64_t ns) {
	ut->mock_time_ns = ns;
	dataplane_ut_publish_time(ut, ns);
}

uint64_t
dataplane_ut_get_time_ns(struct dataplane_ut *ut) {
	return ut->mock_time_ns;
}

struct rte_mbuf *
dataplane_ut_alloc_mbuf(struct dataplane_ut *ut) {
	return rte_pktmbuf_alloc(ut->mempool);
}

static void
dataplane_ut_packet_list_free(struct packet_list *list) {
	struct packet *packet;
	while ((packet = packet_list_pop(list)) != NULL) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (mbuf->pool != NULL) {
			rte_pktmbuf_free(mbuf);
		} else {
			free_packet(packet);
		}
	}
}

void
dataplane_ut_round_result_free(struct dataplane_ut_round_result *result) {
	dataplane_ut_packet_list_free(&result->output);
	dataplane_ut_packet_list_free(&result->drop);
}

size_t
dataplane_ut_mempool_outstanding(struct dataplane_ut *ut) {
	return test_mempool_outstanding(ut->mempool);
}

void
dataplane_ut_run(
	struct dataplane_ut *ut,
	size_t worker_idx,
	struct packet_list *input,
	struct dataplane_ut_round_result *result
) {
	if (worker_idx >= ut->dp_config->worker_count) {
		LOG(ERROR,
		    "dataplane_ut_run: worker_idx %zu is out of range "
		    "(worker_count %lu)",
		    worker_idx,
		    ut->dp_config->worker_count);

		// Mirror the config_gen_ectx == NULL branch below: a caller
		// must still get a well-formed result (output/drop initialized)
		// and input must end up drained, so packets are neither leaked
		// nor re-added to a non-empty list by dataplane_ut_run_rounds.
		packet_list_init(&result->output);
		packet_list_init(&result->drop);

		struct packet *packet;
		while ((packet = packet_list_pop(input)) != NULL) {
			packet_list_add(&result->drop, packet);
		}
		return;
	}

	// The harness only manages one instance, so dp_workers lives in
	// dp_config->workers as a registry of size worker_count.
	struct dp_worker **workers = ADDR_OF(&ut->dp_config->workers);
	struct dp_worker *dp_worker = ADDR_OF(&workers[worker_idx]);

	struct worker_round round = worker_round_prepare(
		dp_worker, ut->cp_config, ut->mock_time_ns
	);
	struct cp_config_gen *cp_config_gen = round.cp_config_gen;
	struct config_gen_ectx *config_gen_ectx = round.config_gen_ectx;

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	packet_list_init(&result->output);
	packet_list_init(&result->drop);

	if (config_gen_ectx == NULL) {
		// No active pipeline: drop everything.
		struct packet *packet;
		while ((packet = packet_list_pop(input)) != NULL) {
			packet_list_add(&result->drop, packet);
		}
		return;
	}

	// Feed input straight onto each packet's target device input entry,
	// the same way worker_read deposits RX into the pipeline.
	struct packet *packet;
	while ((packet = packet_list_pop(input)) != NULL) {
		struct device_ectx *device_ectx = config_gen_ectx_get_device(
			config_gen_ectx, packet->tx_device_id
		);
		if (device_ectx == NULL) {
			packet_front_drop(&packet_front, packet);
			continue;
		}
		device_entry_ectx_schedule(
			config_gen_ectx,
			ADDR_OF(&device_ectx->input_pipelines),
			packet
		);
	}

	worker_pipeline_round(
		dp_worker, cp_config_gen, config_gen_ectx, &packet_front
	);

	packet_list_concat(&result->output, &packet_front.output);
	packet_list_concat(&result->drop, &packet_front.drop);
}

// Saved per-packet state used by dataplane_ut_run_rounds.
struct saved_packet {
	struct packet *pkt;
	struct packet state;
	uint8_t *data;
	uint32_t data_len;
	uint16_t segment_count;
	uint16_t first_data_off;
	uint16_t first_data_len;
};

static bool
saved_packet_contains(
	const struct saved_packet *saved,
	size_t count,
	const struct packet *packet
) {
	for (size_t idx = 0; idx < count; ++idx) {
		if (saved[idx].pkt == packet) {
			return true;
		}
	}
	return false;
}

static void
saved_packet_list_free_extra(
	struct packet_list *list, const struct saved_packet *saved, size_t count
) {
	struct packet *packet;
	while ((packet = packet_list_pop(list)) != NULL) {
		if (saved_packet_contains(saved, count, packet)) {
			continue;
		}

		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (mbuf->pool != NULL) {
			rte_pktmbuf_free(mbuf);
		} else {
			free_packet(packet);
		}
	}
}

static int
saved_packet_restore(struct saved_packet *saved) {
	*saved->pkt = saved->state;
	saved->pkt->next = NULL;

	struct rte_mbuf *mbuf = packet_to_mbuf(saved->pkt);
	if (rte_pktmbuf_pkt_len(mbuf) != saved->data_len ||
	    mbuf->nb_segs != saved->segment_count ||
	    mbuf->data_off != saved->first_data_off ||
	    mbuf->data_len != saved->first_data_len) {
		return -EINVAL;
	}
	if (saved->data == NULL) {
		return 0;
	}

	uint32_t offset = 0;
	for (struct rte_mbuf *segment = mbuf; segment != NULL;
	     segment = segment->next) {
		if (segment->data_len > saved->data_len - offset) {
			return -EINVAL;
		}
		memcpy(rte_pktmbuf_mtod(segment, void *),
		       saved->data + offset,
		       segment->data_len);
		offset += segment->data_len;
	}
	return offset == saved->data_len ? 0 : -EINVAL;
}

static int
saved_packets_rebuild(
	struct saved_packet *saved, size_t count, struct packet_list *input
) {
	packet_list_init(input);
	int result = 0;
	for (size_t idx = 0; idx < count; ++idx) {
		int rc = saved_packet_restore(&saved[idx]);
		if (result == 0 && rc != 0) {
			result = rc;
		}
		packet_list_add(input, saved[idx].pkt);
	}
	return result;
}

int
dataplane_ut_run_rounds(
	struct dataplane_ut *ut,
	size_t worker,
	struct packet_list *input,
	uint64_t rounds,
	int reset_payload
) {
	size_t count = (size_t)packet_list_count(input);
	if (rounds == 0 || count == 0) {
		return 0;
	}

	struct saved_packet *saved = calloc(count, sizeof(struct saved_packet));
	if (saved == NULL) {
		return -ENOMEM;
	}

	int result = 0;
	int captured = 0;
	size_t snapshot_count = 0;

	// Capture before draining input so allocation failures leave it intact.
	struct packet *pkt = packet_list_first(input);
	for (size_t idx = 0; idx < count; ++idx) {
		saved[idx].pkt = pkt;
		saved[idx].state = *pkt;
		snapshot_count = idx + 1;
		struct rte_mbuf *mbuf = packet_to_mbuf(pkt);
		saved[idx].data_len = rte_pktmbuf_pkt_len(mbuf);
		saved[idx].segment_count = mbuf->nb_segs;
		saved[idx].first_data_off = mbuf->data_off;
		saved[idx].first_data_len = mbuf->data_len;
		if (reset_payload) {
			saved[idx].data = malloc(saved[idx].data_len);
			if (saved[idx].data == NULL) {
				result = -ENOMEM;
				goto cleanup;
			}

			uint32_t offset = 0;
			for (struct rte_mbuf *segment = mbuf; segment != NULL;
			     segment = segment->next) {
				if (segment->data_len >
				    saved[idx].data_len - offset) {
					result = -EINVAL;
					goto cleanup;
				}
				memcpy(saved[idx].data + offset,
				       rte_pktmbuf_mtod(segment, void *),
				       segment->data_len);
				offset += segment->data_len;
			}
			if (offset != saved[idx].data_len) {
				result = -EINVAL;
				goto cleanup;
			}
		}
		pkt = pkt->next;
	}
	captured = 1;

	// The saved packet set must remain allocated and each round must return
	// every saved packet through output or drop. Newly emitted packets are
	// reclaimed below.
	for (uint64_t round = 0; round < rounds; ++round) {
		result = saved_packets_rebuild(saved, count, input);
		if (result != 0) {
			break;
		}

		struct dataplane_ut_round_result round_result;
		dataplane_ut_run(ut, worker, input, &round_result);
		saved_packet_list_free_extra(
			&round_result.output, saved, count
		);
		saved_packet_list_free_extra(&round_result.drop, saved, count);
	}

	// Leave input holding all packets so the caller can free them.
cleanup:
	if (captured) {
		int restore_result = saved_packets_rebuild(saved, count, input);
		if (result == 0) {
			result = restore_result;
		}
	}
	for (size_t idx = 0; idx < snapshot_count; ++idx) {
		free(saved[idx].data);
	}

	free(saved);
	return result;
}

int
dataplane_ut_build_optimized(void) {
	return build_is_optimized();
}
