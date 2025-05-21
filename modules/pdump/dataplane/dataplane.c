#include "config.h"

#include <bpf_impl.h>
#include <rte_bpf.h>

#include "dataplane/config/zone.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "logging/log.h"

#include "ring.h"

static inline void
pdump_write_msg(
	struct ring_buffer *ring,
	uint8_t *ring_data,
	struct ring_msg_hdr *hdr,
	uint8_t *payload
) {
	// Step 1. Move readable_idx to make space for new data
	pdump_ring_prepare(ring, ring_data, hdr->total_len);

	// Step 2. Write ring_msg_hdr struct
	uint64_t hdr_size = sizeof(*hdr);
	pdump_ring_write(ring, ring_data, 0, (uint8_t *)hdr, hdr_size);

	// Step 3. Write mbuf data to an offset equal to hdr_size.
	uint64_t payload_size = hdr->total_len - hdr_size;
	pdump_ring_write(ring, ring_data, hdr_size, payload, payload_size);

	// Step 4. Store write_idx atomically, considering alignment.
	pdump_ring_checkpoint(ring, hdr->total_len);
}

static inline void
pdump_msg_header(
	struct ring_msg_hdr *hdr,
	struct packet *pkt,
	uint32_t worker_idx,
	uint32_t snaplen,
	bool is_drops
) {
	memset(hdr, 0, sizeof(*hdr));

	struct rte_mbuf *mbuf = packet_to_mbuf(pkt);

	// FIXME: add timestamp
	// NOTE: We do not support multi-segment mbuf;
	// therefore, data_len must equal pkt_len.
	uint16_t packet_len = rte_pktmbuf_data_len(mbuf);
	uint32_t capture_len = packet_len > snaplen ? snaplen : packet_len;
	hdr->packet_len = packet_len;
	hdr->total_len = sizeof(*hdr) + capture_len;
	hdr->worker_idx = worker_idx;
	hdr->pipeline_idx = pkt->pipeline_idx;
	hdr->rx_device_id = pkt->rx_device_id;
	hdr->tx_device_id = pkt->tx_device_id;
	hdr->is_drops = is_drops;
}

void
pdump_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)counter_storage;

	struct pdump_module_config *config =
		container_of(cp_module, struct pdump_module_config, cp_module);

	struct ring_buffer *ring = ADDR_OF(&config->rings) + worker_idx;
	uint8_t *ring_data = ADDR_OF(&ring->data);

	struct rte_bpf *bpf_shm = ADDR_OF(&config->ebpf_program);
	struct rte_bpf bpf = *bpf_shm;

	bpf.prm.ins = ADDR_OF(&bpf_shm->prm.ins);
	bpf.prm.xsym = NULL;
	bpf.prm.nb_xsym = 0;

	struct packet *pkt;
	struct ring_msg_hdr hdr;

	// First, process dropped packets.
	if (config->mode & PDUMP_DROPS) {
		for (pkt = packet_front->drop.first; pkt != NULL;
		     pkt = pkt->next) {
			struct rte_mbuf *mbuf = packet_to_mbuf(pkt);

			int rc = rte_bpf_exec(&bpf, (void *)mbuf);
			if (rc) {
				LOG_TRACE(
					"capturing packet from the drop queue"
				);

				pdump_msg_header(
					&hdr,
					pkt,
					// Assume a maximum of 4 million
					// workers.
					(uint32_t)worker_idx,
					config->snaplen,
					true
				);
				uint8_t *payload =
					rte_pktmbuf_mtod(mbuf, uint8_t *);
				pdump_write_msg(ring, ring_data, &hdr, payload);

			} else {
				LOG_TRACE("skip packet from the drop queue");
			}
		}
	}

	// Then process the input packets.
	if (config->mode & PDUMP_INPUT) {
		for (pkt = packet_front->input.first; pkt != NULL;
		     pkt = pkt->next) {
			struct rte_mbuf *mbuf = packet_to_mbuf(pkt);

			int rc = rte_bpf_exec(&bpf, (void *)mbuf);
			if (rc) {
				LOG_TRACE(
					"capturing packet from the input queue"
				);

				pdump_msg_header(
					&hdr,
					pkt,
					worker_idx,
					config->snaplen,
					false
				);
				uint8_t *payload =
					rte_pktmbuf_mtod(mbuf, uint8_t *);
				pdump_write_msg(ring, ring_data, &hdr, payload);
			} else {
				LOG_TRACE("skip packet from the input queue");
			}
		}
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
