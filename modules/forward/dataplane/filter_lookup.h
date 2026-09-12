#pragma once

/*
 * Module authored attribute lookups of the forward filters.
 *
 * Each routine reads the classifier the compile side produced and
 * yields the region class of every packet of the batch: the packet
 * getters are called directly, so their parsing is inlined into the
 * lookup body, and the network getters are batched so the parsing
 * loops amortize over the batch. The arrays at the bottom pair with
 * the FILTER_COMPILER_DECLARE signatures of
 * modules/forward/api/controlplane.c by position.
 */

#include <stdint.h>
#include <string.h>

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/dataplane/packet/packet.h"

#include "lib/filter2/classifiers/device.h"
#include "lib/filter2/classifiers/net4.h"
#include "lib/filter2/classifiers/net6.h"
#include "lib/filter2/classifiers/vlan.h"

#include "lib/filter2/query/declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

static inline uint32_t
forward_packet_get_device(const struct packet *packet) {
	return packet->module_device_id;
}

static inline void
forward_lookup_device(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct filter_query_attr_device *device_attr =
		container_of(attr, struct filter_query_attr_device, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		uint32_t device_id = forward_packet_get_device(packets[idx]);
		if (device_id >= device_attr->line.size) {
			device_id = 0;
		}
		results[idx] = vline_get(&device_attr->line, device_id);
	}
}

FILTER_QUERY_ATTR(forward_attr_device, forward_lookup_device)

static inline uint32_t
forward_packet_get_vlan(const struct packet *packet) {
	return packet->vlan;
}

static inline void
forward_lookup_vlan(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct filter_query_attr_vlan *vlan_attr =
		container_of(attr, struct filter_query_attr_vlan, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t vlan_id = forward_packet_get_vlan(packets[idx]);
		results[idx] = vline_get(&vlan_attr->line, vlan_id);
	}
}

FILTER_QUERY_ATTR(forward_attr_vlan, forward_lookup_vlan)

#define FORWARD_LOOKUP_NET4(variant, getter)                                   \
	static inline void forward_lookup_##variant(                           \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_net4 *attr_net4 = container_of( \
			attr, const struct filter_query_attr_net4, attr        \
		);                                                             \
		uint8_t addrs[packet_count][NET4_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] = vline_get(                              \
				&attr_net4->line,                              \
				lpm4_lookup(&attr_net4->lpm, addrs[idx])       \
			);                                                     \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(forward_attr_##variant, forward_lookup_##variant)

static inline void
forward_packet_get_net4_src_batch(
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
forward_packet_get_net4_dst_batch(
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

FORWARD_LOOKUP_NET4(net4_src, forward_packet_get_net4_src_batch)
FORWARD_LOOKUP_NET4(net4_dst, forward_packet_get_net4_dst_batch)

#define FORWARD_LOOKUP_NET6(variant, getter)                                   \
	static inline void forward_lookup_##variant(                           \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_net6 *attr_net6 = container_of( \
			attr, const struct filter_query_attr_net6, attr        \
		);                                                             \
		uint32_t *row_scalar = ADDR_OF(&attr_net6->row_scalar);        \
		uint32_t *row_index = ADDR_OF(&attr_net6->row_index);          \
		uint8_t addrs[packet_count][NET6_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			const uint8_t *addr = addrs[idx];                      \
			uint32_t hi = lpm8_lookup(&attr_net6->hi, addr);       \
			uint32_t scalar = row_scalar[hi];                      \
			if (scalar != FILTER_NET6_ROW_2D) {                    \
				results[idx] = scalar;                         \
			} else {                                               \
				uint32_t lo =                                  \
					lpm8_lookup(&attr_net6->lo, addr + 8); \
				results[idx] = value_table_get(                \
					&attr_net6->comb, row_index[hi], lo    \
				);                                             \
			}                                                      \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(forward_attr_##variant, forward_lookup_##variant)

static inline void
forward_packet_get_net6_src_batch(
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
forward_packet_get_net6_dst_batch(
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

FORWARD_LOOKUP_NET6(net6_src, forward_packet_get_net6_src_batch)
FORWARD_LOOKUP_NET6(net6_dst, forward_packet_get_net6_dst_batch)

/*
 * Query signatures, pairing by position with the
 * FILTER_COMPILER_DECLARE signatures of
 * modules/forward/api/controlplane.c: device, vlan, net{4,6}_src,
 * net{4,6}_dst.
 */
static const struct filter_query_attr_handlers *forward_query_vlan[] = {
	&forward_attr_device,
	&forward_attr_vlan,
};

static const struct filter_query_attr_handlers *forward_query_ip4[] = {
	&forward_attr_device,
	&forward_attr_vlan,
	&forward_attr_net4_src,
	&forward_attr_net4_dst,
};

static const struct filter_query_attr_handlers *forward_query_ip6[] = {
	&forward_attr_device,
	&forward_attr_vlan,
	&forward_attr_net6_src,
	&forward_attr_net6_dst,
};
