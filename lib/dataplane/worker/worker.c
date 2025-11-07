#include "worker.h"
#include <rte_mbuf.h>

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
