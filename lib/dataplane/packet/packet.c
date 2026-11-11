#include "packet.h"

#include "common/crc32.h"
#include "data.h"

#include <stdint.h>

#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_ether.h>
#include <rte_ip.h>

/*
 * TODO: analyze if the valid packet parsing may
 * overflow the 65535 value in an offset.
 */

static inline int
parse_ether_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ether_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, *offset);
	*type = ether_hdr->ether_type;
	*offset += sizeof(struct rte_ether_hdr);
	return 0;
}

static inline int
parse_vlan_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_vlan_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_vlan_hdr *vlan_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_vlan_hdr *, *offset);

	packet->vlan = rte_be_to_cpu_16(vlan_hdr->vlan_tci) & 0x0fff;

	*type = vlan_hdr->eth_proto;
	*offset += sizeof(struct rte_vlan_hdr);
	return 0;
}

inline int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ipv4_hdr)) {
		return -1;
	}

	const struct rte_ipv4_hdr *ipv4_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv4_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + rte_be_to_cpu_16(ipv4_hdr->total_length)) {
		return -1;
	}

	if ((ipv4_hdr->version_ihl & 0x0F) < 0x05) {
		return -1;
	}

	if (rte_be_to_cpu_16(ipv4_hdr->total_length) <
	    4 * (ipv4_hdr->version_ihl & 0x0F)) {
		return -1;
	}

	packet->hash = crc32(&ipv4_hdr->src_addr, 4, packet->hash);
	packet->hash = crc32(&ipv4_hdr->dst_addr, 4, packet->hash);

	if ((ipv4_hdr->fragment_offset &
	     RTE_BE16(RTE_IPV4_HDR_MF_FLAG | RTE_IPV4_HDR_OFFSET_MASK)) != 0) {
		packet->flags |= 1 << PACKET_FLAG_FRAGMENTED;

		packet->fragment_offset =
			rte_be_to_cpu_16(ipv4_hdr->fragment_offset) &
			RTE_IPV4_HDR_OFFSET_MASK;
	}

	// FIXME: process extensions

	*type = ipv4_hdr->next_proto_id;
	*offset += 4 * (ipv4_hdr->version_ihl & 0x0F);

	return 0;
}

inline int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ipv6_hdr)) {
		return -1;
	}

	const struct rte_ipv6_hdr *ipv6_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    *offset + sizeof(struct rte_ipv6_hdr) +
		    rte_be_to_cpu_16(ipv6_hdr->payload_len)) {
		return -1;
	}

	// walk through extensions
	*offset += sizeof(struct rte_ipv6_hdr);
	uint16_t max_offset = *offset + rte_be_to_cpu_16(ipv6_hdr->payload_len);
	uint8_t ext_type = ipv6_hdr->proto;
	while (*offset < max_offset) {
		if (ext_type == IPPROTO_HOPOPTS ||
		    ext_type == IPPROTO_ROUTING ||
		    ext_type == IPPROTO_DSTOPTS) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct yanet_ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct yanet_ipv6_ext_2byte *,
					*offset
				);

			ext_type = ext->next_header;
			*offset += (1 + ext->extension_length) * 8;

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_AH) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct yanet_ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct yanet_ipv6_ext_2byte *,
					*offset
				);

			ext_type = ext->next_header;
			*offset += (2 + ext->extension_length) * 4;
			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_FRAGMENT) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct yanet_ipv6_ext_fragment *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct yanet_ipv6_ext_fragment *,
					*offset
				);

			if ((ext->offset_flag &
			     RTE_BE16(RTE_IPV6_FRAG_USED_MASK)) != 0x0000) {
				packet->flags |= 1 << PACKET_FLAG_FRAGMENTED;
				packet->fragment_offset =
					rte_be_to_cpu_16(ext->offset_flag) &
					RTE_IPV6_EHDR_FO_MASK;
			}

			*offset += RTE_IPV6_FRAG_HDR_SIZE;

			ext_type = ext->next_header;

			// Bytes after a non-initial fragment are flow
			// payload, not extension headers, so traversal ends
			// here carrying the Fragment header's own Next
			// Header; whether a transport header eventually
			// follows stays unknowable from this fragment.
			if (packet->fragment_offset != 0) {
				break;
			}

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else {
			break;
		}
	}

	if (*offset > max_offset) {
		return -1;
	}

	packet->hash = crc32(ipv6_hdr->src_addr, 16, packet->hash);
	packet->hash = crc32(ipv6_hdr->dst_addr, 16, packet->hash);

	*type = ext_type;

	return 0;
}

int
parse_packet(struct packet *packet) {
	uint16_t type = 0;
	uint16_t offset = 0;

	packet->hash = 0;
	packet->flags = 0;
	packet->fragment_offset = 0;
	packet->data_len = rte_pktmbuf_data_len(packet_to_mbuf(packet));

	if (parse_ether_header(packet, &type, &offset)) {
		return -1;
	}

	if ((type == rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) &&
	    parse_vlan_header(packet, &type, &offset)) {
		return -1;
	}

	packet->network_header.type = type;
	packet->network_header.offset = offset;

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		if (parse_ipv4_header(packet, &type, &offset)) {
			return -1;
		}
	} else if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		if (parse_ipv6_header(packet, &type, &offset)) {
			return -1;
		}
	} else {
		// unknown header
		return 0;
	}

	// FIXME: separate routines for transport level parsing
	packet->transport_header.type = type;
	packet->transport_header.offset = offset;

	// A non-initial fragment carries flow payload where transport
	// headers should be, so recording one would let port and session
	// readers consume arbitrary bytes. Tag the declared protocol as
	// header-unavailable and skip the port hash: equality dispatch on
	// untagged protocol values keeps excluding the packet from header
	// reads, protocol-only classification stays possible, and the hash
	// stays the address-only one computed from the IP headers. Initial
	// fragments of the same datagram extend the hash with ports and
	// therefore do not share it.
	if ((packet->flags & (1 << PACKET_FLAG_FRAGMENTED)) != 0 &&
	    packet->fragment_offset != 0) {
		packet->transport_header.type |=
			PACKET_TRANSPORT_HEADER_UNAVAILABLE;
		return 0;
	}

	// TODO: should tcp/udp data be added to packet hash?
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (type == IPPROTO_TCP) {
		if (rte_pktmbuf_pkt_len(mbuf) <
		    (uint32_t)offset + sizeof(struct rte_tcp_hdr)) {
			return -1;
		}

		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_tcp_hdr *, offset
		);

		packet->hash = crc32(&tcp_hdr->src_port, 2, packet->hash);
		packet->hash = crc32(&tcp_hdr->dst_port, 2, packet->hash);
	} else if (type == IPPROTO_UDP) {
		if (rte_pktmbuf_pkt_len(mbuf) <
		    (uint32_t)offset + sizeof(struct rte_udp_hdr)) {
			return -1;
		}

		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_udp_hdr *, offset
		);

		packet->hash = crc32(&udp_hdr->src_port, 2, packet->hash);
		packet->hash = crc32(&udp_hdr->dst_port, 2, packet->hash);
	}

	return 0;
}
