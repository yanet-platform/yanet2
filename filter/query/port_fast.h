#pragma once

#include "classifiers/port_fast.h"
#include "common/btree/u32.h"
#include "common/memory_address.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>

#include "declare.h"

static inline void
FILTER_ATTR_QUERY_FUNC(port_fast_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct port_fast_classifier *classifier =
		(struct port_fast_classifier *)data;
	uint32_t *to = ADDR_OF(&classifier->to);
	uint32_t values[btree_u32_max_batch_size];
	while (count >= btree_u32_max_batch_size) {
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			uint16_t port = 0;
			if (packets[i]->transport_header.type == IPPROTO_TCP) {
				struct rte_tcp_hdr *tcp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						packets[i]
							->transport_header
							.offset
					);
				port = rte_be_to_cpu_16(tcp_hdr->dst_port);
			} else if (packets[i]->transport_header.type ==
				   IPPROTO_UDP) {
				struct rte_udp_hdr *udp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						packets[i]
							->transport_header
							.offset
					);
				port = rte_be_to_cpu_16(udp_hdr->dst_port);
			}
			values[i] = port + 1;
		}
		btree_u32_lower_bounds(
			&classifier->btree,
			values,
			btree_u32_max_batch_size,
			result
		);
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			if (unlikely(
				    result[i] == 0 ||
				    to[result[i] - 1] < values[i] - 1
			    )) {
				result[i] = classifier->btree.n;
			} else {
				--result[i];
			}
		}
		result += btree_u32_max_batch_size;
		packets += btree_u32_max_batch_size;
		count -= btree_u32_max_batch_size;
	}
	count %= btree_u32_max_batch_size;
	for (size_t i = 0; i < count; ++i) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
		uint16_t port = 0;
		if (packets[i]->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packets[i]->transport_header.offset
			);
			port = rte_be_to_cpu_16(tcp_hdr->dst_port);
		} else if (packets[i]->transport_header.type == IPPROTO_UDP) {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packets[i]->transport_header.offset
			);
			port = rte_be_to_cpu_16(udp_hdr->dst_port);
		}
		values[i] = port + 1;
	}
	btree_u32_lower_bounds(&classifier->btree, values, count, result);
	for (size_t i = 0; i < count; ++i) {
		if (unlikely(
			    result[i] == 0 || to[result[i] - 1] < values[i] - 1
		    )) {
			result[i] = classifier->btree.n;
		} else {
			--result[i];
		}
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(port_fast_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct port_fast_classifier *classifier =
		(struct port_fast_classifier *)data;
	uint32_t *to = ADDR_OF(&classifier->to);
	uint32_t values[btree_u32_max_batch_size];
	while (count >= btree_u32_max_batch_size) {
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			uint16_t port = 0;
			if (packets[i]->transport_header.type == IPPROTO_TCP) {
				struct rte_tcp_hdr *tcp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						packets[i]
							->transport_header
							.offset
					);
				port = rte_be_to_cpu_16(tcp_hdr->src_port);
			} else if (packets[i]->transport_header.type ==
				   IPPROTO_UDP) {
				struct rte_udp_hdr *udp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						packets[i]
							->transport_header
							.offset
					);
				port = rte_be_to_cpu_16(udp_hdr->src_port);
			}
			values[i] = port + 1;
		}
		btree_u32_lower_bounds(
			&classifier->btree,
			values,
			btree_u32_max_batch_size,
			result
		);
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			if (unlikely(
				    result[i] == 0 ||
				    to[result[i] - 1] < values[i] - 1
			    )) {
				result[i] = classifier->btree.n;
			} else {
				--result[i];
			}
		}
		result += btree_u32_max_batch_size;
		packets += btree_u32_max_batch_size;
		count -= btree_u32_max_batch_size;
	}
	count %= btree_u32_max_batch_size;
	for (size_t i = 0; i < count; ++i) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
		uint16_t port = 0;
		if (packets[i]->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packets[i]->transport_header.offset
			);
			port = rte_be_to_cpu_16(tcp_hdr->src_port);
		} else if (packets[i]->transport_header.type == IPPROTO_UDP) {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packets[i]->transport_header.offset
			);
			port = rte_be_to_cpu_16(udp_hdr->src_port);
		}
		values[i] = port + 1;
	}
	btree_u32_lower_bounds(&classifier->btree, values, count, result);
	for (size_t i = 0; i < count; ++i) {
		if (unlikely(
			    result[i] == 0 || to[result[i] - 1] < values[i] - 1
		    )) {
			result[i] = classifier->btree.n;
		} else {
			--result[i];
		}
	}
}