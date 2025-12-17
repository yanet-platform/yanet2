#include "worker.h"

#include "../../utils/mbuf.h"

struct packet *
worker_packet_alloc(struct dp_worker *dp_worker) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(dp_worker->rx_mempool);
	if (mbuf == NULL) {
		return NULL;
	}

	struct packet *packet = mbuf_to_packet(mbuf);
	packet->mbuf = mbuf;

	return packet;
}

struct packet *
worker_clone_packet(struct dp_worker *dp_worker, struct packet *packet) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(dp_worker->rx_mempool);
	if (mbuf == NULL) {
		return NULL;
	}

	struct packet *packet_clone = mbuf_to_packet(mbuf);
	rte_memcpy(packet_clone, packet, sizeof(struct packet));
	packet_clone->mbuf = mbuf;
	packet_clone->next = NULL;

	mbuf_copy(packet_clone->mbuf, packet->mbuf);
	return packet_clone;
}

void
worker_packet_free(struct packet *packet) {
	rte_pktmbuf_free(packet->mbuf);
}
