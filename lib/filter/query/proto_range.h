#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stddef.h>
#include <stdint.h>

#include <netinet/in.h>
#include <rte_icmp.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

static inline void
FILTER_ATTR_QUERY_FUNC(proto_range)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct packet *packet = packets[idx];

		uint16_t transport_type = packet->transport_header.type;
		uint32_t proto = packet_transport_protocol(packet);

		// Every byte this query reads must be inside the packet:
		// TCP flags sit at the fourteenth header byte, the ICMP and
		// ICMPv6 type at the first one. Whatever is shorter joins
		// the unavailable path below.
		uint32_t read_size = 1;
		if (transport_type == IPPROTO_TCP) {
			read_size = offsetof(struct rte_tcp_hdr, tcp_flags) + 1;
		}

		if ((transport_type & PACKET_TRANSPORT_HEADER_UNAVAILABLE) !=
			    0 ||
		    rte_pktmbuf_pkt_len(packet_to_mbuf(packet)
		    ) < (uint32_t)packet->transport_header.offset + read_size) {
			// The declared protocol is known but its header is
			// fragment payload, or too short to hold the bytes
			// this query reads: match only rules covering the
			// whole protocol block, never a fabricated subtype.
			// The dedicated classes carry their result directly,
			// while other protocols never read a subtype and
			// keep their plain slot.
			switch (proto) {
			case IPPROTO_TCP:
				result[idx] = c->unavailable_classes
						      [PROTO_UNAVAILABLE_TCP];
				continue;
			case IPPROTO_ICMP:
				result[idx] = c->unavailable_classes
						      [PROTO_UNAVAILABLE_ICMP];
				continue;
			case IPPROTO_ICMPV6:
				result[idx] =
					c->unavailable_classes
						[PROTO_UNAVAILABLE_ICMPV6];
				continue;
			default:
				result[idx] = vline_get(&c->line, proto * 256);
				continue;
			}
		}

		uint32_t lookup = proto * 256;
		if (transport_type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_header =
				rte_pktmbuf_mtod_offset(
					packet_to_mbuf(packet),
					struct rte_tcp_hdr *,
					packet->transport_header.offset
				);
			lookup += tcp_header->tcp_flags;
		}
		if (transport_type == IPPROTO_ICMP) {
			struct rte_icmp_hdr *icmp_header =
				rte_pktmbuf_mtod_offset(
					packet_to_mbuf(packet),
					struct rte_icmp_hdr *,
					packet->transport_header.offset
				);
			lookup += icmp_header->icmp_type;
		}
		if (transport_type == IPPROTO_ICMPV6) {
			struct rte_icmp_hdr *icmp_header =
				rte_pktmbuf_mtod_offset(
					packet_to_mbuf(packet),
					struct rte_icmp_hdr *,
					packet->transport_header.offset
				);
			lookup += icmp_header->icmp_type;
		}

		result[idx] = vline_get(&c->line, lookup);
	}
}
