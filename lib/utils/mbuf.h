#pragma once

#include "rte_mbuf.h"

static inline void
mbuf_copy(struct rte_mbuf *dst, struct rte_mbuf *src) {
	void *src_data = rte_pktmbuf_mtod(src, void *);
	void *dst_data = rte_pktmbuf_mtod(dst, void *);
	uint32_t data_len = rte_pktmbuf_data_len(src);

	rte_memcpy(dst_data, src_data, data_len);

	dst->data_len = data_len;
	dst->pkt_len = src->pkt_len;

	dst->ol_flags = src->ol_flags;
	dst->packet_type = src->packet_type;
	dst->vlan_tci = src->vlan_tci;
	dst->tx_offload = src->tx_offload;
}