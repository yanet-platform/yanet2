#include "config.h"

#include <bpf_impl.h>
#include <rte_bpf.h>
#include <rte_mbuf_dyn.h>

#include "dataplane/config/zone.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

#include "controlplane/config/econtext.h"

#include "dataplane/time/tsc.h"
#include "ring.h"

#define TSC_SHIFT 32

static inline bool
mbuf_is_timestamp_enabled(const struct rte_mbuf *mbuf) {
	static uint64_t timestamp_rx_dynflag;

	if (timestamp_rx_dynflag == 0) {
		int timestamp_rx_dynflag_offset = rte_mbuf_dynflag_lookup(
			RTE_MBUF_DYNFLAG_RX_TIMESTAMP_NAME, NULL
		);
		if (timestamp_rx_dynflag_offset < 0)
			return false;
		timestamp_rx_dynflag = RTE_BIT64(timestamp_rx_dynflag_offset);
	}

	return (mbuf->ol_flags & timestamp_rx_dynflag) != 0;
}

static inline rte_mbuf_timestamp_t
mbuf_get_timestamp(const struct rte_mbuf *mbuf) {
	static int timestamp_dynfield_offset = -1;

	if (timestamp_dynfield_offset < 0) {
		timestamp_dynfield_offset = rte_mbuf_dynfield_lookup(
			RTE_MBUF_DYNFIELD_TIMESTAMP_NAME, NULL
		);
		if (timestamp_dynfield_offset < 0)
			return 0;
	}

	return *RTE_MBUF_DYNFIELD(mbuf, timestamp_dynfield_offset, rte_mbuf_timestamp_t *);
}

static inline void
process_queue(
	struct packet *first_pkt,
	struct rte_bpf *bpf,
	struct ring_buffer *ring,
	uint32_t worker_idx,
	uint32_t snaplen,
	enum pdump_mode queue
) {
	uint64_t tsc_timestamp = ~0ULL;

	uint8_t *ring_data = ADDR_OF(&ring->data);

	for (struct packet *pkt = first_pkt; pkt != NULL; pkt = pkt->next) {
		struct rte_mbuf *mbuf = packet_to_mbuf(pkt);

		int rc = rte_bpf_exec(bpf, (void *)mbuf);
		if (rc) {
			uint64_t timestamp;
			if (mbuf_is_timestamp_enabled(mbuf)) {
				timestamp = mbuf_get_timestamp(mbuf);
			} else {
				// Fallback to the TSC timestamp for the entire
				// packet list.
				if (tsc_timestamp == ~0ULL) {
					tsc_timestamp = tsc_timestamp_ns();
				}
				timestamp = tsc_timestamp;
			}

			// NOTE: We do not support multi-segment mbuf;
			// therefore, data_len must equal pkt_len.
			uint16_t packet_len = rte_pktmbuf_data_len(mbuf);
			uint32_t capture_len =
				packet_len > snaplen ? snaplen : packet_len;
			struct ring_msg_hdr hdr = {
				.total_len = sizeof(hdr) + capture_len,
				.magic = RING_MSG_MAGIC,
				.packet_len = packet_len,
				.timestamp = timestamp,
				.worker_idx = worker_idx,
				// FIXME
				// .pipeline_idx = pkt->pipeline_idx,
				.rx_device_id = pkt->rx_device_id,
				.tx_device_id = pkt->tx_device_id,
				.queue = (uint8_t)queue
			};

			uint8_t *payload = rte_pktmbuf_mtod(mbuf, uint8_t *);
			pdump_ring_write_msg(ring, ring_data, &hdr, payload);
		}
	}
}

void
pdump_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct pdump_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct pdump_module_config,
		cp_module
	);

	struct ring_buffer *ring = ADDR_OF(&config->rings) + dp_worker->idx;

	struct rte_bpf *bpf_shm = ADDR_OF(&config->ebpf_program);
	struct rte_bpf bpf = *bpf_shm;

	bpf.prm.ins = ADDR_OF(&bpf_shm->prm.ins);
	bpf.prm.xsym = NULL;
	bpf.prm.nb_xsym = 0;

	// First, process dropped packets.
	if (config->mode & PDUMP_DROPS && packet_front->drop.first != NULL) {
		process_queue(
			packet_front->drop.first,
			&bpf,
			ring,
			dp_worker->idx,
			config->snaplen,
			PDUMP_DROPS
		);
	}

	// Then process the input packets.
	if (config->mode & PDUMP_INPUT && packet_front->input.first != NULL) {
		process_queue(
			packet_front->input.first,
			&bpf,
			ring,
			dp_worker->idx,
			config->snaplen,
			PDUMP_INPUT
		);
	}
	// We should always pass the packets in the input queue
	packet_front_pass(packet_front);
}

struct pdump_module {
	struct module module;
};

struct module *
new_module_pdump() {
	struct pdump_module *module =
		(struct pdump_module *)malloc(sizeof(struct pdump_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "pdump"
	);
	module->module.handler = pdump_handle_packets;

	return &module->module;
}
