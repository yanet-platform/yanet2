#pragma once

/*
 * Module authored attribute lookups of the forward filters.
 *
 * Each routine reads the classifier the compile side produced and
 * yields the class of every packet of the batch; the network getters
 * are batched so the parsing loops amortize over the batch. The
 * arrays at the bottom pair with the classifier builds of
 * modules/forward/api/controlplane.c by tape leaf order.
 */

#include <rte_ip.h>
#include <rte_mbuf.h>
#include <stdint.h>
#include <string.h>

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/vlan.h"
#include "lib/classify/classify.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

static inline void
fwd_lookup_device(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct classify_query_attr_device *device_attr =
		container_of(attr, struct classify_query_attr_device, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		uint32_t device_id = packets[idx]->module_device_id;
		if (device_id >= device_attr->value_table.h_dim) {
			device_id = 0;
		}
		results[idx] = value_table_get(
			&device_attr->value_table, 0, device_id
		);
	}
}

static inline void
fwd_lookup_vlan(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct classify_query_attr_vlan *vlan_attr =
		container_of(attr, struct classify_query_attr_vlan, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t vlan_id = packets[idx]->vlan;
		results[idx] =
			value_table_get(&vlan_attr->value_table, 0, vlan_id);
	}
}

static inline void
fwd_packet_get_net4_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->src_addr, NET4_LEN);
	}
}

static inline void
fwd_packet_get_net4_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->dst_addr, NET4_LEN);
	}
}

#define FWD_LOOKUP_NET4(variant, getter)                                       \
	static inline void fwd_lookup_##variant(                               \
		const struct classify_query_attr *attr,                        \
		const struct classify_query_attr_handlers *handlers,           \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct classify_query_attr_net4 *attr_net4 =             \
			container_of(                                          \
				attr, struct classify_query_attr_net4, attr    \
			);                                                     \
		uint8_t addrs[packet_count][NET4_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] =                                         \
				lpm4_lookup(&attr_net4->lpm, addrs[idx]);      \
		}                                                              \
	}

FWD_LOOKUP_NET4(net4_src, fwd_packet_get_net4_src_batch)
FWD_LOOKUP_NET4(net4_dst, fwd_packet_get_net4_dst_batch)

static inline void
fwd_packet_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->src_addr, NET6_LEN);
	}
}

static inline void
fwd_packet_get_net6_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr, NET6_LEN);
	}
}

#define FWD_LOOKUP_NET6(variant, getter)                                       \
	static inline void fwd_lookup_##variant(                               \
		const struct classify_query_attr *attr,                        \
		const struct classify_query_attr_handlers *handlers,           \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct classify_query_attr_net6 *attr_net6 =             \
			container_of(                                          \
				attr, struct classify_query_attr_net6, attr    \
			);                                                     \
		uint8_t addrs[packet_count][NET6_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			const uint8_t *addr = addrs[idx];                      \
			uint32_t hi = lpm8_lookup(&attr_net6->hi, addr);       \
			if (!(hi & FILTER_NET6_ROW_MARK)) {                    \
				results[idx] = hi;                             \
				continue;                                      \
			}                                                      \
			uint32_t lo = lpm8_lookup(&attr_net6->lo, addr + 8);   \
			results[idx] = *value_table_get_ptr(                   \
				&attr_net6->comb,                              \
				hi & ~FILTER_NET6_ROW_MARK,                    \
				lo                                             \
			);                                                     \
		}                                                              \
	}

FWD_LOOKUP_NET6(net6_src, fwd_packet_get_net6_src_batch)
FWD_LOOKUP_NET6(net6_dst, fwd_packet_get_net6_dst_batch)

CLASSIFY_QUERY_ATTR(fwd_attr_device, fwd_lookup_device)
CLASSIFY_QUERY_ATTR(fwd_attr_vlan, fwd_lookup_vlan)
CLASSIFY_QUERY_ATTR(fwd_attr_net4_src, fwd_lookup_net4_src)
CLASSIFY_QUERY_ATTR(fwd_attr_net4_dst, fwd_lookup_net4_dst)
CLASSIFY_QUERY_ATTR(fwd_attr_net6_src, fwd_lookup_net6_src)
CLASSIFY_QUERY_ATTR(fwd_attr_net6_dst, fwd_lookup_net6_dst)

static const struct classify_query_attr_handlers *fwd_query_vlan[] = {
	&fwd_attr_device,
	&fwd_attr_vlan,
};

static const struct classify_query_attr_handlers *fwd_query_ip4[] = {
	&fwd_attr_device,
	&fwd_attr_vlan,
	&fwd_attr_net4_src,
	&fwd_attr_net4_dst,
};

static const struct classify_query_attr_handlers *fwd_query_ip6[] = {
	&fwd_attr_device,
	&fwd_attr_vlan,
	&fwd_attr_net6_src,
	&fwd_attr_net6_dst,
};
