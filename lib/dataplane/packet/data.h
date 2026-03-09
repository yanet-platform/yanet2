#pragma once

#include "packet.h"

/*
 * Include mbuf header in order to inline packet handling routines like
 * packet data and packet data length retrieval.
 */
#include <rte_mbuf.h>

static inline struct rte_mbuf *
packet_to_mbuf(const struct packet *packet) {
	return packet->mbuf;
}

static inline struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf) {
	return (struct packet *)((void *)mbuf->buf_addr);
}

static inline void *
packet_data(struct packet *packet) {
	return rte_pktmbuf_mtod(packet_to_mbuf(packet), char *);
}

static inline uint16_t
packet_data_len(struct packet *packet) {
	return rte_pktmbuf_data_len(packet_to_mbuf(packet));
}
