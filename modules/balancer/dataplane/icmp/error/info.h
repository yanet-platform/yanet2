#pragma once

#include "dataplane/packet/packet.h"
#include "rte_byteorder.h"
#include "rte_ether.h"
#include "rte_ip.h"

////////////////////////////////////////////////////////////////////////////////

struct icmp_packet_info {
	// ICMP packet layout:
	// [NETWORK | ICMP | /* inner */ NETWORK | /* inner */ TRANSPORT]
	struct network_header network;
	struct transport_header transport;
};

////////////////////////////////////////////////////////////////////////////////

struct ipv6_extension {
	uint8_t next_header;
	uint8_t extension_length;
} __attribute__((__packed__));

struct ipv6_extension_fragment {
	uint8_t next_header;
	uint8_t reserved;
	uint16_t offset_flag_m;
	uint32_t identification;
} __attribute__((__packed__));

////////////////////////////////////////////////////////////////////////////////

#define PACKET_INFO_UNKNOWN ((uint16_t)-1)
#define PACKET_INFO_EXTENSIONS_MAX ((uint32_t)32)
#define PACKET_INFO_EXTENSION_SIZE_MAX ((uint32_t)16)

////////////////////////////////////////////////////////////////////////////////

static inline int
fill_icmp_packet_info_ipv4(
	struct rte_mbuf *mbuf, struct icmp_packet_info *info
) {
	const struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, info->network.offset
	);

	/// @todo: check version

	// check the entire ip packet encapsulated
	// in the icmp packet.
	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)info->network.offset +
		    rte_be_to_cpu_16(ipv4_hdr->total_length)) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	}

	if ((ipv4_hdr->version_ihl & 0x0F) < 0x05) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	} else {

		info->transport.type = ipv4_hdr->next_proto_id;
		info->transport.offset = info->network.offset +
					 4 * (ipv4_hdr->version_ihl & 0x0F);
	}

	if (rte_be_to_cpu_16(ipv4_hdr->total_length) <
	    4 * (ipv4_hdr->version_ihl & 0x0F)) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	}

	return 0;
}

static inline int
fill_icmp_packet_info_ipv6(
	struct rte_mbuf *mbuf, struct icmp_packet_info *info
) {
	const struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, info->network.offset
	);

	/// @todo: check version

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)info->network.offset + sizeof(struct rte_ipv6_hdr) +
		    rte_be_to_cpu_16(ipv6_hdr->payload_len)) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	}

	uint8_t transport_hdr_type = ipv6_hdr->proto;
	uint16_t transport_hdr_offset =
		info->network.offset + sizeof(struct rte_ipv6_hdr);

	unsigned int extension_i = 0;
	for (extension_i = 0; extension_i < PACKET_INFO_EXTENSIONS_MAX + 1;
	     extension_i++) {
		if (transport_hdr_type == IPPROTO_HOPOPTS ||
		    transport_hdr_type == IPPROTO_ROUTING ||
		    transport_hdr_type == IPPROTO_DSTOPTS) {
			const struct ipv6_extension *extension =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_extension *,
					transport_hdr_offset
				);

			if (extension->extension_length >
			    PACKET_INFO_EXTENSION_SIZE_MAX) {
				info->network.type = PACKET_INFO_UNKNOWN;
				return -1;
			}

			transport_hdr_type = extension->next_header;
			transport_hdr_offset +=
				8 + extension->extension_length * 8;
		} else if (transport_hdr_type == IPPROTO_FRAGMENT) {
			const struct ipv6_extension_fragment *extension =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_extension_fragment *,
					transport_hdr_offset
				);

			transport_hdr_type = extension->next_header;
			transport_hdr_offset += 8;

			/** @todo: last extension?
			info->transport.type = transport_headerType;
			info->transport.offset = transport_headerOffset;

			break;
			*/
		} else {
			info->transport.type = transport_hdr_type;
			info->transport.offset = transport_hdr_offset;

			break;
		}
	}
	if (extension_i == PACKET_INFO_EXTENSIONS_MAX + 1) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	}

	if (rte_be_to_cpu_16(ipv6_hdr->payload_len) <
	    info->transport.offset - info->network.offset -
		    sizeof(struct rte_ipv6_hdr)) {
		info->network.type = PACKET_INFO_UNKNOWN;
		return -1;
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
fill_icmp_packet_info(struct rte_mbuf *mbuf, struct icmp_packet_info *info) {
	if (info->network.type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		return fill_icmp_packet_info_ipv4(mbuf, info);
	} else if (info->network.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		return fill_icmp_packet_info_ipv6(mbuf, info);
	} else {
		// unknown
		return -1;
	}
}
