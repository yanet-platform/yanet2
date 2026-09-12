#pragma once

/*
 * Module authored attribute lookups of the acl filters.
 *
 * Each routine reads the classifier the compile side produced and
 * yields the region class of every packet of the batch: the packet
 * getters are called directly, so their parsing is inlined into the
 * lookup body, and the network and port getters are batched so the
 * parsing loops amortize over the batch. The arrays at the bottom pair
 * with the FILTER_COMPILER_DECLARE signatures of
 * modules/acl/api/controlplane.c by position.
 */

#include <netinet/in.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>
#include <stdint.h>
#include <string.h>

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/dataplane/packet/packet.h"

#include "lib/filter2/classifiers/device.h"
#include "lib/filter2/classifiers/ipfrag.h"
#include "lib/filter2/classifiers/net4.h"
#include "lib/filter2/classifiers/net6.h"
#include "lib/filter2/classifiers/port.h"
#include "lib/filter2/classifiers/proto_range.h"
#include "lib/filter2/classifiers/vlan.h"

#include "lib/filter2/query/declare.h"

static inline uint32_t
acl_packet_get_device(const struct packet *packet) {
	return packet->module_device_id;
}

static inline void
acl_lookup_device(
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
		uint32_t device_id = acl_packet_get_device(packets[idx]);
		if (device_id >= device_attr->line.size) {
			device_id = 0;
		}
		results[idx] = vline_get(&device_attr->line, device_id);
	}
}

FILTER_QUERY_ATTR(acl_attr_device, acl_lookup_device)

static inline uint32_t
acl_packet_get_vlan(const struct packet *packet) {
	return packet->vlan;
}

static inline void
acl_lookup_vlan(
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
		const uint16_t vlan_id = acl_packet_get_vlan(packets[idx]);
		results[idx] = vline_get(&vlan_attr->line, vlan_id);
	}
}

FILTER_QUERY_ATTR(acl_attr_vlan, acl_lookup_vlan)

static inline void
acl_lookup_ip_frag(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct filter_query_attr_ip_frag *ipfrag_attr =
		container_of(attr, struct filter_query_attr_ip_frag, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] = vline_get(&ipfrag_attr->line, id);
	}
}

FILTER_QUERY_ATTR(acl_attr_ip_frag, acl_lookup_ip_frag)

static inline uint16_t
acl_packet_get_proto_range(const struct packet *packet) {
	uint16_t proto = packet->transport_header.type * 256;
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		proto += tcp_header->tcp_flags;
	}
	if (packet->transport_header.type == IPPROTO_ICMP) {
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		proto += icmp_header->icmp_type;
	}
	if (packet->transport_header.type == IPPROTO_ICMPV6) {
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		proto += icmp_header->icmp_type;
	}
	return proto;
}

static inline void
acl_lookup_proto_range(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct filter_query_attr_proto_range *proto_range_attr =
		container_of(attr, struct filter_query_attr_proto_range, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t proto_range =
			acl_packet_get_proto_range(packets[idx]);
		results[idx] = vline_get(&proto_range_attr->line, proto_range);
	}
}

FILTER_QUERY_ATTR(acl_attr_proto_range, acl_lookup_proto_range)

#define ACL_LOOKUP_NET4(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
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
	FILTER_QUERY_ATTR(acl_attr_##variant, acl_lookup_##variant)

static inline void
acl_packet_get_net4_src_batch(
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
acl_packet_get_net4_dst_batch(
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

ACL_LOOKUP_NET4(net4_src, acl_packet_get_net4_src_batch)
ACL_LOOKUP_NET4(net4_dst, acl_packet_get_net4_dst_batch)

#define ACL_LOOKUP_NET6(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
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
	FILTER_QUERY_ATTR(acl_attr_##variant, acl_lookup_##variant)

static inline void
acl_packet_get_net6_src_batch(
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
acl_packet_get_net6_dst_batch(
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

ACL_LOOKUP_NET6(net6_src, acl_packet_get_net6_src_batch)
ACL_LOOKUP_NET6(net6_dst, acl_packet_get_net6_dst_batch)

#define ACL_LOOKUP_PORT(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_port *port_attr = container_of( \
			attr, const struct filter_query_attr_port, attr        \
		);                                                             \
		uint16_t ports[packet_count];                                  \
		getter(packets, ports, packet_count);                          \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] =                                         \
				vline_get(&port_attr->line, ports[idx]);       \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(acl_attr_##variant, acl_lookup_##variant)

static inline void
acl_packet_get_port_src_batch(
	const struct packet **packets, uint16_t *ports, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (packet->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->src_port);
		} else {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(udp_hdr->src_port);
		}
	}
}

static inline void
acl_packet_get_port_dst_batch(
	const struct packet **packets, uint16_t *ports, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (packet->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->dst_port);
		} else {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(udp_hdr->dst_port);
		}
	}
}

ACL_LOOKUP_PORT(port_src, acl_packet_get_port_src_batch)
ACL_LOOKUP_PORT(port_dst, acl_packet_get_port_dst_batch)

/*
 * Query signatures, pairing by position with the FILTER_COMPILER_DECLARE
 * signatures of modules/acl/api/controlplane.c: device, vlan,
 * net{4,6}_src, net{4,6}_dst, ip_frag, proto_range, port_src, port_dst.
 */
static const struct filter_query_attr_handlers *acl_query_vlan[] = {
	&acl_attr_device,
	&acl_attr_vlan,
};

static const struct filter_query_attr_handlers *acl_query_ip4[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net4_src,
	&acl_attr_net4_dst,
	&acl_attr_ip_frag,
	&acl_attr_proto_range,
};

static const struct filter_query_attr_handlers *acl_query_ip4_port[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net4_src,
	&acl_attr_net4_dst,
	&acl_attr_proto_range,
	&acl_attr_port_src,
	&acl_attr_port_dst,
};

static const struct filter_query_attr_handlers *acl_query_ip6[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net6_src,
	&acl_attr_net6_dst,
	&acl_attr_ip_frag,
	&acl_attr_proto_range,
};

static const struct filter_query_attr_handlers *acl_query_ip6_port[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net6_src,
	&acl_attr_net6_dst,
	&acl_attr_proto_range,
	&acl_attr_port_src,
	&acl_attr_port_dst,
};
