#include "worker.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include <string.h>

struct packet *
worker_packet_alloc(struct dp_worker *dp_worker) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(dp_worker->rx_mempool);
	if (mbuf == NULL) {
		return NULL;
	}

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;

	return packet;
}

struct packet *
worker_clone_packet(
	struct dp_worker *dp_worker,
	struct packet *packet,
	uint16_t packet_recirc_limit
) {
	struct rte_mbuf *mbuf = rte_pktmbuf_copy(
		packet->mbuf, dp_worker->rx_mempool, 0, UINT32_MAX
	);
	if (mbuf == NULL) {
		return NULL;
	}

	struct packet *packet_clone = mbuf_to_packet(mbuf);
	// Order is load-bearing: init the source budget first (no-op when the
	// lineage is already initialized, so an already-spent source splits its
	// remainder), snapshot into the clone, then partition floor/ceil. A
	// swap of init and memcpy would copy uninitialized fields and re-init
	// the clone to the full limit on its first redirect.
	packet_recirc_init(packet, packet_recirc_limit);
	rte_memcpy(packet_clone, packet, sizeof(struct packet));
	packet_clone->mbuf = mbuf;
	packet_clone->next = NULL;
	packet_clone->recirc_remaining = packet->recirc_remaining / 2;
	packet->recirc_remaining -= packet_clone->recirc_remaining;

	packet_refresh_data_len(packet_clone);
	return packet_clone;
}

void
worker_packet_free(struct packet *packet) {
	rte_pktmbuf_free(packet->mbuf);
}
