#pragma once

/*
 * Module authored attribute lookups of the acl filters.
 *
 * Each routine reads the classifier the compile side produced and
 * yields the class of every packet of the batch: the packet getters
 * are called directly, so their parsing is inlined into the lookup
 * body, and the network and port getters are batched so the parsing
 * loops amortize over the batch. The arrays at the bottom pair with
 * the classifier compositions of modules/acl/api/controlplane.c by
 * tape leaf order.
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
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"
#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classifiers/proto_range.h"
#include "lib/classify/classifiers/vlan.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

static inline uint32_t
acl_packet_get_device(const struct packet *packet) {
	return packet->module_device_id;
}

static inline void
acl_lookup_device(
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
		uint32_t device_id = acl_packet_get_device(packets[idx]);
		if (device_id >= device_attr->value_table.h_dim) {
			device_id = 0;
		}
		results[idx] = value_table_get(
			&device_attr->value_table, 0, device_id
		);
	}
}

static inline uint32_t
acl_packet_get_vlan(const struct packet *packet) {
	return packet->vlan;
}

static inline void
acl_lookup_vlan(
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
		const uint16_t vlan_id = acl_packet_get_vlan(packets[idx]);
		results[idx] =
			value_table_get(&vlan_attr->value_table, 0, vlan_id);
	}
}

static inline void
acl_lookup_ipfrag(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct classify_query_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_query_attr_ipfrag, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] =
			value_table_get(&ipfrag_attr->value_table, 0, id);
	}
}

// The parser validates the transport header against the whole packet
// length, so a chained packet can carry it past the head segment; the
// reads below are bounded by the bytes actually classified.
static inline uint16_t
acl_packet_get_proto(const struct packet *packet) {
	uint16_t proto = packet->transport_header.type * 256;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// The TCP flags byte sits after the ports and the sequence and
	// acknowledgment numbers.
	if (packet->transport_header.type == IPPROTO_TCP) {
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->transport_header.offset + 14) {
			struct rte_tcp_hdr *tcp_hdr =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_tcp_hdr *,
					packet->transport_header.offset
				);
			proto += tcp_hdr->tcp_flags;
		}
	} else if (packet->transport_header.type == IPPROTO_ICMP ||
		   packet->transport_header.type == IPPROTO_ICMPV6) {
		// Only the leading type byte of the message is classified;
		// a bare echo header carries it without the rest of the
		// full struct.
		if (rte_pktmbuf_data_len(mbuf) >
		    packet->transport_header.offset) {
			proto += *((uint8_t *)rte_pktmbuf_mtod_offset(
				mbuf,
				uint8_t *,
				packet->transport_header.offset
			));
		}
	}
	return proto;
}

static inline void
acl_lookup_proto(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;

	const struct classify_query_attr_proto_range *proto_attr =
		container_of(attr, struct classify_query_attr_proto_range, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		results[idx] = vline_get(
			(struct vline *)&proto_attr->line,
			acl_packet_get_proto(packets[idx])
		);
	}
}

static inline void
acl_packet_get_net4_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
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
			mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->dst_addr, NET4_LEN);
	}
}

#define ACL_LOOKUP_NET4(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
		const struct classify_query_attr *attr,                        \
		const struct classify_query_attr_handlers *handlers,           \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                           \
	) {                                                                    \
		(void)handlers;                                                \
		const struct classify_query_attr_net4 *attr_net4 =             \
			container_of(                                          \
				attr, struct classify_query_attr_net4, attr     \
			);                                                     \
		uint8_t addrs[packet_count][NET4_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] = lpm4_lookup(&attr_net4->lpm, addrs[idx]); \
		}                                                              \
	}

ACL_LOOKUP_NET4(net4_src, acl_packet_get_net4_src_batch)
ACL_LOOKUP_NET4(net4_dst, acl_packet_get_net4_dst_batch)

static inline void
acl_packet_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
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
			mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr, NET6_LEN);
	}
}

#define ACL_LOOKUP_NET6(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
		const struct classify_query_attr *attr,                        \
		const struct classify_query_attr_handlers *handlers,           \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                           \
	) {                                                                    \
		(void)handlers;                                                \
		const struct classify_query_attr_net6 *attr_net6 =             \
			container_of(                                          \
				attr, struct classify_query_attr_net6, attr     \
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
			uint32_t lo = lpm8_lookup(&attr_net6->lo, addr + 8);  \
			results[idx] = *value_table_get_ptr(                   \
				&attr_net6->comb,                               \
				hi & ~FILTER_NET6_ROW_MARK,                     \
				lo                                             \
			);                                                     \
		}                                                              \
	}

ACL_LOOKUP_NET6(net6_src, acl_packet_get_net6_src_batch)
ACL_LOOKUP_NET6(net6_dst, acl_packet_get_net6_dst_batch)

static inline void
acl_packet_get_port_src_batch(
	const struct packet **packets, uint16_t *ports, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		ports[idx] = 0;

		if (packet->transport_header.type == IPPROTO_TCP) {
			// The port pair occupies the first four bytes of the
			// segment; the rest of the header can sit in a later
			// segment of a chained packet.
			if (rte_pktmbuf_data_len(mbuf) <
			    packet->transport_header.offset + 4) {
				continue;
			}
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->src_port);
		} else if (packet->transport_header.type == IPPROTO_UDP) {
			if (rte_pktmbuf_data_len(mbuf) <
			    packet->transport_header.offset + 4) {
				continue;
			}
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
		ports[idx] = 0;

		if (packet->transport_header.type == IPPROTO_TCP) {
			if (rte_pktmbuf_data_len(mbuf) <
			    packet->transport_header.offset + 4) {
				continue;
			}
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->dst_port);
		} else if (packet->transport_header.type == IPPROTO_UDP) {
			if (rte_pktmbuf_data_len(mbuf) <
			    packet->transport_header.offset + 4) {
				continue;
			}
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(udp_hdr->dst_port);
		}
	}
}

#define ACL_LOOKUP_PORT(variant, getter)                                       \
	static inline void acl_lookup_##variant(                               \
		const struct classify_query_attr *attr,                        \
		const struct classify_query_attr_handlers *handlers,           \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                           \
	) {                                                                    \
		(void)handlers;                                                \
		const struct classify_query_attr_port *port_attr =             \
			container_of(                                          \
				attr, struct classify_query_attr_port, attr     \
			);                                                     \
		uint16_t ports[packet_count];                                  \
		getter(packets, ports, packet_count);                          \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] = vline_get(                              \
				(struct vline *)&port_attr->line, ports[idx]    \
			);                                                     \
		}                                                              \
	}

ACL_LOOKUP_PORT(port_src, acl_packet_get_port_src_batch)
ACL_LOOKUP_PORT(port_dst, acl_packet_get_port_dst_batch)

CLASSIFY_QUERY_ATTR(acl_attr_device, acl_lookup_device)
CLASSIFY_QUERY_ATTR(acl_attr_vlan, acl_lookup_vlan)
CLASSIFY_QUERY_ATTR(acl_attr_ipfrag, acl_lookup_ipfrag)
CLASSIFY_QUERY_ATTR(acl_attr_proto, acl_lookup_proto)
CLASSIFY_QUERY_ATTR(acl_attr_net4_src, acl_lookup_net4_src)
CLASSIFY_QUERY_ATTR(acl_attr_net4_dst, acl_lookup_net4_dst)
CLASSIFY_QUERY_ATTR(acl_attr_net6_src, acl_lookup_net6_src)
CLASSIFY_QUERY_ATTR(acl_attr_net6_dst, acl_lookup_net6_dst)
CLASSIFY_QUERY_ATTR(acl_attr_port_src, acl_lookup_port_src)
CLASSIFY_QUERY_ATTR(acl_attr_port_dst, acl_lookup_port_dst)

// Query signatures in classifier tape leaf order: the plain family
// filters join the ip fragment leaf after the family core, the port
// scoped filters join the ports pair.
static const struct classify_query_attr_handlers *acl_query_vlan[] = {
	&acl_attr_device,
	&acl_attr_vlan,
};

static const struct classify_query_attr_handlers *acl_query_ip4[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net4_src,
	&acl_attr_net4_dst,
	&acl_attr_proto,
	&acl_attr_ipfrag,
};

static const struct classify_query_attr_handlers *acl_query_ip4_port[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net4_src,
	&acl_attr_net4_dst,
	&acl_attr_proto,
	&acl_attr_port_src,
	&acl_attr_port_dst,
};

static const struct classify_query_attr_handlers *acl_query_ip6[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net6_src,
	&acl_attr_net6_dst,
	&acl_attr_proto,
	&acl_attr_ipfrag,
};

static const struct classify_query_attr_handlers *acl_query_ip6_port[] = {
	&acl_attr_device,
	&acl_attr_vlan,
	&acl_attr_net6_src,
	&acl_attr_net6_dst,
	&acl_attr_proto,
	&acl_attr_port_src,
	&acl_attr_port_dst,
};
