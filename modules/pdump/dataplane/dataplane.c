#include "config.h"

#include <string.h>

#include <bpf_impl.h>
#include <rte_bpf.h>

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "common/likely.h"

#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"

#include "objects/ring/api/ring_object.h"

#include "record.h"

// Per-worker state of one execution context, set by the context commit
// handler for the worker that runs the context.
//
// The ring and its data area are the linked ring object's entry for that
// worker; the filter is the config's program with its absolute
// instruction address. A NULL ring leaves capture disabled.
struct pdump_prepared {
	struct ring_worker *ring;
	uint8_t *ring_data;
	struct rte_bpf bpf;
};

// Worker index of an execution context whose worker is not known.
#define PDUMP_WORKER_IDX_NONE UINT64_MAX

// Find the index of the worker whose execution context holds the given
// module context.
//
// A context commit handler is not told its worker: the generation keeps
// one context per worker at the worker's index, so the index is where the
// module's own context appears. Returns PDUMP_WORKER_IDX_NONE when the
// context is not found. A copy of the fwstate helper of the same purpose.
static inline uint64_t
pdump_worker_idx(struct module_ectx *module_ectx) {
	struct config_gen_ectx *own = module_ectx->abs_config_gen_ectx;
	if (own == NULL) {
		return PDUMP_WORKER_IDX_NONE;
	}
	struct cp_config_gen *config_gen = ADDR_OF(&own->cp_config_gen);
	if (config_gen == NULL) {
		return PDUMP_WORKER_IDX_NONE;
	}
	for (uint64_t idx = 0; idx < config_gen->config_gen_ectx_count; ++idx) {
		if (cp_config_gen_worker_ectx(config_gen, idx) == own) {
			return idx;
		}
	}
	return PDUMP_WORKER_IDX_NONE;
}

// Write one capture record: the ring frame, the metadata, then the
// captured bytes, front to back.
//
// A record larger than the ring's max record size is refused and the
// packet stays uncaptured. A full ring never refuses: it evicts published
// records instead. The captured length fits 16 bits, so the record length
// cannot wrap.
static inline void
pdump_ring_write_msg(
	struct ring_worker *ring,
	uint8_t *ring_data,
	const struct pdump_record_hdr *hdr,
	const uint8_t *payload,
	uint16_t capture_len
) {
	uint32_t total_len =
		RING_RECORD_FRAME_SIZE + sizeof(*hdr) + capture_len;
	if (ring_worker_prepare(ring, ring_data, total_len) != 0) {
		return;
	}
	ring_worker_write(
		ring,
		ring_data,
		RING_RECORD_FRAME_SIZE,
		(const uint8_t *)hdr,
		sizeof(*hdr)
	);
	ring_worker_write(
		ring,
		ring_data,
		RING_RECORD_FRAME_SIZE + sizeof(*hdr),
		payload,
		capture_len
	);
	ring_worker_commit(ring, total_len);
}

static inline void
process_queue(
	struct packet *first_pkt,
	const struct rte_bpf *bpf,
	struct ring_worker *ring,
	uint8_t *ring_data,
	const struct dp_worker *dp_worker,
	uint32_t snaplen,
	enum pdump_mode queue
) {
	// Stamp every record from the worker clock, which is the same time
	// base the rest of the dataplane uses. Hardware RX timestamps are not
	// portable nanoseconds: mlx5 hands over a raw device counter unless
	// the NIC runs in real-time mode, so they cannot share this field.
	uint64_t timestamp = dp_worker->current_time;

	for (struct packet *pkt = first_pkt; pkt != NULL; pkt = pkt->next) {
		struct rte_mbuf *mbuf = packet_to_mbuf(pkt);

		int rc = rte_bpf_exec(bpf, (void *)mbuf);
		if (!rc) {
			continue;
		}

		// NOTE: We do not support multi-segment mbuf;
		// therefore, data_len must equal pkt_len.
		uint16_t packet_len = rte_pktmbuf_data_len(mbuf);
		uint16_t capture_len =
			packet_len > snaplen ? (uint16_t)snaplen : packet_len;
		struct pdump_record_hdr hdr = {
			.magic = PDUMP_RECORD_MAGIC,
			.packet_len = packet_len,
			.timestamp = timestamp,
			.worker_idx = (uint32_t)dp_worker->idx,
			// FIXME
			// .pipeline_idx = pkt->pipeline_idx,
			.rx_device_id = pkt->rx_device_id,
			.tx_device_id = pkt->tx_device_id,
			.queue = (uint8_t)queue,
		};

		pdump_ring_write_msg(
			ring,
			ring_data,
			&hdr,
			rte_pktmbuf_mtod(mbuf, uint8_t *),
			capture_len
		);
	}
}

void
pdump_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct pdump_module_config *config = container_of(
		module_ectx->abs_cp_module,
		struct pdump_module_config,
		cp_module
	);

	// The worker's ring and the filter were resolved when the context was
	// committed. A context with no resolved ring captures nothing.
	const struct pdump_prepared *prepared =
		module_ectx->abs_module_prepared;
	struct ring_worker *ring = prepared->ring;
	if (likely(ring != NULL)) {
		uint8_t *ring_data = prepared->ring_data;
		const struct rte_bpf *bpf = &prepared->bpf;

		// First, process dropped packets.
		if (config->mode & PDUMP_DROPS &&
		    packet_front->drop.first != NULL) {
			process_queue(
				packet_front->drop.first,
				bpf,
				ring,
				ring_data,
				dp_worker,
				config->snaplen,
				PDUMP_DROPS
			);
		}

		// Then process the input packets.
		if (config->mode & PDUMP_INPUT &&
		    packet_front->input.first != NULL) {
			process_queue(
				packet_front->input.first,
				bpf,
				ring,
				ring_data,
				dp_worker,
				config->snaplen,
				PDUMP_INPUT
			);
		}

		// Publish once per call, after both queues, so a call that
		// commits fewer records than the publish batch still leaves
		// them readable when it returns.
		ring_worker_publish(ring);
	}

	// We should always pass the packets in the input queue
	packet_front_pass(packet_front);
}

struct pdump_module {
	struct module module;
};

static void
pdump_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

// Resolve an execution context's worker entry in the linked ring and the
// config's filter.
//
// An absent link, an unresolved object, a worker past the ring's own
// count or a missing filter give a zeroed result with a NULL ring, so the
// context captures nothing.
static struct pdump_prepared
pdump_resolve_prepared(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct pdump_prepared result = {0};

	struct pdump_module_config *config =
		container_of(cp_module, struct pdump_module_config, cp_module);
	struct rte_bpf *bpf = ADDR_OF(&config->ebpf_program);
	if (bpf == NULL || config->ring_link_idx == PDUMP_RING_LINK_NONE) {
		return result;
	}
	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, config->ring_link_idx);
	if (link == NULL || link->abs_object_ectx == NULL) {
		return result;
	}
	struct cp_object *ring_object = link->abs_object_ectx->abs_cp_object;
	if (ring_object == NULL) {
		return result;
	}
	uint64_t worker_idx = pdump_worker_idx(module_ectx);
	struct ring_worker *ring = ring_object_worker(ring_object, worker_idx);
	if (ring == NULL) {
		return result;
	}

	result.bpf = *bpf;
	result.bpf.prm.ins = ADDR_OF(&bpf->prm.ins);
	result.bpf.prm.xsym = NULL;
	result.bpf.prm.nb_xsym = 0;
	result.ring_data = ADDR_OF(&ring->local.data);
	result.ring = ring;
	return result;
}

// Link an execution context to its worker's entry in the linked ring and
// to the config's filter.
//
// The result is stored with one assignment, so a repeated pass over a
// context a worker already runs rewrites the same bytes and never leaves
// a half-cleared buffer.
static void
pdump_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct pdump_prepared *prepared = module_ectx->abs_module_prepared;
	if (prepared == NULL) {
		return;
	}
	*prepared = pdump_resolve_prepared(module_ectx, cp_module);
}

struct module *
new_module_pdump() {
	struct pdump_module *module =
		(struct pdump_module *)malloc(sizeof(struct pdump_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "pdump"
	);
	module->module.handler = pdump_handle_packets;
	module->module.commit_handler = pdump_module_commit;
	module->module.commit_ectx_handler = pdump_module_commit_ectx;
	module->module.prepared_size = sizeof(struct pdump_prepared);

	return &module->module;
}
