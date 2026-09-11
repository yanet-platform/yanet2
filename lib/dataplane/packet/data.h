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

static inline uint16_t
packet_data_offset(struct packet *packet) {
	return packet_to_mbuf(packet)->data_off;
}

// Grows the frame upwards and returns NULL once the room above the packet
// descriptor sharing the headroom is spent.
static inline char *
packet_headroom_prepend(struct rte_mbuf *mbuf, uint16_t len) {
	if (rte_pktmbuf_headroom(mbuf) < sizeof(struct packet) + len) {
		return NULL;
	}
	return rte_pktmbuf_prepend(mbuf, len);
}

// A headroom too small for the descriptor would refuse every prepend.
_Static_assert(
	RTE_PKTMBUF_HEADROOM > sizeof(struct packet),
	"the packet descriptor must fit inside the mbuf headroom"
);

// Refresh the cached first-segment length after the mbuf was resized.
//
// Call this only while the packet is not linked in a counted packet_front
// list (between a pop and the next counted add): packet_front byte sums are
// kept in sync incrementally from data_len, so refreshing it in place after
// the packet has been counted would desync that front's cached byte sum.
static inline void
packet_refresh_data_len(struct packet *packet) {
	packet->data_len = rte_pktmbuf_data_len(packet_to_mbuf(packet));
}
