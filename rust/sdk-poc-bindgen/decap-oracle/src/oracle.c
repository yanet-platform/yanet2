// C reference for the Rust decap module: the unmodified C module source
// plus construction of parsed packets over heap mbufs.

#include "modules/decap/dataplane/dataplane.c"

#include <stdlib.h>
#include <string.h>

#include <rte_mbuf_core.h>

struct packet *
decap_oracle_packet_new(const uint8_t *frame, uint16_t len) {
	struct rte_mbuf *mbuf = aligned_alloc(64, sizeof(struct rte_mbuf));
	uint16_t buf_len = RTE_PKTMBUF_HEADROOM + len;
	uint8_t *buf = malloc(buf_len);
	if (mbuf == NULL || buf == NULL) {
		free(mbuf);
		free(buf);
		return NULL;
	}
	memset(mbuf, 0, sizeof(*mbuf));
	memset(buf, 0, RTE_PKTMBUF_HEADROOM);
	memcpy(buf + RTE_PKTMBUF_HEADROOM, frame, len);
	mbuf->buf_addr = buf;
	mbuf->buf_len = buf_len;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->data_len = len;
	mbuf->pkt_len = len;
	mbuf->nb_segs = 1;
	mbuf->refcnt = 1;

	// As the worker does on receive.
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	if (parse_packet(packet) != 0) {
		free(buf);
		free(mbuf);
		return NULL;
	}
	return packet;
}

void
decap_oracle_packet_free(struct packet *packet) {
	struct rte_mbuf *mbuf = packet->mbuf;
	free(mbuf->buf_addr);
	free(mbuf);
}

const uint8_t *
decap_oracle_packet_data(struct packet *packet, uint16_t *len) {
	*len = rte_pktmbuf_data_len(packet->mbuf);
	return rte_pktmbuf_mtod(packet->mbuf, const uint8_t *);
}

void
decap_oracle_handle(struct module_ectx *ectx, struct packet_front *front) {
	decap_handle_packets(NULL, ectx, front);
}
