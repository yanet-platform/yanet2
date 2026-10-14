#pragma once

/*
 * Packet lookups must follow the same attribute joins as compilation.
 *
 * Each leaf routine reads one attribute classifier the compile side
 * produced and yields the class of every packet of the batch; the network
 * getters are batched so the parsing loops amortize over the batch. The
 * dispatchers below pair the leaves with the joints of the classifier
 * structs exactly as the compile stages joined them.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <rte_tcp.h>

#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/packet.h"

#include "objects/l3b/api/l3b_virtual_service_object.h"

#include "config.h"

static inline void
l3b_packet_get_net4_src_batch(
	const struct packet **packets, uint32_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		// The head-length check protects the address read.
		// An unread address supplies a zero key, not a fixed class.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset +
			    sizeof(struct rte_ipv4_hdr)) {
			addrs[idx] = ipv4_hdr->src_addr;
		} else {
			addrs[idx] = 0;
		}
	}
}

static inline void
l3b_packet_get_net4_dst_batch(
	const struct packet **packets, uint32_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		// The head-length check protects the address read.
		// An unread address supplies a zero key, not a fixed class.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset +
			    sizeof(struct rte_ipv4_hdr)) {
			addrs[idx] = ipv4_hdr->dst_addr;
		} else {
			addrs[idx] = 0;
		}
	}
}

static inline void
l3b_packet_get_net6_src_batch(
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
		// The head-length check protects the address read.
		// An unread address supplies a zero key, not a fixed class.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset +
			    sizeof(struct rte_ipv6_hdr)) {
			rte_mov16(addrs + idx * NET6_LEN, ipv6_hdr->src_addr);
		} else {
			memset(addrs + idx * NET6_LEN, 0, NET6_LEN);
		}
	}
}

static inline void
l3b_packet_get_net6_dst_batch(
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
		// The head-length check protects the address read.
		// An unread address supplies a zero key, not a fixed class.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset +
			    sizeof(struct rte_ipv6_hdr)) {
			rte_mov16(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr);
		} else {
			memset(addrs + idx * NET6_LEN, 0, NET6_LEN);
		}
	}
}

/*
 * Transport keys encode protocol in the high byte and subtype in the low
 * byte: TCP flags or ICMP/ICMPv6 type.
 *
 * The head-length check protects each subtype read; if unread, its key
 * byte stays zero. Module dispatch excludes fragments and rejects required
 * headers outside the head before this lookup. Whole-packet length alone
 * does not establish head readability.
 */
static inline void
l3b_lookup_proto(
	const struct classify_attr_port *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		uint8_t proto = packet_transport_protocol(packet);
		uint32_t lookup = (uint32_t)proto << 8;

		uint32_t subtype_offset = 0;
		if (proto == IPPROTO_TCP) {
			subtype_offset =
				offsetof(struct rte_tcp_hdr, tcp_flags);
		} else if (proto == IPPROTO_ICMP || proto == IPPROTO_ICMPV6) {
			subtype_offset = 0;
		} else {
			// The protocols without a subtype byte resolve at the
			// plain protocol slot.
			results[idx] =
				vline_get((struct vline *)&attr->line, lookup);
			continue;
		}

		if (rte_pktmbuf_data_len(mbuf) >
		    packet->transport_header.offset + subtype_offset) {
			uint8_t *subtype = (uint8_t *)rte_pktmbuf_mtod_offset(
				mbuf,
				uint8_t *,
				packet->transport_header.offset + subtype_offset
			);
			lookup += *subtype;
		}

		results[idx] = vline_get((struct vline *)&attr->line, lookup);
	}
}

// The service port attribute: the destination port of the TCP or the UDP
// segment, zero for anything else.
static inline void
l3b_lookup_port_dst(
	const struct classify_attr_port *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		uint16_t port = 0;

		// Source admission uses the destination service port.
		//
		// The head-length check protects the four-byte port pair read;
		// its low half after byte-order conversion is the destination.
		if (packet->transport_header.type == IPPROTO_TCP ||
		    packet->transport_header.type == IPPROTO_UDP) {
			if (rte_pktmbuf_data_len(mbuf) >=
			    packet->transport_header.offset + 4) {
				uint32_t raw;
				memcpy(&raw,
				       rte_pktmbuf_mtod_offset(
					       mbuf,
					       const void *,
					       packet->transport_header.offset
				       ),
				       sizeof(raw));
				port = rte_be_to_cpu_32(raw);
			}
		}

		results[idx] = vline_get((struct vline *)&attr->line, port);
	}
}

// Fixed-size chunks bound dispatcher scratch independently of batch size.
#define L3B_CLASSIFY_MAX_BATCH 256

/*
 * IPv4 destination lookup preserves first-match rule precedence.
 */
static inline void
l3b_classify_destination_ip4(
	const struct l3b_destination_classifier_ip4 *cls,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t nets[L3B_CLASSIFY_MAX_BATCH];
	uint32_t protos[L3B_CLASSIFY_MAX_BATCH];
	uint32_t addrs4[L3B_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += L3B_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < L3B_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : L3B_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		l3b_packet_get_net4_dst_batch(batch, addrs4, count);
		classify_net4_lookup(&cls->dst_attr, addrs4, nets, count);

		l3b_lookup_proto(&cls->proto_attr, batch, protos, count);

		classify_combine(
			&cls->root_joint,
			&cls->rule_map,
			nets,
			protos,
			results + off,
			count
		);
	}
}

// IPv6 destination lookup preserves first-match rule precedence.
static inline void
l3b_classify_destination_ip6(
	const struct l3b_destination_classifier_ip6 *cls,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t nets[L3B_CLASSIFY_MAX_BATCH];
	uint32_t protos[L3B_CLASSIFY_MAX_BATCH];
	uint8_t addrs6[L3B_CLASSIFY_MAX_BATCH][NET6_LEN];

	for (uint32_t off = 0; off < packet_count;
	     off += L3B_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < L3B_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : L3B_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		l3b_packet_get_net6_dst_batch(batch, addrs6[0], count);
		classify_net6_lookup(&cls->dst_attr, addrs6[0], nets, count);

		l3b_lookup_proto(&cls->proto_attr, batch, protos, count);

		classify_combine(
			&cls->root_joint,
			&cls->rule_map,
			nets,
			protos,
			results + off,
			count
		);
	}
}

/*
 * Source admission requires a matching client network and service port.
 * With no source rules, ordinary transport admission fails.
 *
 * Module dispatch supplies head-resident required headers. Matched Echo
 * requests bypass this predicate in service processing.
 */
static inline bool
l3b_source_filter_matches(
	const struct virtual_service *virtual_service,
	const struct packet *packet
) {
	if (virtual_service->source_filter_rule_count == 0) {
		return false;
	}

	const struct packet *packets[1] = {packet};
	uint32_t nets[1];
	uint32_t ports[1];
	uint32_t result[1];
	uint16_t type = packet->network_header.type;

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		const struct l3b_source_classifier_ip4 *cls =
			&virtual_service->classifier_ip4;
		uint32_t addrs4[1];

		l3b_packet_get_net4_src_batch(packets, addrs4, 1);
		classify_net4_lookup(&cls->src_attr, addrs4, nets, 1);

		l3b_lookup_port_dst(&cls->port_attr, packets, ports, 1);

		classify_combine(
			&cls->root_joint, &cls->rule_map, nets, ports, result, 1
		);
		return result[0] != CLASSIFY_RULE_INVALID;
	}

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		const struct l3b_source_classifier_ip6 *cls =
			&virtual_service->classifier_ip6;
		uint8_t addrs6[1][NET6_LEN];

		l3b_packet_get_net6_src_batch(packets, addrs6[0], 1);
		classify_net6_lookup(&cls->src_attr, addrs6[0], nets, 1);

		l3b_lookup_port_dst(&cls->port_attr, packets, ports, 1);

		classify_combine(
			&cls->root_joint, &cls->rule_map, nets, ports, result, 1
		);
		return result[0] != CLASSIFY_RULE_INVALID;
	}

	return false;
}
