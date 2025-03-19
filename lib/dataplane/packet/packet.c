#include "packet.h"

#include <stdint.h>

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

	packet->vlan = rte_be_to_cpu_16(vlan_hdr->vlan_tci);

	*type = vlan_hdr->eth_proto;
	*offset += sizeof(struct rte_vlan_hdr);
	return 0;
}

inline int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ipv4_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ipv4_hdr *ipv4_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv4_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + rte_be_to_cpu_16(ipv4_hdr->total_length)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	if ((ipv4_hdr->version_ihl & 0x0F) < 0x05) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	if (rte_be_to_cpu_16(ipv4_hdr->total_length) <
	    4 * (ipv4_hdr->version_ihl & 0x0F)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	// FIXME: check if fragmented
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
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ipv6_hdr *ipv6_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    *offset + sizeof(struct rte_ipv6_hdr) +
		    rte_be_to_cpu_16(ipv6_hdr->payload_len)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
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

			const struct ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf, struct ipv6_ext_2byte *, *offset
				);

			ext_type = ext->next_type;
			*offset += (1 + ext->size) * 8;

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_AH) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf, struct ipv6_ext_2byte *, *offset
				);

			ext_type = ext->next_type;
			*offset += (2 + ext->size) * 4;
			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_FRAGMENT) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct ipv6_ext_fragment *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_ext_fragment *,
					*offset
				);

			if ((ext->offset_flag & 0xF9FF) != 0x0000) {
				// FIXME: NETWORK_FLAG_FRAGMENT
				if ((ext->offset_flag & 0xF8FF) != 0x0000) {
					// FIXME:
					// NETWORK_FLAG_NOT_FIRST_FRAGMENT;
				}
			}

			ext_type = ext->next_type;
			*offset += RTE_IPV6_FRAG_HDR_SIZE;

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else {
			break;
		}
	}

	if (*offset > max_offset) {
		return -1;
	}

	*type = ext_type;

	return 0;
}

int
parse_packet(struct packet *packet) {
	uint16_t type = 0;
	uint16_t offset = 0;

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
		return -1;
	}

	// FIXME: separate routines for transport level parsing
	packet->transport_header.type = type;
	packet->transport_header.offset = offset;

	return 0;
}

struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf) {
	return (struct packet *)((void *)mbuf->buf_addr);
}
